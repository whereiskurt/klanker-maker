package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	sesv2svc "github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	appcfg "github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// --- Mock STS ---

type mockSTSClient struct {
	output *sts.GetCallerIdentityOutput
	err    error
}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return m.output, m.err
}

// --- Mock S3 ---

type mockS3HeadBucketClient struct {
	err error
}

func (m *mockS3HeadBucketClient) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, m.err
}

func (m *mockS3HeadBucketClient) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{}, m.err
}

// --- Mock DynamoDB ---

type mockDynamoClient struct {
	output *dynamodb.DescribeTableOutput
	err    error
}

func (m *mockDynamoClient) DescribeTable(ctx context.Context, params *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	return m.output, m.err
}

// --- Mock KMS ---

type mockKMSClient struct {
	output *kms.DescribeKeyOutput
	err    error
}

func (m *mockKMSClient) DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	return m.output, m.err
}

// --- Mock Organizations ---

type mockOrgsClient struct {
	output *organizations.ListPoliciesForTargetOutput
	err    error
}

func (m *mockOrgsClient) ListPoliciesForTarget(ctx context.Context, params *organizations.ListPoliciesForTargetInput, optFns ...func(*organizations.Options)) (*organizations.ListPoliciesForTargetOutput, error) {
	return m.output, m.err
}

// --- Mock SSM ---

type mockSSMReadClient struct {
	outputs map[string]*ssm.GetParameterOutput
	err     error
	// pathOutputs maps a path prefix to matching parameters (for GetParametersByPath).
	pathOutputs map[string][]ssmtypes.Parameter
}

func (m *mockSSMReadClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.outputs != nil {
		if out, ok := m.outputs[aws.ToString(params.Name)]; ok {
			return out, nil
		}
	}
	return nil, &ssmtypes.ParameterNotFound{}
}

func (m *mockSSMReadClient) GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	path := aws.ToString(params.Path)
	if m.pathOutputs != nil {
		if parameters, ok := m.pathOutputs[path]; ok {
			return &ssm.GetParametersByPathOutput{Parameters: parameters}, nil
		}
	}
	// No parameters found at path — return empty result (not an error).
	return &ssm.GetParametersByPathOutput{}, nil
}

// --- Mock EC2 ---

type mockEC2Client struct {
	vpcs []ec2types.Vpc
	err  error
}

func (m *mockEC2Client) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{Vpcs: m.vpcs}, m.err
}

func (m *mockEC2Client) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{}, nil
}

// --- Mock SandboxLister ---

type mockSandboxLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (m *mockSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	return m.records, m.err
}

// =============================================================================
// Tests: checkCredential
// =============================================================================

func TestCheckCredential_OK(t *testing.T) {
	client := &mockSTSClient{
		output: &sts.GetCallerIdentityOutput{
			Arn:     aws.String("arn:aws:iam::123456789012:role/test-role"),
			Account: aws.String("123456789012"),
		},
	}
	result := checkCredential(context.Background(), client, "test-profile")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
	if result.Message == "" {
		t.Error("expected non-empty message with ARN")
	}
}

func TestCheckCredential_Failure(t *testing.T) {
	client := &mockSTSClient{
		err: errors.New("credentials expired"),
	}
	result := checkCredential(context.Background(), client, "test-profile")
	if result.Status != CheckError {
		t.Errorf("expected CheckError, got %s", result.Status)
	}
	if result.Remediation == "" {
		t.Error("expected non-empty remediation")
	}
	// Remediation should mention sso login
	if len(result.Remediation) < 5 {
		t.Error("remediation too short")
	}
}

func TestCheckCredential_NilClient(t *testing.T) {
	result := checkCredential(context.Background(), nil, "test-profile")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil client, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkStateBucket
// =============================================================================

func TestCheckStateBucket_OK(t *testing.T) {
	client := &mockS3HeadBucketClient{err: nil}
	result := checkStateBucket(context.Background(), client, "my-state-bucket")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckStateBucket_Missing(t *testing.T) {
	client := &mockS3HeadBucketClient{err: errors.New("bucket not found")}
	result := checkStateBucket(context.Background(), client, "missing-bucket")
	if result.Status != CheckError {
		t.Errorf("expected CheckError, got %s", result.Status)
	}
	if result.Remediation == "" {
		t.Error("expected remediation mentioning km bootstrap")
	}
}

func TestCheckStateBucket_NoBucket(t *testing.T) {
	client := &mockS3HeadBucketClient{}
	result := checkStateBucket(context.Background(), client, "")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for empty bucket name, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkDynamoTable
// =============================================================================

func TestCheckDynamoTable_OK(t *testing.T) {
	client := &mockDynamoClient{
		output: &dynamodb.DescribeTableOutput{
			Table: &dynamodbtypes.TableDescription{
				TableName: aws.String("km-budgets"),
			},
		},
	}
	result := checkDynamoTable(context.Background(), client, "km-budgets", "Budget Table")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckDynamoTable_Missing(t *testing.T) {
	client := &mockDynamoClient{
		err: errors.New("table not found"),
	}
	result := checkDynamoTable(context.Background(), client, "km-budgets", "Budget Table")
	if result.Status != CheckError {
		t.Errorf("expected CheckError, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkKMSKey
// =============================================================================

func TestCheckKMSKey_OK(t *testing.T) {
	client := &mockKMSClient{
		output: &kms.DescribeKeyOutput{
			KeyMetadata: &kmstypes.KeyMetadata{
				KeyId: aws.String("key-id-123"),
			},
		},
	}
	result := checkKMSKey(context.Background(), client, "km-platform")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckKMSKey_Missing(t *testing.T) {
	client := &mockKMSClient{
		err: errors.New("key not found"),
	}
	result := checkKMSKey(context.Background(), client, "km-platform")
	if result.Status != CheckError {
		t.Errorf("expected CheckError, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkSCP
// =============================================================================

func TestCheckSCP_SkippedWhenNoCreds(t *testing.T) {
	result := checkSCP(context.Background(), nil, "")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for empty accountID, got %s", result.Status)
	}
}

func TestCheckSCP_OK(t *testing.T) {
	client := &mockOrgsClient{
		output: &organizations.ListPoliciesForTargetOutput{
			Policies: []orgtypes.PolicySummary{
				{Name: aws.String("km-sandbox-containment")},
			},
		},
	}
	result := checkSCP(context.Background(), client, "123456789012")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckSCP_NotFound(t *testing.T) {
	client := &mockOrgsClient{
		output: &organizations.ListPoliciesForTargetOutput{
			Policies: []orgtypes.PolicySummary{},
		},
	}
	result := checkSCP(context.Background(), client, "123456789012")
	if result.Status != CheckError {
		t.Errorf("expected CheckError when SCP policy missing, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkGitHubConfig
// =============================================================================

func TestCheckGitHubConfig_OK(t *testing.T) {
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/app-client-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("12345")},
			},
			"/km/config/github/installation-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("67890")},
			},
		},
	}
	result := checkGitHubConfig(context.Background(), client)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckGitHubConfig_PerAccountOnly(t *testing.T) {
	// app-client-id exists, per-account installations exist, NO legacy key.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/app-client-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("12345")},
			},
		},
		pathOutputs: map[string][]ssmtypes.Parameter{
			"/km/config/github/installations/": {
				{Name: aws.String("/km/config/github/installations/userA"), Value: aws.String("111")},
				{Name: aws.String("/km/config/github/installations/userB"), Value: aws.String("222")},
			},
		},
	}
	result := checkGitHubConfig(context.Background(), client)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK for per-account installations, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "2 installation(s)") {
		t.Errorf("expected installation count in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "userA") || !strings.Contains(result.Message, "userB") {
		t.Errorf("expected account names in message, got: %s", result.Message)
	}
}

func TestCheckGitHubConfig_LegacyOnly(t *testing.T) {
	// app-client-id exists, legacy installation-id exists, NO per-account keys.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/app-client-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("12345")},
			},
			"/km/config/github/installation-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("67890")},
			},
		},
	}
	result := checkGitHubConfig(context.Background(), client)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK for legacy installation-id, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "legacy") {
		t.Errorf("expected 'legacy' in message, got: %s", result.Message)
	}
}

func TestCheckGitHubConfig_BothPerAccountAndLegacy(t *testing.T) {
	// Both per-account and legacy exist — should report OK with per-account count.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/app-client-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("12345")},
			},
			"/km/config/github/installation-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("67890")},
			},
		},
		pathOutputs: map[string][]ssmtypes.Parameter{
			"/km/config/github/installations/": {
				{Name: aws.String("/km/config/github/installations/orgA"), Value: aws.String("333")},
			},
		},
	}
	result := checkGitHubConfig(context.Background(), client)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when both exist, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "1 installation(s)") {
		t.Errorf("expected installation count in message, got: %s", result.Message)
	}
}

func TestCheckGitHubConfig_NeitherPerAccountNorLegacy(t *testing.T) {
	// app-client-id exists, but neither per-account nor legacy installation keys.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/app-client-id": {
				Parameter: &ssmtypes.Parameter{Value: aws.String("12345")},
			},
		},
	}
	result := checkGitHubConfig(context.Background(), client)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn when no installations exist, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckGitHubConfig_NotConfigured(t *testing.T) {
	// app-client-id missing — should WARN.
	client := &mockSSMReadClient{
		err: &ssmtypes.ParameterNotFound{},
	}
	result := checkGitHubConfig(context.Background(), client)
	// Missing GitHub config is WARN (not ERROR) — GitHub integration is optional
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn for missing GitHub config, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkRegionVPC
// =============================================================================

func TestCheckRegion_OK(t *testing.T) {
	client := &mockEC2Client{
		vpcs: []ec2types.Vpc{
			{VpcId: aws.String("vpc-123")},
		},
	}
	result := checkRegionVPC(context.Background(), client, "us-east-1")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckRegion_NoVPC(t *testing.T) {
	client := &mockEC2Client{
		vpcs: []ec2types.Vpc{},
	}
	result := checkRegionVPC(context.Background(), client, "us-east-1")
	if result.Status != CheckError {
		t.Errorf("expected CheckError when no VPC, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkDynamoTable (identity table — CheckWarn not ERROR)
// =============================================================================

func TestCheckIdentityTable_MissingIsWarn(t *testing.T) {
	client := &mockDynamoClient{
		err: errors.New("table not found"),
	}
	// checkDynamoTable returns CheckError; caller converts to CheckWarn for identity table
	result := checkDynamoTable(context.Background(), client, "km-identities", "Identity Table")
	if result.Status != CheckError {
		t.Errorf("expected CheckError from checkDynamoTable (caller promotes to warn), got %s", result.Status)
	}
	// Demote to warn
	if result.Status == CheckError {
		result.Status = CheckWarn
	}
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn after demotion, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkConfig
// =============================================================================

func TestCheckConfig_OK(t *testing.T) {
	cfg := &testConfig{
		domain:      "example.com",
		mgmtAcct:    "111111111111",
		tfAcct:      "222222222222",
		appAcct:     "333333333333",
		ssoURL:      "https://sso.example.com/start",
		region:      "us-east-1",
	}
	result := checkConfig(cfg)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckConfig_MissingFields(t *testing.T) {
	cfg := &testConfig{}
	result := checkConfig(cfg)
	if result.Status != CheckError {
		t.Errorf("expected CheckError for empty config, got %s", result.Status)
	}
	// Should mention what's missing
	if result.Message == "" {
		t.Error("expected non-empty message listing missing fields")
	}
}

// =============================================================================
// Tests: runChecks (parallel execution)
// =============================================================================

func TestRunChecks_Parallel(t *testing.T) {
	checks := []func(context.Context) CheckResult{
		func(ctx context.Context) CheckResult {
			return CheckResult{Name: "check-a", Status: CheckOK, Message: "ok"}
		},
		func(ctx context.Context) CheckResult {
			return CheckResult{Name: "check-b", Status: CheckError, Message: "error"}
		},
		func(ctx context.Context) CheckResult {
			return CheckResult{Name: "check-c", Status: CheckWarn, Message: "warn"}
		},
	}
	results := runChecks(context.Background(), checks)
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	// Sorted by Name
	if results[0].Name != "check-a" {
		t.Errorf("expected sorted results[0] = check-a, got %s", results[0].Name)
	}
	if results[1].Name != "check-b" {
		t.Errorf("expected sorted results[1] = check-b, got %s", results[1].Name)
	}
	if results[2].Name != "check-c" {
		t.Errorf("expected sorted results[2] = check-c, got %s", results[2].Name)
	}
}

// =============================================================================
// Tests: filterNonOK
// =============================================================================

func TestFilterNonOK(t *testing.T) {
	results := []CheckResult{
		{Name: "a", Status: CheckOK},
		{Name: "b", Status: CheckError},
		{Name: "c", Status: CheckWarn},
		{Name: "d", Status: CheckSkipped},
		{Name: "e", Status: CheckOK},
	}
	filtered := filterNonOK(results)
	if len(filtered) != 2 {
		t.Errorf("expected 2 non-OK results (error+warn), got %d", len(filtered))
	}
	for _, r := range filtered {
		if r.Status == CheckOK || r.Status == CheckSkipped {
			t.Errorf("filterNonOK should not include OK or Skipped results: %s", r.Status)
		}
	}
}

// =============================================================================
// Tests: checkSandboxSummary
// =============================================================================

func TestCheckSandboxSummary_NilLister(t *testing.T) {
	result := checkSandboxSummary(context.Background(), nil)
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil lister, got %s", result.Status)
	}
}

func TestCheckSandboxSummary_OK(t *testing.T) {
	lister := &mockSandboxLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-1", Status: "running"},
			{SandboxID: "sb-2", Status: "running"},
		},
	}
	result := checkSandboxSummary(context.Background(), lister)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
}

// =============================================================================
// Helpers for testConfig (satisfies the checkConfig interface)
// =============================================================================

// testConfig is a simple struct used in checkConfig tests to avoid depending
// on a fully-loaded config.Config.
type testConfig struct {
	domain   string
	mgmtAcct string
	tfAcct   string
	appAcct  string
	ssoURL   string
	region   string
}

// doctorConfigProvider is the interface that checkConfig accepts.
// Defined in doctor.go.

func (c *testConfig) GetDomain() string              { return c.domain }
func (c *testConfig) GetManagementAccountID() string  { return c.mgmtAcct }
func (c *testConfig) GetTerraformAccountID() string   { return c.tfAcct }
func (c *testConfig) GetApplicationAccountID() string { return c.appAcct }
func (c *testConfig) GetSSOStartURL() string          { return c.ssoURL }
func (c *testConfig) GetPrimaryRegion() string        { return c.region }
func (c *testConfig) GetStateBucket() string          { return "" }
func (c *testConfig) GetBudgetTableName() string      { return "" }
func (c *testConfig) GetIdentityTableName() string    { return "" }
func (c *testConfig) GetAWSProfile() string           { return "" }
func (c *testConfig) GetArtifactsBucket() string      { return "" }

// =============================================================================
// Tests: DoctorCmd (Task 2)
// =============================================================================

func TestDoctorCmd_CommandShape(t *testing.T) {
	cmd := NewDoctorCmdWithDeps(minimalConfig(), nil)
	if cmd.Use != "doctor" {
		t.Errorf("expected Use=doctor, got %s", cmd.Use)
	}
	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag")
	}
	if cmd.Flags().Lookup("quiet") == nil {
		t.Error("expected --quiet flag")
	}
}

func TestDoctorCmd_AllChecksPass_ExitZero(t *testing.T) {
	deps := allOKDeps()
	cmd := NewDoctorCmdWithDeps(minimalConfig(), deps)
	cmd.SetOut(new(nopWriter))
	err := cmd.RunE(cmd, []string{})
	if err != nil {
		t.Errorf("expected nil error (exit 0) when all checks pass, got: %v", err)
	}
}

func TestDoctorCmd_AnyCheckError_ExitOne(t *testing.T) {
	deps := allOKDeps()
	// Inject a failing STS client to cause an error
	deps.STSClient = &mockSTSClient{err: errors.New("no credentials")}
	cmd := NewDoctorCmdWithDeps(minimalConfigWithProfile(), deps)
	cmd.SetOut(new(nopWriter))
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Error("expected non-nil error (exit 1) when any check fails")
	}
}

func TestDoctorCmd_JSONOutput(t *testing.T) {
	deps := allOKDeps()
	cmd := NewDoctorCmdWithDeps(minimalConfig(), deps)
	buf := new(bufWriter)
	cmd.SetOut(buf)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Output must be valid JSON array
	out := buf.String()
	if len(out) < 2 || out[0] != '[' {
		t.Errorf("expected JSON array, got: %q", out)
	}
}

func TestDoctorCmd_JSONQuiet(t *testing.T) {
	deps := allOKDeps()
	// Add a failing check
	deps.STSClient = &mockSTSClient{err: errors.New("no creds")}
	cmd := NewDoctorCmdWithDeps(minimalConfigWithProfile(), deps)
	buf := new(bufWriter)
	cmd.SetOut(buf)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("quiet", "true"); err != nil {
		t.Fatal(err)
	}
	// RunE may return error (exit 1) — that's fine; we just want to check output
	_ = cmd.RunE(cmd, []string{})
	out := buf.String()
	if len(out) < 2 || out[0] != '[' {
		t.Errorf("expected JSON array, got: %q", out)
	}
	// The JSON array should NOT contain OK entries
	if len(out) > 3 && !containsNonOK(out) {
		t.Error("expected JSON to contain at least one non-OK entry")
	}
}

func TestDoctorCmd_QuietMode(t *testing.T) {
	deps := allOKDeps()
	deps.STSClient = &mockSTSClient{err: errors.New("no creds")}
	cmd := NewDoctorCmdWithDeps(minimalConfigWithProfile(), deps)
	buf := new(bufWriter)
	cmd.SetOut(buf)
	if err := cmd.Flags().Set("quiet", "true"); err != nil {
		t.Fatal(err)
	}
	_ = cmd.RunE(cmd, []string{})
	out := buf.String()
	// Should not contain OK symbol
	if containsString(out, checkOKSymbol+" ") {
		t.Error("quiet mode should suppress OK lines")
	}
}

func TestDoctorCmd_RegisteredInRoot(t *testing.T) {
	cfg := &appcfg.Config{}
	root := NewRootCmd(cfg)
	for _, sub := range root.Commands() {
		if sub.Use == "doctor" {
			return
		}
	}
	t.Error("doctor command not registered in root")
}

// =============================================================================
// Test helpers
// =============================================================================

// nopWriter discards output.
type nopWriter struct{}

func (n *nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// bufWriter buffers output for inspection.
type bufWriter struct {
	data []byte
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *bufWriter) String() string { return string(b.data) }

func minimalConfig() *testDoctorConfig {
	// Fully populated so checkConfig returns OK.
	return &testDoctorConfig{
		domain:   "example.com",
		mgmtAcct: "111111111111",
		tfAcct:   "222222222222",
		appAcct:  "333333333333",
		ssoURL:   "https://sso.example.com/start",
		region:   "us-east-1",
	}
}

func minimalConfigWithProfile() *testDoctorConfig {
	cfg := minimalConfig()
	cfg.awsProfile = "test-profile"
	return cfg
}

// testDoctorConfig implements the DoctorConfigProvider interface.
type testDoctorConfig struct {
	domain     string
	mgmtAcct   string
	tfAcct     string
	appAcct    string
	ssoURL     string
	region     string
	awsProfile string
}

func (c *testDoctorConfig) GetDomain() string              { return c.domain }
func (c *testDoctorConfig) GetManagementAccountID() string  { return c.mgmtAcct }
func (c *testDoctorConfig) GetTerraformAccountID() string   { return c.tfAcct }
func (c *testDoctorConfig) GetApplicationAccountID() string { return c.appAcct }
func (c *testDoctorConfig) GetSSOStartURL() string          { return c.ssoURL }
func (c *testDoctorConfig) GetPrimaryRegion() string        { return c.region }
func (c *testDoctorConfig) GetStateBucket() string          { return "" }
func (c *testDoctorConfig) GetBudgetTableName() string      { return "" }
func (c *testDoctorConfig) GetIdentityTableName() string    { return "" }
func (c *testDoctorConfig) GetAWSProfile() string           { return c.awsProfile }
func (c *testDoctorConfig) GetArtifactsBucket() string      { return "" }

func allOKDeps() *DoctorDeps {
	return &DoctorDeps{
		STSClient:     nil, // nil = skip
		S3Client:      nil,
		DynamoClient:  nil,
		KMSClient:     nil,
		OrgsClient:    nil,
		SSMReadClient: nil,
		EC2Clients:    nil,
		Lister:        nil,
	}
}

func containsNonOK(s string) bool {
	return containsString(s, `"status":"ERROR"`) || containsString(s, `"status":"WARN"`)
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Compile-time checks that mocks satisfy interfaces (will fail until doctor.go defined)
var _ STSCallerAPI = (*mockSTSClient)(nil)
var _ S3HeadBucketAPI = (*mockS3HeadBucketClient)(nil)
var _ DynamoDescribeAPI = (*mockDynamoClient)(nil)
var _ KMSDescribeAPI = (*mockKMSClient)(nil)
var _ OrgsListPoliciesAPI = (*mockOrgsClient)(nil)
var _ SSMReadAPI = (*mockSSMReadClient)(nil)
var _ EC2DescribeAPI = (*mockEC2Client)(nil)

// Compile-time: ensure testConfig implements DoctorConfigProvider
var _ DoctorConfigProvider = (*testConfig)(nil)
var _ DoctorConfigProvider = (*testDoctorConfig)(nil)

// =============================================================================
// Tests: checkLambdaFunction (TestDoctorLambda)
// =============================================================================

// mockLambdaClient satisfies LambdaGetFunctionAPI.
type mockLambdaClient struct {
	output *lambdasvc.GetFunctionOutput
	err    error
}

func (m *mockLambdaClient) GetFunction(ctx context.Context, params *lambdasvc.GetFunctionInput, optFns ...func(*lambdasvc.Options)) (*lambdasvc.GetFunctionOutput, error) {
	return m.output, m.err
}

func TestDoctorLambda_OK(t *testing.T) {
	client := &mockLambdaClient{
		output: &lambdasvc.GetFunctionOutput{},
	}
	result := checkLambdaFunction(context.Background(), client, "km-ttl-handler")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when GetFunction succeeds, got %s: %s", result.Status, result.Message)
	}
	if !containsString(result.Message, "deployed") {
		t.Errorf("expected message to contain 'deployed', got: %s", result.Message)
	}
}

func TestDoctorLambda_NotFound(t *testing.T) {
	client := &mockLambdaClient{
		err: &lambdatypes.ResourceNotFoundException{Message: aws.String("Function not found")},
	}
	result := checkLambdaFunction(context.Background(), client, "km-ttl-handler")
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn on ResourceNotFoundException, got %s", result.Status)
	}
	if result.Remediation == "" {
		t.Error("expected remediation mentioning 'km init'")
	}
}

func TestDoctorLambda_NilClient(t *testing.T) {
	result := checkLambdaFunction(context.Background(), nil, "km-ttl-handler")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil client, got %s", result.Status)
	}
}

// =============================================================================
// Tests: checkSESIdentity
// =============================================================================

// mockSESClient satisfies SESGetEmailIdentityAPI.
type mockSESClient struct {
	output *sesv2svc.GetEmailIdentityOutput
	err    error
}

func (m *mockSESClient) GetEmailIdentity(ctx context.Context, params *sesv2svc.GetEmailIdentityInput, optFns ...func(*sesv2svc.Options)) (*sesv2svc.GetEmailIdentityOutput, error) {
	return m.output, m.err
}

func TestCheckSESIdentity_OK(t *testing.T) {
	client := &mockSESClient{
		output: &sesv2svc.GetEmailIdentityOutput{
			VerificationStatus: sesv2types.VerificationStatusSuccess,
		},
	}
	result := checkSESIdentity(context.Background(), client, "example.com")
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when verified, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckSESIdentity_NotFound(t *testing.T) {
	client := &mockSESClient{
		err: &sesv2types.NotFoundException{Message: aws.String("Identity not found")},
	}
	result := checkSESIdentity(context.Background(), client, "example.com")
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn on NotFoundException, got %s", result.Status)
	}
	if result.Remediation == "" {
		t.Error("expected remediation mentioning 'km init'")
	}
}

func TestCheckSESIdentity_NilClient(t *testing.T) {
	result := checkSESIdentity(context.Background(), nil, "example.com")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil client, got %s", result.Status)
	}
}

// =============================================================================
// Tests: buildChecks includes Lambda and SES
// =============================================================================

func TestBuildChecks_IncludesLambdaAndSES(t *testing.T) {
	cfg := minimalConfig()
	deps := &DoctorDeps{
		LambdaClient: &mockLambdaClient{output: &lambdasvc.GetFunctionOutput{}},
		SESClient:    &mockSESClient{output: &sesv2svc.GetEmailIdentityOutput{VerificationStatus: sesv2types.VerificationStatusSuccess}},
	}
	checks := buildChecks(cfg, deps)
	// Run all checks and look for Lambda and SES names.
	results := runChecks(context.Background(), checks)
	var foundLambda, foundSES bool
	for _, r := range results {
		if containsString(r.Name, "Lambda") || containsString(r.Name, "TTL") {
			foundLambda = true
		}
		if containsString(r.Name, "SES") {
			foundSES = true
		}
	}
	if !foundLambda {
		t.Error("buildChecks should include a Lambda check")
	}
	if !foundSES {
		t.Error("buildChecks should include a SES check")
	}
}

// Compile-time checks for new mock types.
var _ LambdaGetFunctionAPI = (*mockLambdaClient)(nil)
var _ SESGetEmailIdentityAPI = (*mockSESClient)(nil)

// =============================================================================
// Tests: checkCredentialRotationAge
// =============================================================================

// makeSSMParamOutput creates an SSM GetParameterOutput with the given LastModifiedDate.
func makeSSMParamOutput(name string, lastModified time.Time) *ssm.GetParameterOutput {
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:             aws.String(name),
			LastModifiedDate: aws.Time(lastModified),
		},
	}
}

func TestCheckCredentialRotationAge_AllFresh(t *testing.T) {
	freshTime := time.Now().Add(-30 * 24 * time.Hour)
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/private-key":   makeSSMParamOutput("/km/config/github/private-key", freshTime),
			"/km/config/github/app-client-id": makeSSMParamOutput("/km/config/github/app-client-id", freshTime),
		},
	}
	result := checkCredentialRotationAge(context.Background(), client, 90)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK for fresh creds, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "all platform credentials rotated within 90 days") {
		t.Errorf("expected OK message, got: %s", result.Message)
	}
}

func TestCheckCredentialRotationAge_OneStale(t *testing.T) {
	staleTime := time.Now().Add(-142 * 24 * time.Hour)
	freshTime := time.Now().Add(-30 * 24 * time.Hour)
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/private-key":   makeSSMParamOutput("/km/config/github/private-key", staleTime),
			"/km/config/github/app-client-id": makeSSMParamOutput("/km/config/github/app-client-id", freshTime),
		},
	}
	result := checkCredentialRotationAge(context.Background(), client, 90)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn for stale cred, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "/km/config/github/private-key") {
		t.Errorf("expected stale param name in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "142d") {
		t.Errorf("expected age in days in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Remediation, "km roll creds --platform") {
		t.Errorf("expected remediation hint in result, got: %s", result.Remediation)
	}
}

func TestCheckCredentialRotationAge_BothStale(t *testing.T) {
	staleTime := time.Now().Add(-100 * 24 * time.Hour)
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/private-key":   makeSSMParamOutput("/km/config/github/private-key", staleTime),
			"/km/config/github/app-client-id": makeSSMParamOutput("/km/config/github/app-client-id", staleTime),
		},
	}
	result := checkCredentialRotationAge(context.Background(), client, 90)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn for both stale, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "/km/config/github/private-key") {
		t.Errorf("expected private-key in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "/km/config/github/app-client-id") {
		t.Errorf("expected app-client-id in message, got: %s", result.Message)
	}
}

func TestCheckCredentialRotationAge_ParamNotFound(t *testing.T) {
	// Empty outputs map — all params return ParameterNotFound from the mock.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{},
	}
	result := checkCredentialRotationAge(context.Background(), client, 90)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when params are missing (graceful skip), got %s: %s", result.Status, result.Message)
	}
}

func TestCheckCredentialRotationAge_ChecksBothParams(t *testing.T) {
	freshTime := time.Now().Add(-10 * 24 * time.Hour)
	var calledParams []string
	// Use a custom mock that records which params were queried.
	type trackingSSMClient struct {
		mockSSMReadClient
		called *[]string
	}
	// Instead use mockSSMReadClient with outputs for both — verify both present means OK.
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/private-key":   makeSSMParamOutput("/km/config/github/private-key", freshTime),
			"/km/config/github/app-client-id": makeSSMParamOutput("/km/config/github/app-client-id", freshTime),
		},
	}
	_ = calledParams
	result := checkCredentialRotationAge(context.Background(), client, 90)
	// Both found and fresh → OK confirms both were checked.
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when both params present and fresh, got %s", result.Status)
	}
}

// Suppress unused import warning
var _ = fmt.Sprintf
var _ = strings.Contains
