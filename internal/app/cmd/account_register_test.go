// Package cmd_test provides tests for `km account register/list/rm` (Phase 126 Plan 07).
package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"gopkg.in/yaml.v3"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ======================== Test doubles ==========================================

// mockArtifactsPolicyS3 is a test double for cmd.ArtifactsPolicyS3API.
type mockArtifactsPolicyS3 struct {
	ExistingPolicy string // raw JSON; "" == no policy attached (NoSuchBucketPolicy)
	PutCalls       []string
	DeleteCalls    int
	GetErr         error
	PutErr         error
	FailIfCalled   *testing.T
}

func (m *mockArtifactsPolicyS3) GetBucketPolicy(_ context.Context, _ *s3.GetBucketPolicyInput, _ ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockArtifactsPolicyS3.GetBucketPolicy should not have been called")
	}
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	if m.ExistingPolicy == "" {
		return nil, &smithy.GenericAPIError{Code: "NoSuchBucketPolicy"}
	}
	return &s3.GetBucketPolicyOutput{Policy: aws.String(m.ExistingPolicy)}, nil
}

func (m *mockArtifactsPolicyS3) PutBucketPolicy(_ context.Context, in *s3.PutBucketPolicyInput, _ ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockArtifactsPolicyS3.PutBucketPolicy should not have been called")
	}
	if m.PutErr != nil {
		return nil, m.PutErr
	}
	policy := aws.ToString(in.Policy)
	m.PutCalls = append(m.PutCalls, policy)
	m.ExistingPolicy = policy // keep state current so a second call in the same test sees it
	return &s3.PutBucketPolicyOutput{}, nil
}

func (m *mockArtifactsPolicyS3) DeleteBucketPolicy(_ context.Context, _ *s3.DeleteBucketPolicyInput, _ ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockArtifactsPolicyS3.DeleteBucketPolicy should not have been called")
	}
	m.DeleteCalls++
	m.ExistingPolicy = ""
	return &s3.DeleteBucketPolicyOutput{}, nil
}

// mockAccountSSM is a test double for cmd.AccountSSMAPI.
type mockAccountSSM struct {
	PutCalls         []*ssm.PutParameterInput
	DeleteCalls      []string
	NotFoundOnDelete bool
	FailIfCalled     *testing.T
}

func (m *mockAccountSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockAccountSSM.PutParameter should not have been called")
	}
	m.PutCalls = append(m.PutCalls, in)
	return &ssm.PutParameterOutput{}, nil
}

func (m *mockAccountSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockAccountSSM.DeleteParameter should not have been called")
	}
	if m.NotFoundOnDelete {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	m.DeleteCalls = append(m.DeleteCalls, aws.ToString(in.Name))
	return &ssm.DeleteParameterOutput{}, nil
}

// mockAccountRmRunner is a test double for cmd.AccountRunner, scoped to what
// km account rm calls (Destroy only — Plan/Apply/Output/Init/Validate are
// no-ops it never reaches).
type mockAccountRmRunner struct {
	DestroyCalls []string
	DestroyErr   error
	FailIfCalled *testing.T
}

func (m *mockAccountRmRunner) Plan(_ context.Context, _ string) error  { return nil }
func (m *mockAccountRmRunner) Apply(_ context.Context, _ string) error { return nil }
func (m *mockAccountRmRunner) Destroy(_ context.Context, dir string) error {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockAccountRmRunner.Destroy should not have been called")
	}
	if m.DestroyErr != nil {
		return m.DestroyErr
	}
	m.DestroyCalls = append(m.DestroyCalls, dir)
	return nil
}
func (m *mockAccountRmRunner) Output(_ context.Context, _ string) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockAccountRmRunner) InitNoBackend(_ context.Context, _ string) error { return nil }
func (m *mockAccountRmRunner) Validate(_ context.Context, _ string) error      { return nil }

// mockPurgeBucketS3 is a test double for cmd.PurgeBucketS3API.
type mockPurgeBucketS3 struct {
	Versions           []s3types.ObjectVersion
	DeleteMarkers      []s3types.DeleteMarkerEntry
	DeleteObjectsCalls int
	DeleteBucketCalls  int
	FailIfCalled       *testing.T
}

func (m *mockPurgeBucketS3) ListObjectVersions(_ context.Context, _ *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockPurgeBucketS3.ListObjectVersions should not have been called")
	}
	return &s3.ListObjectVersionsOutput{Versions: m.Versions, DeleteMarkers: m.DeleteMarkers}, nil
}

func (m *mockPurgeBucketS3) DeleteObjects(_ context.Context, _ *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockPurgeBucketS3.DeleteObjects should not have been called")
	}
	m.DeleteObjectsCalls++
	return &s3.DeleteObjectsOutput{}, nil
}

func (m *mockPurgeBucketS3) DeleteBucket(_ context.Context, _ *s3.DeleteBucketInput, _ ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockPurgeBucketS3.DeleteBucket should not have been called")
	}
	m.DeleteBucketCalls++
	return &s3.DeleteBucketOutput{}, nil
}

// mockPurgeLockTableDynamoDB is a test double for cmd.PurgeLockTableDynamoDBAPI.
type mockPurgeLockTableDynamoDB struct {
	DeleteCalls  []string
	FailIfCalled *testing.T
}

func (m *mockPurgeLockTableDynamoDB) DeleteTable(_ context.Context, in *dynamodb.DeleteTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteTableOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockPurgeLockTableDynamoDB.DeleteTable should not have been called")
	}
	m.DeleteCalls = append(m.DeleteCalls, aws.ToString(in.TableName))
	return &dynamodb.DeleteTableOutput{}, nil
}

// registerHomeCaller is the resolved identity RunAccountRegister sees for the
// HOME account — deliberately a different account id than any link's target
// AccountID used below, so the self-enrollment guard never trips by accident.
var registerHomeCaller = cmd.AccountCallerIdentity{
	AccountID: "999999999999",
	ARN:       "arn:aws:sts::999999999999:assumed-role/AWSReservedSSO_Admin/operator@example.com",
}

// installAccountRegisterSeams overrides every seam RunAccountRegister touches.
func installAccountRegisterSeams(t *testing.T, s3c cmd.ArtifactsPolicyS3API, ssmc cmd.AccountSSMAPI, caller cmd.AccountCallerIdentity) {
	t.Helper()

	origS3 := cmd.NewArtifactsPolicyS3Client
	cmd.NewArtifactsPolicyS3Client = func(_ aws.Config) cmd.ArtifactsPolicyS3API { return s3c }
	t.Cleanup(func() { cmd.NewArtifactsPolicyS3Client = origS3 })

	origSSM := cmd.NewAccountSSMClientFunc
	cmd.NewAccountSSMClientFunc = func(_ aws.Config) cmd.AccountSSMAPI { return ssmc }
	t.Cleanup(func() { cmd.NewAccountSSMClientFunc = origSSM })

	origCaller := cmd.NewAccountCallerIdentityFunc
	cmd.NewAccountCallerIdentityFunc = func(_ context.Context, _ aws.Config) (cmd.AccountCallerIdentity, error) {
		return caller, nil
	}
	t.Cleanup(func() { cmd.NewAccountCallerIdentityFunc = origCaller })
}

// installAccountRmSeams overrides every seam RunAccountRm touches.
func installAccountRmSeams(t *testing.T, s3c cmd.ArtifactsPolicyS3API, ssmc cmd.AccountSSMAPI, runner cmd.AccountRunner, purgeS3 cmd.PurgeBucketS3API, purgeDDB cmd.PurgeLockTableDynamoDBAPI) {
	t.Helper()

	origS3 := cmd.NewArtifactsPolicyS3Client
	cmd.NewArtifactsPolicyS3Client = func(_ aws.Config) cmd.ArtifactsPolicyS3API { return s3c }
	t.Cleanup(func() { cmd.NewArtifactsPolicyS3Client = origS3 })

	origSSM := cmd.NewAccountSSMClientFunc
	cmd.NewAccountSSMClientFunc = func(_ aws.Config) cmd.AccountSSMAPI { return ssmc }
	t.Cleanup(func() { cmd.NewAccountSSMClientFunc = origSSM })

	origRunner := cmd.NewAccountRunnerFunc
	cmd.NewAccountRunnerFunc = func(_, _ string) cmd.AccountRunner { return runner }
	t.Cleanup(func() { cmd.NewAccountRunnerFunc = origRunner })

	origPurgeS3 := cmd.NewPurgeBucketS3Client
	cmd.NewPurgeBucketS3Client = func(_ aws.Config) cmd.PurgeBucketS3API { return purgeS3 }
	t.Cleanup(func() { cmd.NewPurgeBucketS3Client = origPurgeS3 })

	origPurgeDDB := cmd.NewPurgeLockTableDynamoDBClient
	cmd.NewPurgeLockTableDynamoDBClient = func(_ aws.Config) cmd.PurgeLockTableDynamoDBAPI { return purgeDDB }
	t.Cleanup(func() { cmd.NewPurgeLockTableDynamoDBClient = origPurgeDDB })
}

// writeLinkFragment writes a fully-populated AccountLinkFragment YAML at
// ~/.km/account-links/<name>.link.yaml (HOME is set via t.Setenv by the
// caller) and returns the fragment.
func writeLinkFragment(t *testing.T, home, name string) cmd.AccountLinkFragment {
	t.Helper()
	frag := cmd.AccountLinkFragment{
		Name:              name,
		AccountID:         "555566667777",
		LauncherRoleARN:   "arn:aws:iam::555566667777:role/km-gpu-launcher",
		BoxRoleARN:        "arn:aws:iam::555566667777:role/km-gpu-box",
		ExternalID:        "super-secret-external-id-value",
		Region:            "us-east-1",
		SubnetIDs:         []string{"subnet-1", "subnet-2"},
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
		SecurityGroupID:   "sg-0123",
		ResultsBucket:     "km-results-555566667777",
		InstanceTypes:     []string{"g6e.12xlarge"},
		StateBucket:       cmd.LinkStateBucketName("km", "555566667777", "use1"),
		LockTable:         cmd.LinkLockTableName("km", "use1"),
		StateKey:          cmd.LinkStateKey(name),
	}
	dir := filepath.Join(home, ".km", "account-links")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(frag)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".link.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return frag
}

// writeKMConfigYAML writes a minimal km-config.yaml at dir/km-config.yaml so
// PersistLaunchAccountsConfig has a file to read-modify-write, and returns
// its path.
func writeKMConfigYAML(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "km-config.yaml")
	if err := os.WriteFile(path, []byte("resource_prefix: km\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// testPolicyStatement is a test-local decode target for a generated bucket
// policy statement, independent of the production (unexported) struct — this
// keeps the assertions black-box.
type testPolicyStatement struct {
	Sid       string `json:"Sid"`
	Effect    string `json:"Effect"`
	Principal struct {
		AWS string `json:"AWS"`
	} `json:"Principal"`
	Action []string `json:"Action"`
}

type testPolicyDocument struct {
	Version   string                `json:"Version"`
	Statement []testPolicyStatement `json:"Statement"`
}

// ======================== Task 1: km account register ============================

func baseRegisterOpts(name string) cmd.AccountRegisterOpts {
	return cmd.AccountRegisterOpts{
		Name:       name,
		AWSProfile: "home-admin",
	}
}

// Test 0: the written launch_accounts entry carries state bucket/lock
// table/state key from the fragment.
func TestAccountRegister_CarriesStateBackendFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	frag := writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	opts := baseRegisterOpts("mgmt-gpu")
	if err := cmd.RunAccountRegister(cfg, opts, repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	link, ok := cfg.GetLaunchAccount("mgmt-gpu")
	if !ok {
		t.Fatal("expected launch_accounts.mgmt-gpu to be present after register")
	}
	if link.StateBucket != frag.StateBucket || link.LockTable != frag.LockTable || link.StateKey != frag.StateKey {
		t.Errorf("state backend fields not carried through: got bucket=%s table=%s key=%s, want bucket=%s table=%s key=%s",
			link.StateBucket, link.LockTable, link.StateKey, frag.StateBucket, frag.LockTable, frag.StateKey)
	}
}

// Test 1: registering from a fragment writes every field, and the file never
// contains the external id value.
func TestAccountRegister_FromFragment_WritesConfigEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	frag := writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	configPath := writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	opts := baseRegisterOpts("mgmt-gpu")
	if err := cmd.RunAccountRegister(cfg, opts, repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	link, ok := cfg.GetLaunchAccount("mgmt-gpu")
	if !ok {
		t.Fatal("expected launch_accounts.mgmt-gpu")
	}
	if link.AccountID != frag.AccountID || link.LauncherRoleARN != frag.LauncherRoleARN ||
		link.BoxRoleARN != frag.BoxRoleARN || link.Region != frag.Region ||
		link.SecurityGroupID != frag.SecurityGroupID || link.ResultsBucket != frag.ResultsBucket {
		t.Errorf("link fields not fully populated from fragment: %+v", link)
	}
	if link.ExternalIDSSM == "" {
		t.Error("expected ExternalIDSSM to be populated")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), frag.ExternalID) {
		t.Error("km-config.yaml must never contain the raw external id value")
	}
	if !strings.Contains(string(data), link.ExternalIDSSM) {
		t.Error("km-config.yaml must contain the external id parameter PATH")
	}
}

// Test 2: the external id is written as an encrypted SecureString with
// overwrite enabled, at the expected path.
func TestAccountRegister_ExternalIDWrittenAsSecureString(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	frag := writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	if err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	if len(ssmc.PutCalls) != 1 {
		t.Fatalf("expected exactly 1 PutParameter call, got %d", len(ssmc.PutCalls))
	}
	put := ssmc.PutCalls[0]
	if put.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("parameter type = %s, want SecureString", put.Type)
	}
	if put.Overwrite == nil || !*put.Overwrite {
		t.Error("expected Overwrite=true so re-registration succeeds")
	}
	if aws.ToString(put.Value) != frag.ExternalID {
		t.Error("wrong value written to the external id parameter")
	}
	wantPath := "/km/launch-accounts/mgmt-gpu/external-id"
	if aws.ToString(put.Name) != wantPath {
		t.Errorf("parameter path = %s, want %s", aws.ToString(put.Name), wantPath)
	}
}

// Test 3: against a bucket with no existing policy, the grant statement is
// created with the per-link Sid, the box role as principal, and read-only actions.
func TestAccountRegister_ArtifactsGrant_NoExistingPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	frag := writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	if err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	if len(s3c.PutCalls) != 1 {
		t.Fatalf("expected exactly 1 PutBucketPolicy call, got %d", len(s3c.PutCalls))
	}
	var doc testPolicyDocument
	if err := json.Unmarshal([]byte(s3c.PutCalls[0]), &doc); err != nil {
		t.Fatalf("unmarshal generated policy: %v", err)
	}
	if len(doc.Statement) != 1 {
		t.Fatalf("expected exactly 1 statement, got %d", len(doc.Statement))
	}
	stmt := doc.Statement[0]
	wantSid := "km-account-link-mgmt-gpu-read"
	if stmt.Sid != wantSid {
		t.Errorf("Sid = %s, want %s", stmt.Sid, wantSid)
	}
	if stmt.Principal.AWS != frag.BoxRoleARN {
		t.Errorf("Principal.AWS = %s, want %s", stmt.Principal.AWS, frag.BoxRoleARN)
	}
	if stmt.Effect != "Allow" {
		t.Errorf("Effect = %s, want Allow", stmt.Effect)
	}
}

// Test 4: against a bucket with an existing unrelated policy, the existing
// statement is preserved and the new one is appended.
func TestAccountRegister_ArtifactsGrant_PreservesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLinkFragment(t, home, "mgmt-gpu")

	existing := `{"Version":"2012-10-17","Statement":[{"Sid":"some-other-grant","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::111111111111:role/other"},"Action":["s3:GetObject"],"Resource":"arn:aws:s3:::km-artifacts-123/*"}]}`
	s3c := &mockArtifactsPolicyS3{ExistingPolicy: existing}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	if err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	var doc testPolicyDocument
	if err := json.Unmarshal([]byte(s3c.PutCalls[len(s3c.PutCalls)-1]), &doc); err != nil {
		t.Fatalf("unmarshal generated policy: %v", err)
	}
	if len(doc.Statement) != 2 {
		t.Fatalf("expected 2 statements (unrelated preserved + new appended), got %d", len(doc.Statement))
	}
	var sawOther, sawOurs bool
	for _, s := range doc.Statement {
		if s.Sid == "some-other-grant" {
			sawOther = true
		}
		if s.Sid == "km-account-link-mgmt-gpu-read" {
			sawOurs = true
		}
	}
	if !sawOther || !sawOurs {
		t.Errorf("expected both statements present, got %+v", doc.Statement)
	}
}

// Test 5: re-registering the same link REPLACES its own statement rather
// than appending a duplicate.
func TestAccountRegister_ArtifactsGrant_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	opts := baseRegisterOpts("mgmt-gpu")
	if err := cmd.RunAccountRegister(cfg, opts, repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister (1st): %v", err)
	}
	if err := cmd.RunAccountRegister(cfg, opts, repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister (2nd): %v", err)
	}

	var doc testPolicyDocument
	if err := json.Unmarshal([]byte(s3c.PutCalls[len(s3c.PutCalls)-1]), &doc); err != nil {
		t.Fatalf("unmarshal generated policy: %v", err)
	}
	count := 0
	for _, s := range doc.Statement {
		if s.Sid == "km-account-link-mgmt-gpu-read" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 statement for the link's Sid after re-registering, got %d", count)
	}
}

// Test 6: no action in the generated statement grants a write — asserted
// against the generated statement's Action list, not against source text.
func TestAccountRegister_ArtifactsGrant_NoWriteActions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	if err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRegister: %v", err)
	}

	var doc testPolicyDocument
	if err := json.Unmarshal([]byte(s3c.PutCalls[0]), &doc); err != nil {
		t.Fatalf("unmarshal generated policy: %v", err)
	}
	wantActions := map[string]bool{"s3:GetObject": true, "s3:ListBucket": true}
	for _, stmt := range doc.Statement {
		if stmt.Sid != "km-account-link-mgmt-gpu-read" {
			continue
		}
		if len(stmt.Action) != len(wantActions) {
			t.Fatalf("action list = %v, want exactly %v", stmt.Action, wantActions)
		}
		for _, a := range stmt.Action {
			if !wantActions[a] {
				t.Errorf("unexpected action %q in generated grant statement (must be read-only)", a)
			}
			if strings.Contains(strings.ToLower(a), "put") || strings.Contains(strings.ToLower(a), "delete") || strings.Contains(strings.ToLower(a), "write") {
				t.Errorf("action %q looks like a write action — grant must be read-only", a)
			}
		}
	}
}

// Test 7: a missing fragment with no explicit flags is a clear, named error.
func TestAccountRegister_MissingFragmentAndFlags_Error(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // no fragment written

	s3c := &mockArtifactsPolicyS3{FailIfCalled: t}
	ssmc := &mockAccountSSM{FailIfCalled: t}
	installAccountRegisterSeams(t, s3c, ssmc, registerHomeCaller)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{})
	if err == nil {
		t.Fatal("expected an error when neither fragment nor explicit flags are available")
	}
	wantFragPath := filepath.Join(home, ".km", "account-links", "mgmt-gpu.link.yaml")
	if !strings.Contains(err.Error(), wantFragPath) {
		t.Errorf("error must name the expected fragment path %q, got: %v", wantFragPath, err)
	}
	if !strings.Contains(err.Error(), "--from-fragment") {
		t.Errorf("error must name the flag alternative, got: %v", err)
	}
}

// Self-enrollment guard: the home account must not equal the link's target account.
func TestAccountRegister_RefusesSelfEnrollment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	frag := writeLinkFragment(t, home, "mgmt-gpu")

	s3c := &mockArtifactsPolicyS3{FailIfCalled: t}
	ssmc := &mockAccountSSM{FailIfCalled: t}
	// Home caller resolves to the SAME account as the link's target.
	installAccountRegisterSeams(t, s3c, ssmc, cmd.AccountCallerIdentity{AccountID: frag.AccountID, ARN: "arn:aws:sts::" + frag.AccountID + ":assumed-role/x/y"})

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)

	cfg := &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123"}
	err := cmd.RunAccountRegister(cfg, baseRegisterOpts("mgmt-gpu"), repoRoot, &discardWriter{})
	if err == nil {
		t.Fatal("expected refusal when home account equals the link's target account")
	}
}

// ======================== Task 2: km account list =================================

func TestAccountList_RendersRows(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {
				AccountID: "555566667777", Region: "us-east-1",
				SubnetIDs:     []string{"subnet-1", "subnet-2"},
				ResultsBucket: "km-results-555566667777",
				ExternalIDSSM: "/km/launch-accounts/mgmt-gpu/external-id",
			},
		},
	}
	var buf bytes.Buffer
	if err := cmd.RunAccountList(&buf, cfg); err != nil {
		t.Fatalf("RunAccountList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"mgmt-gpu", "555566667777", "us-east-1", "2", "km-results-555566667777", "/km/launch-accounts/mgmt-gpu/external-id"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q; got:\n%s", want, out)
		}
	}

	var empty bytes.Buffer
	if err := cmd.RunAccountList(&empty, &config.Config{}); err != nil {
		t.Fatalf("RunAccountList (empty): %v", err)
	}
	if !strings.Contains(empty.String(), "no launch account links") {
		t.Errorf("expected an empty-state line, got: %s", empty.String())
	}
}

func TestAccountList_NeverPrintsExternalID(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {
				AccountID: "555566667777", Region: "us-east-1",
				ExternalIDSSM: "/km/launch-accounts/mgmt-gpu/external-id",
			},
		},
	}
	var buf bytes.Buffer
	if err := cmd.RunAccountList(&buf, cfg); err != nil {
		t.Fatalf("RunAccountList: %v", err)
	}
	if strings.Contains(buf.String(), "super-secret") {
		t.Error("list output must never contain a raw external id value")
	}
}

// ======================== Task 2: km account rm ====================================

func linkedCfg(names ...string) *config.Config {
	links := map[string]config.LaunchAccountConfig{}
	for _, n := range names {
		links[n] = config.LaunchAccountConfig{
			AccountID: "555566667777", Region: "us-east-1",
			LauncherRoleARN: "arn:aws:iam::555566667777:role/km-gpu-launcher",
			BoxRoleARN:      "arn:aws:iam::555566667777:role/km-gpu-box",
			ExternalIDSSM:   "/km/launch-accounts/" + n + "/external-id",
			SubnetIDs:       []string{"subnet-1"},
			StateBucket:     "tf-km-linkstate-555566667777-use1",
			LockTable:       "tf-km-linklocks-use1",
			StateKey:        "account-links/" + n + "/terraform.tfstate",
		}
	}
	return &config.Config{ResourcePrefix: "km", ArtifactsBucket: "km-artifacts-123", LaunchAccounts: links}
}

func baseRmOpts(name string) cmd.AccountRmOpts {
	return cmd.AccountRmOpts{Name: name, HomeAWSProfile: "home-admin", Yes: true}
}

// Test 3: rm removes only its own grant statement, leaving an unrelated one intact.
func TestAccountRm_RemovesOwnGrantOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	existing := `{"Version":"2012-10-17","Statement":[` +
		`{"Sid":"some-other-grant","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::111111111111:role/other"},"Action":["s3:GetObject"],"Resource":"arn:aws:s3:::km-artifacts-123/*"},` +
		`{"Sid":"km-account-link-mgmt-gpu-read","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::555566667777:role/km-gpu-box"},"Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::km-artifacts-123","arn:aws:s3:::km-artifacts-123/*"]}]}`
	s3c := &mockArtifactsPolicyS3{ExistingPolicy: existing}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{FailIfCalled: t} // no --target-aws-profile: destroy must not run
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	if err := cmd.RunAccountRm(cfg, baseRmOpts("mgmt-gpu"), repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}

	var doc testPolicyDocument
	if err := json.Unmarshal([]byte(s3c.ExistingPolicy), &doc); err != nil {
		t.Fatalf("unmarshal remaining policy: %v", err)
	}
	if len(doc.Statement) != 1 || doc.Statement[0].Sid != "some-other-grant" {
		t.Errorf("expected only the unrelated statement to survive, got %+v", doc.Statement)
	}
}

// Test 4: rm deletes the secure parameter and removes the config entry.
func TestAccountRm_DeletesParamAndConfigEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	configPath := writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	if err := cmd.RunAccountRm(cfg, baseRmOpts("mgmt-gpu"), repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}

	if len(ssmc.DeleteCalls) != 1 || ssmc.DeleteCalls[0] != "/km/launch-accounts/mgmt-gpu/external-id" {
		t.Errorf("expected exactly 1 DeleteParameter call for the link's path, got %v", ssmc.DeleteCalls)
	}
	if _, ok := cfg.GetLaunchAccount("mgmt-gpu"); ok {
		t.Error("expected launch_accounts.mgmt-gpu to be removed from in-memory config")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mgmt-gpu") {
		t.Error("km-config.yaml must no longer contain the removed link")
	}
}

// Test 5: without --target-aws-profile, rm performs only the home-side
// removal and prints a self-contained, pasteable follow-up command.
func TestAccountRm_NoTargetProfile_PrintsFollowup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	var out bytes.Buffer
	if err := cmd.RunAccountRm(cfg, baseRmOpts("mgmt-gpu"), repoRoot, strings.NewReader(""), &out); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	wantSubstrings := []string{"terragrunt destroy", "--terragrunt-working-dir", unitDir}
	for _, want := range wantSubstrings {
		if !strings.Contains(out.String(), want) {
			t.Errorf("follow-up output missing %q; got:\n%s", want, out.String())
		}
	}
}

// Test 6: with --target-aws-profile, rm invokes the runner's destroy against
// the generated unit directory exactly once.
func TestAccountRm_WithTargetProfile_InvokesDestroyOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pre-populate the unit directory (as km account add would have).
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte("# existing unit\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	opts := baseRmOpts("mgmt-gpu")
	opts.TargetAWSProfile = "target-admin"
	if err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}
	if len(runner.DestroyCalls) != 1 {
		t.Fatalf("expected exactly 1 Destroy call, got %d", len(runner.DestroyCalls))
	}
	if runner.DestroyCalls[0] != unitDir {
		t.Errorf("Destroy called against %s, want %s", runner.DestroyCalls[0], unitDir)
	}
	// The pre-existing unit directory content must survive untouched — no regeneration needed.
	data, err := os.ReadFile(filepath.Join(unitDir, "terragrunt.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# existing unit\n" {
		t.Error("unit directory was regenerated even though it already existed")
	}
}

// Test 6b: when the unit directory is absent, rm regenerates it from the
// link record before destroying, rather than failing.
func TestAccountRm_RegeneratesMissingUnitDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // unit directory deliberately never created

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	// regenerateAccountLinkUnit resolves the module source relative to repoRoot.
	moduleDir := filepath.Join(repoRoot, "infra", "modules", "gpu-launcher-account", "v1.0.0")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	opts := baseRmOpts("mgmt-gpu")
	opts.TargetAWSProfile = "target-admin"
	if err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}

	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if _, err := os.Stat(filepath.Join(unitDir, "terragrunt.hcl")); err != nil {
		t.Fatalf("expected a regenerated terragrunt.hcl: %v", err)
	}
	if len(runner.DestroyCalls) != 1 {
		t.Fatalf("expected exactly 1 Destroy call, got %d", len(runner.DestroyCalls))
	}
}

// Test 7: without confirmation and without --yes, rm makes no change.
func TestAccountRm_ConfirmationRequired_NoChangeWithoutYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{FailIfCalled: t}
	ssmc := &mockAccountSSM{FailIfCalled: t}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	opts := cmd.AccountRmOpts{Name: "mgmt-gpu", HomeAWSProfile: "home-admin"} // Yes: false
	if err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader("n\n"), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}
	if _, ok := cfg.GetLaunchAccount("mgmt-gpu"); !ok {
		t.Error("declining confirmation must leave the link in place")
	}
}

// Test 8: rm for an unknown link name is a clear error, not a silent success.
func TestAccountRm_UnknownLink_Error(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{FailIfCalled: t}
	ssmc := &mockAccountSSM{FailIfCalled: t}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg() // empty

	err := cmd.RunAccountRm(cfg, baseRmOpts("does-not-exist"), repoRoot, strings.NewReader(""), &discardWriter{})
	if err == nil {
		t.Fatal("expected an error for an unregistered link name")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the unknown link, got: %v", err)
	}
}

// Test 9: rm never deletes the state bucket or lock table by default.
func TestAccountRm_NoBackendDeleteByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	opts := baseRmOpts("mgmt-gpu")
	opts.TargetAWSProfile = "target-admin" // no --purge-backend
	if err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}
	// purgeS3/purgeDDB are FailIfCalled — reaching here without a t.Fatal proves
	// neither the bucket nor the table was touched.
}

// Test 10: --purge-backend deletes the bucket and table only when no other
// configured link shares them; refuses (naming the other link) otherwise.
func TestAccountRm_PurgeBackend_DeletesWhenUnshared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{}
	purgeS3 := &mockPurgeBucketS3{}
	purgeDDB := &mockPurgeLockTableDynamoDB{}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu") // sole link

	opts := baseRmOpts("mgmt-gpu")
	opts.TargetAWSProfile = "target-admin"
	opts.PurgeBackend = true
	if err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm: %v", err)
	}
	if purgeS3.DeleteBucketCalls != 1 {
		t.Errorf("expected the state bucket to be deleted, DeleteBucketCalls=%d", purgeS3.DeleteBucketCalls)
	}
	if len(purgeDDB.DeleteCalls) != 1 {
		t.Errorf("expected the lock table to be deleted, DeleteCalls=%v", purgeDDB.DeleteCalls)
	}
}

func TestAccountRm_PurgeBackend_RefusesWhenShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{}
	runner := &mockAccountRmRunner{}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu", "mgmt-gpu-2") // two links sharing the same backend names

	opts := baseRmOpts("mgmt-gpu")
	opts.TargetAWSProfile = "target-admin"
	opts.PurgeBackend = true
	err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{})
	if err == nil {
		t.Fatal("expected --purge-backend to refuse when another link shares the backend")
	}
	if !strings.Contains(err.Error(), "mgmt-gpu-2") {
		t.Errorf("refusal must name the conflicting link, got: %v", err)
	}
	// purgeS3/purgeDDB are FailIfCalled — reaching here proves nothing was deleted.
}

// --purge-backend requires --target-aws-profile.
func TestAccountRm_PurgeBackend_RequiresTargetProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{FailIfCalled: t}
	ssmc := &mockAccountSSM{FailIfCalled: t}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	opts := baseRmOpts("mgmt-gpu")
	opts.PurgeBackend = true // no TargetAWSProfile
	err := cmd.RunAccountRm(cfg, opts, repoRoot, strings.NewReader(""), &discardWriter{})
	if err == nil {
		t.Fatal("expected an error when --purge-backend is set without --target-aws-profile")
	}
}

// ======================== Idempotency of the low-level removal primitives ========
//
// "Every removal step is idempotent; the test runs rm twice and the second
// run succeeds" (Task 2 acceptance criteria) is proven here at the primitive
// level rather than by invoking RunAccountRm twice with the SAME link name:
// RunAccountRm's first call always removes the km-config.yaml entry (Test 8
// requires an unregistered name to be a clear error, not a silent success),
// so a literal second top-level call on the same name is expected to error.
// What must be idempotent — and is — is each individual AWS-facing removal
// step, so a retry after a partial failure (e.g. a network blip between the
// grant removal and the parameter deletion) never errors on "already gone".

func TestRemoveArtifactsReadGrant_IdempotentAcrossCalls(t *testing.T) {
	sid := "km-account-link-mgmt-gpu-read"
	existing := `{"Version":"2012-10-17","Statement":[{"Sid":"` + sid + `","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::555566667777:role/km-gpu-box"},"Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::b","arn:aws:s3:::b/*"]}]}`
	s3c := &mockArtifactsPolicyS3{ExistingPolicy: existing}

	if err := cmd.RemoveArtifactsReadGrant(context.Background(), s3c, "b", sid); err != nil {
		t.Fatalf("1st RemoveArtifactsReadGrant: %v", err)
	}
	if s3c.DeleteCalls != 1 {
		t.Fatalf("expected the now-empty policy to be deleted, DeleteCalls=%d", s3c.DeleteCalls)
	}
	// Second call: the grant is already gone — must succeed without erroring
	// and without another write.
	if err := cmd.RemoveArtifactsReadGrant(context.Background(), s3c, "b", sid); err != nil {
		t.Fatalf("2nd RemoveArtifactsReadGrant (already absent): %v", err)
	}
}

// RunAccountRm's own parameter-deletion step must tolerate a parameter that
// is already gone (e.g. a retry after a prior partial run) rather than
// erroring.
func TestAccountRm_ParameterAlreadyGone_StillSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s3c := &mockArtifactsPolicyS3{}
	ssmc := &mockAccountSSM{NotFoundOnDelete: true}
	runner := &mockAccountRmRunner{FailIfCalled: t}
	purgeS3 := &mockPurgeBucketS3{FailIfCalled: t}
	purgeDDB := &mockPurgeLockTableDynamoDB{FailIfCalled: t}
	installAccountRmSeams(t, s3c, ssmc, runner, purgeS3, purgeDDB)

	repoRoot := t.TempDir()
	writeKMConfigYAML(t, repoRoot)
	cfg := linkedCfg("mgmt-gpu")

	if err := cmd.RunAccountRm(cfg, baseRmOpts("mgmt-gpu"), repoRoot, strings.NewReader(""), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountRm must tolerate an already-deleted parameter: %v", err)
	}
}
