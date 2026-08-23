package cmd

// doctor_launch_account_test.go covers the four Phase 126 Plan 09 doctor
// checks (REQ-126-DOCTOR): link well-formed (checkLaunchAccountLinks),
// launcher assumable (checkLaunchAccountAssumable), artifacts grant present
// (checkLaunchAccountArtifactsGrant), and orphaned linked-account instances
// (checkLaunchAccountOrphanInstances). Every stub client below counts its own
// calls so the dormancy tests (Test 1 in both tasks) can assert zero AWS
// calls when no links are configured — not just a SKIPPED status.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"

	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// =============================================================================
// Shared fixtures
// =============================================================================

// wellFormedLaunchLink returns a link record that satisfies every field
// checkLaunchAccountLinks validates, so tests can mutate a single field to
// isolate one defect at a time.
func wellFormedLaunchLink() appcfg.LaunchAccountConfig {
	return appcfg.LaunchAccountConfig{
		AccountID:         "222222222222",
		LauncherRoleARN:   "arn:aws:iam::222222222222:role/km-launcher",
		BoxRoleARN:        "arn:aws:iam::222222222222:role/km-box",
		ExternalIDSSM:     "/km/launch-accounts/gpu-east/external-id",
		Region:            "us-east-2",
		SubnetIDs:         []string{"subnet-aaa", "subnet-bbb"},
		AvailabilityZones: []string{"us-east-2a", "us-east-2b"},
		ResultsBucket:     "km-launch-results",
	}
}

// =============================================================================
// Shared stubs — all count their own calls
// =============================================================================

// countingSSMReadStub implements SSMReadAPI, tracks call count, and answers
// GetParameter from a name→(output,error) map so a test can simulate a
// missing/misreadable external-id parameter distinctly per link.
type countingSSMReadStub struct {
	calls   int
	outputs map[string]*ssm.GetParameterOutput
	errs    map[string]error
}

func (s *countingSSMReadStub) GetParameter(_ context.Context, params *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	s.calls++
	name := aws.ToString(params.Name)
	if s.errs != nil {
		if err, ok := s.errs[name]; ok {
			return nil, err
		}
	}
	if s.outputs != nil {
		if out, ok := s.outputs[name]; ok {
			return out, nil
		}
	}
	return nil, &ssmtypes.ParameterNotFound{}
}

func (s *countingSSMReadStub) GetParametersByPath(_ context.Context, _ *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	s.calls++
	return &ssm.GetParametersByPathOutput{}, nil
}

func externalIDParam(value string) *ssm.GetParameterOutput {
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(value)}}
}

// countingAssumeRoleStub wraps a LaunchAccountAssumeRoleFunc and counts calls,
// so a dormancy test can assert the seam was never invoked.
type countingAssumeRoleStub struct {
	calls int
	fn    func(ctx context.Context, roleARN, externalID, region string) (aws.Config, error)
}

func (s *countingAssumeRoleStub) call(ctx context.Context, roleARN, externalID, region string) (aws.Config, error) {
	s.calls++
	return s.fn(ctx, roleARN, externalID, region)
}

// trackedCredentialsProvider returns a CredentialsProvider whose Retrieve
// increments *retrieveCalls every time it's invoked — this is how
// TestDoctorLaunchAccountAssumable_ForcesCredentialResolution proves the
// check does a real read, not just a config construction.
func trackedCredentialsProvider(retrieveCalls *int, retrieveErr error) aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
		*retrieveCalls++
		if retrieveErr != nil {
			return aws.Credentials{}, retrieveErr
		}
		return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}, nil
	})
}

// countingArtifactsPolicyStub implements ArtifactsPolicyS3API. PutBucketPolicy
// and DeleteBucketPolicy panic — checkLaunchAccountArtifactsGrant must never
// call either (T-126-49); a panic makes an accidental write/delete an
// immediate, loud test failure rather than a silently-passing false positive.
type countingArtifactsPolicyStub struct {
	getCalls int
	policy   string
	notFound bool
	err      error
}

func (s *countingArtifactsPolicyStub) GetBucketPolicy(_ context.Context, _ *s3.GetBucketPolicyInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	s.getCalls++
	if s.err != nil {
		return nil, s.err
	}
	if s.notFound {
		return nil, &smithyAPIErrorStub{code: "NoSuchBucketPolicy"}
	}
	return &s3.GetBucketPolicyOutput{Policy: aws.String(s.policy)}, nil
}

func (s *countingArtifactsPolicyStub) PutBucketPolicy(_ context.Context, _ *s3.PutBucketPolicyInput, _ ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	panic("checkLaunchAccountArtifactsGrant must never write the artifacts bucket policy")
}

func (s *countingArtifactsPolicyStub) DeleteBucketPolicy(_ context.Context, _ *s3.DeleteBucketPolicyInput, _ ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error) {
	panic("checkLaunchAccountArtifactsGrant must never delete the artifacts bucket policy")
}

// smithyAPIErrorStub is a minimal smithy.APIError implementation so
// isS3NoSuchBucketPolicy (account_register.go) recognizes the "no policy
// attached" case via errors.As.
type smithyAPIErrorStub struct{ code string }

func (e *smithyAPIErrorStub) Error() string                 { return e.code }
func (e *smithyAPIErrorStub) ErrorCode() string             { return e.code }
func (e *smithyAPIErrorStub) ErrorMessage() string          { return e.code }
func (e *smithyAPIErrorStub) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

// recordingEC2Stub implements EC2InstanceAPI, records the last
// DescribeInstancesInput it received (Test 7), and counts calls (for
// dormancy assertions).
type recordingEC2Stub struct {
	calls     int
	lastInput *ec2.DescribeInstancesInput
	instances []ec2types.Instance
	err       error
}

func (s *recordingEC2Stub) DescribeInstances(_ context.Context, params *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	s.calls++
	s.lastInput = params
	if s.err != nil {
		return nil, s.err
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: s.instances}}}, nil
}

// =============================================================================
// checkLaunchAccountLinks
// =============================================================================

func TestDoctorLaunchAccountLinks_NoLinks_Skipped(t *testing.T) {
	r := checkLaunchAccountLinks(nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped, got %s: %s", r.Status, r.Message)
	}
}

func TestDoctorLaunchAccountLinks_WellFormed_OK(t *testing.T) {
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": wellFormedLaunchLink()}
	r := checkLaunchAccountLinks(links)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", r.Status, r.Message)
	}
}

func TestDoctorLaunchAccountLinks_MissingFields_Warn(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(l *appcfg.LaunchAccountConfig)
		want   string
	}{
		{"launcher ARN", func(l *appcfg.LaunchAccountConfig) { l.LauncherRoleARN = "" }, "missing launcher_role_arn"},
		{"region", func(l *appcfg.LaunchAccountConfig) { l.Region = "" }, "missing region"},
		{"subnet list", func(l *appcfg.LaunchAccountConfig) { l.SubnetIDs = nil; l.AvailabilityZones = nil }, "missing subnet_ids"},
		{"external id param path", func(l *appcfg.LaunchAccountConfig) { l.ExternalIDSSM = "" }, "missing external_id_ssm"},
		{"box role ARN", func(l *appcfg.LaunchAccountConfig) { l.BoxRoleARN = "" }, "missing box_role_arn"},
		{"results bucket", func(l *appcfg.LaunchAccountConfig) { l.ResultsBucket = "" }, "missing results_bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := wellFormedLaunchLink()
			tc.mutate(&link)
			r := checkLaunchAccountLinks(map[string]appcfg.LaunchAccountConfig{"gpu-east": link})
			if r.Status != CheckWarn {
				t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
			}
			if !strings.Contains(r.Message, "gpu-east") {
				t.Errorf("expected message to name the link %q, got: %s", "gpu-east", r.Message)
			}
			if !strings.Contains(r.Message, tc.want) {
				t.Errorf("expected message to contain %q, got: %s", tc.want, r.Message)
			}
		})
	}
}

func TestDoctorLaunchAccountLinks_SubnetAZLengthMismatch_Warn(t *testing.T) {
	link := wellFormedLaunchLink()
	link.AvailabilityZones = []string{"us-east-2a"} // 2 subnets, 1 AZ
	r := checkLaunchAccountLinks(map[string]appcfg.LaunchAccountConfig{"gpu-east": link})
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "length mismatch") {
		t.Errorf("expected message to mention length mismatch, got: %s", r.Message)
	}
}

func TestDoctorLaunchAccountLinks_SingleSubnet_Warn(t *testing.T) {
	link := wellFormedLaunchLink()
	link.SubnetIDs = []string{"subnet-aaa"}
	link.AvailabilityZones = []string{"us-east-2a"}
	r := checkLaunchAccountLinks(map[string]appcfg.LaunchAccountConfig{"gpu-east": link})
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "single attempt") {
		t.Errorf("expected message to explain the AZ-failover sweep collapses to a single attempt, got: %s", r.Message)
	}
}

// =============================================================================
// checkLaunchAccountAssumable
// =============================================================================

func TestDoctorLaunchAccountAssumable_NoLinks_SkippedNoAWSCalls(t *testing.T) {
	ssmStub := &countingSSMReadStub{}
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		return aws.Config{}, nil
	}}
	r := checkLaunchAccountAssumable(context.Background(), nil, ssmStub, assumeStub.call)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped, got %s: %s", r.Status, r.Message)
	}
	if ssmStub.calls != 0 {
		t.Errorf("expected zero SSM calls, got %d", ssmStub.calls)
	}
	if assumeStub.calls != 0 {
		t.Errorf("expected zero assume-role calls, got %d", assumeStub.calls)
	}
}

func TestDoctorLaunchAccountAssumable_Success_OKNamesLinkAndAccount(t *testing.T) {
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{outputs: map[string]*ssm.GetParameterOutput{
		link.ExternalIDSSM: externalIDParam("ext-id-123"),
	}}
	assumeStub := &countingAssumeRoleStub{fn: func(_ context.Context, roleARN, externalID, region string) (aws.Config, error) {
		return aws.Config{Credentials: trackedCredentialsProvider(new(int), nil)}, nil
	}}
	r := checkLaunchAccountAssumable(context.Background(), links, ssmStub, assumeStub.call)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "gpu-east") || !strings.Contains(r.Message, link.AccountID) {
		t.Errorf("expected message to name the link and resolved account, got: %s", r.Message)
	}
}

func TestDoctorLaunchAccountAssumable_ForcesCredentialResolution(t *testing.T) {
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{outputs: map[string]*ssm.GetParameterOutput{
		link.ExternalIDSSM: externalIDParam("ext-id-123"),
	}}
	retrieveCalls := 0
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		// Constructing the config alone must not be treated as success — only
		// an actual Retrieve() call (forced by the check) proves the target
		// account was contacted.
		return aws.Config{Credentials: trackedCredentialsProvider(&retrieveCalls, nil)}, nil
	}}
	r := checkLaunchAccountAssumable(context.Background(), links, ssmStub, assumeStub.call)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", r.Status, r.Message)
	}
	if retrieveCalls == 0 {
		t.Fatal("expected checkLaunchAccountAssumable to call Credentials.Retrieve(ctx) at least once — an unresolved lazy provider must not report success")
	}
}

func TestDoctorLaunchAccountAssumable_FailedAssume_NeverOK(t *testing.T) {
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{outputs: map[string]*ssm.GetParameterOutput{
		link.ExternalIDSSM: externalIDParam("ext-id-123"),
	}}
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		return aws.Config{Credentials: trackedCredentialsProvider(new(int), errors.New("AccessDenied: not authorized to assume role"))}, nil
	}}
	r := checkLaunchAccountAssumable(context.Background(), links, ssmStub, assumeStub.call)
	if r.Status == CheckOK {
		t.Fatalf("a failed assume must never report CheckOK, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "gpu-east") {
		t.Errorf("expected message to name the link, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, link.LauncherRoleARN) {
		t.Errorf("expected message to name the launcher ARN, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "km account register") {
		t.Errorf("expected message to name the enrollment repair command, got: %s", r.Message)
	}
}

func TestDoctorLaunchAccountAssumable_FailedExternalIDRead_DistinctFromFailedAssume(t *testing.T) {
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{errs: map[string]error{
		link.ExternalIDSSM: errors.New("AccessDeniedException: no permission to read parameter"),
	}}
	assumeCalls := 0
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		assumeCalls++
		return aws.Config{Credentials: trackedCredentialsProvider(new(int), nil)}, nil
	}}
	r := checkLaunchAccountAssumable(context.Background(), links, ssmStub, assumeStub.call)
	if r.Status == CheckOK {
		t.Fatalf("a failed external-id read must never report CheckOK, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "external-id read failed") {
		t.Errorf("expected message to distinguish a parameter-read failure from an assume failure, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "assume failed") {
		t.Errorf("a pure parameter-read failure must not also be reported as an assume failure, got: %s", r.Message)
	}
	if assumeCalls != 0 {
		t.Errorf("expected assume-role to never be attempted after a failed external-id read, got %d call(s)", assumeCalls)
	}
}

// =============================================================================
// checkLaunchAccountArtifactsGrant
// =============================================================================

func TestDoctorLaunchAccountArtifactsGrant_NoLinks_SkippedNoAWSCalls(t *testing.T) {
	s3Stub := &countingArtifactsPolicyStub{}
	r := checkLaunchAccountArtifactsGrant(context.Background(), nil, s3Stub, "km-artifacts", "km")
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped, got %s: %s", r.Status, r.Message)
	}
	if s3Stub.getCalls != 0 {
		t.Errorf("expected zero S3 calls, got %d", s3Stub.getCalls)
	}
}

func grantPolicyJSON(sid, boxRoleARN, bucket string, actions []string) string {
	quoted := make([]string, len(actions))
	for i, a := range actions {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":%q,"Effect":"Allow","Principal":{"AWS":%q},"Action":[%s],"Resource":["arn:aws:s3:::%s","arn:aws:s3:::%s/*"]}]}`,
		sid, boxRoleARN, strings.Join(quoted, ","), bucket, bucket)
}

func TestDoctorLaunchAccountArtifactsGrant_Present_OK(t *testing.T) {
	link := wellFormedLaunchLink()
	sid := artifactsGrantSid("km", "gpu-east")
	s3Stub := &countingArtifactsPolicyStub{
		policy: grantPolicyJSON(sid, link.BoxRoleARN, "km-artifacts", []string{"s3:GetObject", "s3:ListBucket"}),
	}
	r := checkLaunchAccountArtifactsGrant(context.Background(), map[string]appcfg.LaunchAccountConfig{"gpu-east": link}, s3Stub, "km-artifacts", "km")
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", r.Status, r.Message)
	}
}

func TestDoctorLaunchAccountArtifactsGrant_Missing_Warn(t *testing.T) {
	link := wellFormedLaunchLink()
	s3Stub := &countingArtifactsPolicyStub{notFound: true}
	r := checkLaunchAccountArtifactsGrant(context.Background(), map[string]appcfg.LaunchAccountConfig{"gpu-east": link}, s3Stub, "km-artifacts", "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "gpu-east") {
		t.Errorf("expected message to name the link, got: %s", r.Message)
	}
	if !strings.Contains(r.Message+r.Remediation, "km account register") {
		t.Errorf("expected message or remediation to name the register command, got message=%s remediation=%s", r.Message, r.Remediation)
	}
}

func TestDoctorLaunchAccountArtifactsGrant_WriteAction_WarnNotOK(t *testing.T) {
	link := wellFormedLaunchLink()
	sid := artifactsGrantSid("km", "gpu-east")
	s3Stub := &countingArtifactsPolicyStub{
		policy: grantPolicyJSON(sid, link.BoxRoleARN, "km-artifacts", []string{"s3:GetObject", "s3:ListBucket", "s3:PutObject"}),
	}
	r := checkLaunchAccountArtifactsGrant(context.Background(), map[string]appcfg.LaunchAccountConfig{"gpu-east": link}, s3Stub, "km-artifacts", "km")
	if r.Status != CheckWarn {
		t.Fatalf("a write-shaped action in the grant must never report CheckOK, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "write-shaped action") {
		t.Errorf("expected message to call out the write-shaped action, got: %s", r.Message)
	}
}

// TestDoctorLaunchAccountArtifactsGrant_UsesSharedSidHelper is a compile-time
// / call-time proof that the grant check derives its Sid via the same
// artifactsGrantSid helper km account register/rm use, not an inline
// literal — a duplicated derivation could silently drift and report a
// present grant as missing.
func TestDoctorLaunchAccountArtifactsGrant_UsesSharedSidHelper(t *testing.T) {
	link := wellFormedLaunchLink()
	sid := artifactsGrantSid("rg", "gpu-east") // deliberately a non-"km" prefix
	s3Stub := &countingArtifactsPolicyStub{
		policy: grantPolicyJSON(sid, link.BoxRoleARN, "rg-artifacts", []string{"s3:GetObject", "s3:ListBucket"}),
	}
	r := checkLaunchAccountArtifactsGrant(context.Background(), map[string]appcfg.LaunchAccountConfig{"gpu-east": link}, s3Stub, "rg-artifacts", "rg")
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK when the grant Sid matches artifactsGrantSid(prefix, name), got %s: %s", r.Status, r.Message)
	}
}

// =============================================================================
// checkLaunchAccountOrphanInstances
// =============================================================================

func TestDoctorLaunchAccountOrphanInstances_NoLinks_SkippedNoAWSCalls(t *testing.T) {
	ssmStub := &countingSSMReadStub{}
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		return aws.Config{}, nil
	}}
	ec2Stub := &recordingEC2Stub{}
	lister := &mockSandboxLister{}
	r := checkLaunchAccountOrphanInstances(context.Background(), nil, ssmStub, assumeStub.call,
		func(aws.Config) EC2InstanceAPI { return ec2Stub }, lister)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped, got %s: %s", r.Status, r.Message)
	}
	if ssmStub.calls != 0 || assumeStub.calls != 0 || ec2Stub.calls != 0 {
		t.Errorf("expected zero AWS calls, got ssm=%d assume=%d ec2=%d", ssmStub.calls, assumeStub.calls, ec2Stub.calls)
	}
}

// launchAccountOrphanFixture wires a single well-formed link through to a
// working EC2 stub, so each behavior test only needs to override one piece.
func launchAccountOrphanFixture(t *testing.T, ec2Stub *recordingEC2Stub, known []kmaws.SandboxRecord) CheckResult {
	t.Helper()
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{outputs: map[string]*ssm.GetParameterOutput{
		link.ExternalIDSSM: externalIDParam("ext-id-123"),
	}}
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		return aws.Config{Credentials: trackedCredentialsProvider(new(int), nil)}, nil
	}}
	lister := &mockSandboxLister{records: known}
	return checkLaunchAccountOrphanInstances(context.Background(), links, ssmStub, assumeStub.call,
		func(aws.Config) EC2InstanceAPI { return ec2Stub }, lister)
}

func TestDoctorLaunchAccountOrphanInstances_UnmatchedInstance_Warn(t *testing.T) {
	ec2Stub := &recordingEC2Stub{instances: []ec2types.Instance{
		makeEC2Instance("i-orphan1", map[string]string{"km:sandbox-id": "km-orphan1"}),
	}}
	r := launchAccountOrphanFixture(t, ec2Stub, nil) // home inventory knows nothing
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "i-orphan1") && !containsInDetails(r.Details, "i-orphan1") {
		t.Errorf("expected the orphan instance id to appear in message or details, got message=%s details=%v", r.Message, r.Details)
	}
	if !containsInDetails(r.Details, "km-orphan1") && !strings.Contains(r.Message, "km-orphan1") {
		t.Errorf("expected the orphan sandbox id to appear in message or details, got message=%s details=%v", r.Message, r.Details)
	}
	if !containsInDetails(r.Details, "gpu-east") && !strings.Contains(r.Message, "gpu-east") {
		t.Errorf("expected the link name to appear in message or details, got message=%s details=%v", r.Message, r.Details)
	}
}

func TestDoctorLaunchAccountOrphanInstances_MatchedInstance_OK(t *testing.T) {
	ec2Stub := &recordingEC2Stub{instances: []ec2types.Instance{
		makeEC2Instance("i-known1", map[string]string{"km:sandbox-id": "km-known1"}),
	}}
	r := launchAccountOrphanFixture(t, ec2Stub, []kmaws.SandboxRecord{{SandboxID: "km-known1", Status: "running"}})
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK for an instance with a matching home record, got %s: %s", r.Status, r.Message)
	}
}

func TestDoctorLaunchAccountOrphanInstances_FiltersOnFixedTagKey(t *testing.T) {
	ec2Stub := &recordingEC2Stub{}
	launchAccountOrphanFixture(t, ec2Stub, nil)
	if ec2Stub.lastInput == nil {
		t.Fatal("expected DescribeInstances to have been called")
	}
	found := false
	for _, f := range ec2Stub.lastInput.Filters {
		if aws.ToString(f.Name) == "tag:km:sandbox-id" {
			found = true
		}
		// The filter must never be prefix-derived (e.g. "tag:rg:sandbox-id") —
		// km tags every sandbox resource under the fixed "km:" namespace
		// regardless of resource_prefix.
		if strings.HasPrefix(aws.ToString(f.Name), "tag:") && aws.ToString(f.Name) != "tag:km:sandbox-id" && aws.ToString(f.Name) != "instance-state-name" {
			t.Errorf("unexpected tag filter %q — the sandbox-id tag filter must be the literal \"tag:km:sandbox-id\"", aws.ToString(f.Name))
		}
	}
	if !found {
		t.Error(`expected a filter on the exact literal "tag:km:sandbox-id"`)
	}
}

func TestDoctorLaunchAccountOrphanInstances_UnreachableLink_WarnNotCleanResult(t *testing.T) {
	link := wellFormedLaunchLink()
	links := map[string]appcfg.LaunchAccountConfig{"gpu-east": link}
	ssmStub := &countingSSMReadStub{errs: map[string]error{
		link.ExternalIDSSM: errors.New("AccessDeniedException"),
	}}
	assumeStub := &countingAssumeRoleStub{fn: func(context.Context, string, string, string) (aws.Config, error) {
		return aws.Config{Credentials: trackedCredentialsProvider(new(int), nil)}, nil
	}}
	lister := &mockSandboxLister{}
	r := checkLaunchAccountOrphanInstances(context.Background(), links, ssmStub, assumeStub.call,
		func(aws.Config) EC2InstanceAPI { return &recordingEC2Stub{} }, lister)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn when a link cannot be reached, got %s: %s", r.Status, r.Message)
	}
	if strings.Contains(strings.ToLower(r.Message), "no orphaned instances found") {
		t.Errorf("an unreachable link must never be reported as a clean result, got: %s", r.Message)
	}
	if !strings.Contains(strings.ToLower(r.Message), "could not check") && !strings.Contains(strings.ToLower(r.Message), "could not be checked") {
		t.Errorf("expected the message to say the link could not be checked, got: %s", r.Message)
	}
}

func containsInDetails(details []string, substr string) bool {
	for _, d := range details {
		if strings.Contains(d, substr) {
			return true
		}
	}
	return false
}

// =============================================================================
// Compile-time interface satisfaction
// =============================================================================

var (
	_ SSMReadAPI                  = (*countingSSMReadStub)(nil)
	_ ArtifactsPolicyS3API        = (*countingArtifactsPolicyStub)(nil)
	_ EC2InstanceAPI              = (*recordingEC2Stub)(nil)
	_ LaunchAccountAssumeRoleFunc = (*countingAssumeRoleStub)(nil).call
	_ SandboxLister               = (*mockSandboxLister)(nil)
)

// =============================================================================
// End-to-end: km doctor with no links configured — all four checks SKIPPED
// =============================================================================

// TestDoctorLaunchAccountChecks_EndToEnd_NoLinksAllSkipped exercises the real
// buildChecks registration path (not the check functions directly) against a
// link-free configuration, proving the four checks are actually wired into
// the doctor pipeline and every one reports SKIPPED — the plan's acceptance
// criterion that `km doctor` runs end to end on a link-free config with the
// four new checks skipped.
func TestDoctorLaunchAccountChecks_EndToEnd_NoLinksAllSkipped(t *testing.T) {
	cfg := minimalConfig() // GetLaunchAccounts() returns nil — dormant
	deps := &DoctorDeps{}  // no launch-account clients wired at all
	checks := buildChecks(cfg, deps)
	results := runChecks(context.Background(), checks)

	want := []string{
		"Launch Account Links",
		"Launch Account Assumable",
		"Launch Account Artifacts Grant",
		"Launch Account Orphan Instances",
	}
	found := make(map[string]CheckResult, len(want))
	for _, r := range results {
		for _, w := range want {
			if r.Name == w {
				found[w] = r
			}
		}
	}
	for _, w := range want {
		r, ok := found[w]
		if !ok {
			t.Errorf("expected buildChecks to register a check named %q", w)
			continue
		}
		if r.Status != CheckSkipped {
			t.Errorf("expected %q to be SKIPPED on a link-free config, got %s: %s", w, r.Status, r.Message)
		}
		t.Logf("%s: %s — %s", r.Status, r.Name, r.Message)
	}
}
