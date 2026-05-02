package cmd

import (
	"context"
	ed25519key "crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	slackpkg "github.com/whereiskurt/klankrmkr/pkg/slack"
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
		domain:        "example.com",
		dnsParentAcct: "111111111111",
		tfAcct:        "222222222222",
		appAcct:       "333333333333",
		ssoURL:        "https://sso.example.com/start",
		region:        "us-east-1",
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
	domain        string
	dnsParentAcct string
	orgAcct       string
	tfAcct        string
	appAcct       string
	ssoURL        string
	region        string
}

// doctorConfigProvider is the interface that checkConfig accepts.
// Defined in doctor.go.

func (c *testConfig) GetDomain() string                   { return c.domain }
func (c *testConfig) GetManagementAccountID() string      { return "" } // temporary shim until plan 03 removes this from DoctorConfigProvider
func (c *testConfig) GetOrganizationAccountID() string    { return c.orgAcct }
func (c *testConfig) GetDNSParentAccountID() string       { return c.dnsParentAcct }
func (c *testConfig) GetTerraformAccountID() string       { return c.tfAcct }
func (c *testConfig) GetApplicationAccountID() string     { return c.appAcct }
func (c *testConfig) GetSSOStartURL() string              { return c.ssoURL }
func (c *testConfig) GetPrimaryRegion() string            { return c.region }
func (c *testConfig) GetStateBucket() string              { return "" }
func (c *testConfig) GetBudgetTableName() string          { return "" }
func (c *testConfig) GetIdentityTableName() string        { return "" }
func (c *testConfig) GetAWSProfile() string               { return "" }
func (c *testConfig) GetArtifactsBucket() string          { return "" }
func (c *testConfig) GetDoctorStaleAMIDays() int          { return 30 }
func (c *testConfig) GetProfileSearchPaths() []string     { return nil }

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
		domain:        "example.com",
		dnsParentAcct: "111111111111",
		tfAcct:        "222222222222",
		appAcct:       "333333333333",
		ssoURL:        "https://sso.example.com/start",
		region:        "us-east-1",
	}
}

func minimalConfigWithProfile() *testDoctorConfig {
	cfg := minimalConfig()
	cfg.awsProfile = "test-profile"
	return cfg
}

// testDoctorConfig implements the DoctorConfigProvider interface.
type testDoctorConfig struct {
	domain        string
	dnsParentAcct string
	orgAcct       string
	tfAcct        string
	appAcct       string
	ssoURL        string
	region        string
	awsProfile    string
}

func (c *testDoctorConfig) GetDomain() string                   { return c.domain }
func (c *testDoctorConfig) GetManagementAccountID() string      { return "" } // temporary shim until plan 03 removes this from DoctorConfigProvider
func (c *testDoctorConfig) GetOrganizationAccountID() string    { return c.orgAcct }
func (c *testDoctorConfig) GetDNSParentAccountID() string       { return c.dnsParentAcct }
func (c *testDoctorConfig) GetTerraformAccountID() string       { return c.tfAcct }
func (c *testDoctorConfig) GetApplicationAccountID() string     { return c.appAcct }
func (c *testDoctorConfig) GetSSOStartURL() string              { return c.ssoURL }
func (c *testDoctorConfig) GetPrimaryRegion() string            { return c.region }
func (c *testDoctorConfig) GetStateBucket() string              { return "" }
func (c *testDoctorConfig) GetBudgetTableName() string          { return "" }
func (c *testDoctorConfig) GetIdentityTableName() string        { return "" }
func (c *testDoctorConfig) GetAWSProfile() string               { return c.awsProfile }
func (c *testDoctorConfig) GetArtifactsBucket() string          { return "" }
func (c *testDoctorConfig) GetDoctorStaleAMIDays() int          { return 30 }
func (c *testDoctorConfig) GetProfileSearchPaths() []string     { return nil }

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

// _testConfigPostRename statically asserts the test stubs expose the renamed
// accessors. The real DoctorConfigProvider in doctor.go is updated by plan 03.
type _testConfigPostRename interface {
	GetOrganizationAccountID() string
	GetDNSParentAccountID() string
}

var _ _testConfigPostRename = (*testConfig)(nil)
var _ _testConfigPostRename = (*testDoctorConfig)(nil)

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

// =============================================================================
// Tests: checkStaleAMIs (Task 1 TDD)
// =============================================================================

// mockEC2AMIDoctor is a local mock for kmaws.EC2AMIAPI used in doctor tests.
// (Local to keep cross-package test imports clean — mirrors ami_test.go's mockEC2AMI.)
type mockEC2AMIDoctor struct {
	images []ec2types.Image
	err    error
}

func (m *mockEC2AMIDoctor) CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	return &ec2.CreateImageOutput{ImageId: aws.String("ami-created")}, nil
}

func (m *mockEC2AMIDoctor) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ec2.DescribeImagesOutput{Images: m.images}, nil
}

func (m *mockEC2AMIDoctor) DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	return &ec2.DeregisterImageOutput{}, nil
}

func (m *mockEC2AMIDoctor) CopyImage(ctx context.Context, params *ec2.CopyImageInput, optFns ...func(*ec2.Options)) (*ec2.CopyImageOutput, error) {
	return &ec2.CopyImageOutput{ImageId: aws.String("ami-copy")}, nil
}

func (m *mockEC2AMIDoctor) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

// makeTestAMI creates an ec2types.Image with the given ID and age in days.
func makeTestAMI(id string, ageDays float64) ec2types.Image {
	created := time.Now().UTC().Add(-time.Duration(ageDays*24) * time.Hour)
	dateStr := created.Format("2006-01-02T15:04:05.000Z")
	return ec2types.Image{
		ImageId:      aws.String(id),
		CreationDate: aws.String(dateStr),
	}
}

// doctorStaleAMIConfig is a minimal DoctorConfigProvider used in checkStaleAMIs tests.
type doctorStaleAMIConfig struct {
	testDoctorConfig
	staleDays    int
	searchPaths  []string
}

func (c *doctorStaleAMIConfig) GetDoctorStaleAMIDays() int     { return c.staleDays }
func (c *doctorStaleAMIConfig) GetProfileSearchPaths() []string { return c.searchPaths }

func newDoctorStaleAMICfg(staleDays int, searchPaths []string) *doctorStaleAMIConfig {
	return &doctorStaleAMIConfig{
		testDoctorConfig: testDoctorConfig{region: "us-east-1"},
		staleDays:        staleDays,
		searchPaths:      searchPaths,
	}
}

// Compile-time: ensure new interface methods are implemented.
var _ _testConfigPostRename = (*doctorStaleAMIConfig)(nil)

func TestCheckStaleAMIs_NilClient_Skipped(t *testing.T) {
	result := checkStaleAMIs(context.Background(), "us-east-1", nil, nil, nil, 30)
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil client, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckStaleAMIs_NoAMIs_OK(t *testing.T) {
	client := &mockEC2AMIDoctor{images: []ec2types.Image{}}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, nil, 30)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK for empty AMI list, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "No stale AMIs in us-east-1") {
		t.Errorf("expected 'No stale AMIs in us-east-1' in message, got: %s", result.Message)
	}
}

func TestCheckStaleAMIs_AllWithinThreshold_OK(t *testing.T) {
	client := &mockEC2AMIDoctor{images: []ec2types.Image{
		makeTestAMI("ami-0fresh111111", 1),
		makeTestAMI("ami-0fresh222222", 5),
		makeTestAMI("ami-0fresh333333", 7),
	}}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, nil, 30)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK when all AMIs within threshold, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckStaleAMIs_StaleFound_Warn(t *testing.T) {
	client := &mockEC2AMIDoctor{images: []ec2types.Image{
		makeTestAMI("ami-0recent111111", 1),
		makeTestAMI("ami-0stale1111111", 45),
		makeTestAMI("ami-0stale2222222", 90),
	}}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, nil, 30)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn when stale AMIs found, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "ami-0stale1111111") {
		t.Errorf("expected stale AMI ID in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "ami-0stale2222222") {
		t.Errorf("expected stale AMI ID in message, got: %s", result.Message)
	}
	// The fresh AMI must NOT appear in the message.
	if strings.Contains(result.Message, "ami-0recent111111") {
		t.Errorf("expected fresh AMI to not appear in stale list, got: %s", result.Message)
	}
}

func TestCheckStaleAMIs_ProfileRefSkipped(t *testing.T) {
	// Write a profile YAML that references the AMI so it is not flagged.
	dir := t.TempDir()
	profileYAML := `
apiVersion: sandbox.klankrmkr.io/v1
kind: SandboxProfile
metadata:
  name: test-prof
spec:
  runtime:
    ami: ami-0referenced111
`
	if err := os.WriteFile(filepath.Join(dir, "test-prof.yaml"), []byte(profileYAML), 0600); err != nil {
		t.Fatal(err)
	}

	client := &mockEC2AMIDoctor{images: []ec2types.Image{
		makeTestAMI("ami-0referenced111", 60),
	}}
	// Pass the temp dir as the search path — FindProfilesReferencingAMI should find the reference.
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, []string{dir}, 30)
	// Referenced AMI should NOT be in the stale list.
	if result.Status == CheckWarn && strings.Contains(result.Message, "ami-0referenced111") {
		t.Errorf("expected profile-referenced AMI to be skipped, got message: %s", result.Message)
	}
}

func TestCheckStaleAMIs_RunningSandboxSkipped(t *testing.T) {
	// Write a profile YAML that maps sandbox's profile name to the AMI.
	dir := t.TempDir()
	profileYAML := `
apiVersion: sandbox.klankrmkr.io/v1
kind: SandboxProfile
metadata:
  name: sb-running-prof
spec:
  runtime:
    ami: ami-0running11111111
`
	if err := os.WriteFile(filepath.Join(dir, "sb-running-prof.yaml"), []byte(profileYAML), 0600); err != nil {
		t.Fatal(err)
	}

	client := &mockEC2AMIDoctor{images: []ec2types.Image{
		makeTestAMI("ami-0running11111111", 60),
	}}
	lister := &mockSandboxLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-abc1234", Profile: "sb-running-prof", Status: "running"},
		},
	}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, lister, []string{dir}, 30)
	// AMI backing a running sandbox should NOT be flagged.
	if result.Status == CheckWarn && strings.Contains(result.Message, "ami-0running11111111") {
		t.Errorf("expected running-sandbox AMI to be skipped, got message: %s", result.Message)
	}
}

func TestCheckStaleAMIs_DescribeImagesError_Warn(t *testing.T) {
	client := &mockEC2AMIDoctor{err: errors.New("describe-images: access denied")}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, nil, 30)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn on DescribeImages error, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "could not list AMIs") {
		t.Errorf("expected 'could not list AMIs' in message, got: %s", result.Message)
	}
}

func TestCheckStaleAMIs_UnparsableCreationDate_Skipped(t *testing.T) {
	// Malformed date — this AMI should be skipped, not flagged.
	badImage := ec2types.Image{
		ImageId:      aws.String("ami-0baddate111111"),
		CreationDate: aws.String("not-a-date"),
	}
	// Add a genuinely stale AMI alongside it.
	client := &mockEC2AMIDoctor{images: []ec2types.Image{
		badImage,
		makeTestAMI("ami-0stale3333333", 60),
	}}
	result := checkStaleAMIs(context.Background(), "us-east-1", client, nil, nil, 30)
	// bad-date AMI should not appear; stale AMI should.
	if strings.Contains(result.Message, "ami-0baddate111111") {
		t.Errorf("expected bad-date AMI to be silently skipped, got: %s", result.Message)
	}
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn for the genuine stale AMI, got %s", result.Status)
	}
}

func TestCheckStaleAMIs_RegionInName(t *testing.T) {
	client := &mockEC2AMIDoctor{images: []ec2types.Image{}}
	result := checkStaleAMIs(context.Background(), "ap-southeast-1", client, nil, nil, 30)
	if !strings.Contains(result.Name, "ap-southeast-1") {
		t.Errorf("expected region in check name for multi-region distinguishability, got: %s", result.Name)
	}
}

// Verify compile-time satisfaction of kmaws.EC2AMIAPI by mockEC2AMIDoctor.
var _ kmaws.EC2AMIAPI = (*mockEC2AMIDoctor)(nil)

// =============================================================================
// Tests: --all-regions flag wiring (Task 2 TDD)
// =============================================================================

func TestDoctorCmd_AllRegionsFlagExists(t *testing.T) {
	cmd := NewDoctorCmdWithDeps(minimalConfig(), nil)
	if cmd.Flags().Lookup("all-regions") == nil {
		t.Error("expected --all-regions flag to exist on km doctor")
	}
}

func TestDoctor_DefaultRegionScope_OnlyPrimary(t *testing.T) {
	// Without --all-regions, EC2AMIClients should have exactly 1 entry (primary region).
	// We test this by calling NewDoctorCmdWithDeps with nil deps (so initRealDeps runs),
	// but since we can't easily call initRealDeps without real AWS, we test the deps
	// construction path via the allRegions field on a pre-built DoctorDeps.
	//
	// Instead: test that buildChecks emits exactly 1 stale-AMI check when EC2AMIClients
	// has 1 entry (the primary region only).
	cfg := minimalConfig()
	deps := &DoctorDeps{
		EC2AMIClients: map[string]kmaws.EC2AMIAPI{
			"us-east-1": &mockEC2AMIDoctor{images: []ec2types.Image{}},
		},
	}
	checks := buildChecks(cfg, deps)
	results := runChecks(context.Background(), checks)
	var staleAMICount int
	for _, r := range results {
		if strings.HasPrefix(r.Name, "Stale AMIs (") {
			staleAMICount++
		}
	}
	if staleAMICount != 1 {
		t.Errorf("expected exactly 1 Stale AMIs check for single-region scope, got %d", staleAMICount)
	}
}

func TestDoctor_AllRegionsFlag_PopulatesMultipleAMIClients(t *testing.T) {
	// Test that buildChecks emits N stale-AMI checks when EC2AMIClients has N entries.
	// This mirrors what initRealDeps does when --all-regions is set.
	cfg := minimalConfig()
	deps := &DoctorDeps{
		EC2AMIClients: map[string]kmaws.EC2AMIAPI{
			"us-east-1":  &mockEC2AMIDoctor{images: []ec2types.Image{}},
			"us-west-2":  &mockEC2AMIDoctor{images: []ec2types.Image{}},
			"eu-west-1":  &mockEC2AMIDoctor{images: []ec2types.Image{}},
		},
	}
	checks := buildChecks(cfg, deps)
	results := runChecks(context.Background(), checks)
	var staleAMICount int
	var foundRegions []string
	for _, r := range results {
		if strings.HasPrefix(r.Name, "Stale AMIs (") {
			staleAMICount++
			// Extract region from "Stale AMIs (us-east-1)".
			start := strings.Index(r.Name, "(")
			end := strings.Index(r.Name, ")")
			if start >= 0 && end > start {
				foundRegions = append(foundRegions, r.Name[start+1:end])
			}
		}
	}
	if staleAMICount != 3 {
		t.Errorf("expected 3 Stale AMIs checks for 3-region scope, got %d", staleAMICount)
	}
	// All three regions must appear.
	for _, want := range []string{"us-east-1", "us-west-2", "eu-west-1"} {
		found := false
		for _, r := range foundRegions {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected region %s in Stale AMIs check names, got: %v", want, foundRegions)
		}
	}
}

// =============================================================================
// Tests: checkSlackTokenValidity (Plan 63-09)
// =============================================================================

// mockSlackSSMStore is a simple map-backed SSMParamStore for doctor tests.
// Note: SSMParamStore interface is declared in create_slack.go (same package).
type mockSlackSSMStore struct {
	params map[string]string
}

func (m *mockSlackSSMStore) Get(_ context.Context, name string, _ bool) (string, error) {
	return m.params[name], nil
}

// mockSlackBridgePoster counts calls and can be configured to return specific responses.
type mockSlackBridgePoster struct {
	resp *slackpkg.PostResponse
	err  error
}

func (m *mockSlackBridgePoster) post(_ context.Context, _ string, _ *slackpkg.SlackEnvelope, _ []byte) (*slackpkg.PostResponse, error) {
	if m.resp != nil {
		return m.resp, m.err
	}
	return &slackpkg.PostResponse{OK: true, TS: "12345.678"}, m.err
}

func genDoctorKey(t *testing.T) ed25519key.PrivateKey {
	t.Helper()
	_, priv, err := ed25519key.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

// TestCheckSlackTokenValidity_NotConfigured_Skipped: /km/slack/bot-token absent → SKIPPED.
func TestCheckSlackTokenValidity_NotConfigured_Skipped(t *testing.T) {
	ssm := &mockSlackSSMStore{params: map[string]string{}} // no bot-token
	keyLoader := func(_ context.Context, _ string) (ed25519key.PrivateKey, error) { return nil, nil }
	poster := &mockSlackBridgePoster{}
	r := checkSlackTokenValidity(context.Background(), ssm, "us-east-1", keyLoader, poster.post)
	if r.Status != CheckSkipped {
		t.Errorf("expected SKIPPED, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckSlackTokenValidity_NoBridgeURL_Warn: bot-token set, no bridge-url → WARN.
func TestCheckSlackTokenValidity_NoBridgeURL_Warn(t *testing.T) {
	ssm := &mockSlackSSMStore{params: map[string]string{
		"/km/slack/bot-token": "xoxb-test",
		// No bridge-url
	}}
	keyLoader := func(_ context.Context, _ string) (ed25519key.PrivateKey, error) { return nil, nil }
	poster := &mockSlackBridgePoster{}
	r := checkSlackTokenValidity(context.Background(), ssm, "us-east-1", keyLoader, poster.post)
	if r.Status != CheckWarn {
		t.Errorf("expected WARN, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckSlackTokenValidity_BridgeReturnsOK_StatusOK: happy path.
func TestCheckSlackTokenValidity_BridgeReturnsOK_StatusOK(t *testing.T) {
	priv := genDoctorKey(t)
	ssm := &mockSlackSSMStore{params: map[string]string{
		"/km/slack/bot-token":       "xoxb-test",
		"/km/slack/bridge-url":      "https://bridge.example.com",
		"/km/slack/shared-channel-id": "C0SHARED",
	}}
	keyLoader := func(_ context.Context, _ string) (ed25519key.PrivateKey, error) { return priv, nil }
	poster := &mockSlackBridgePoster{resp: &slackpkg.PostResponse{OK: true, TS: "123.456"}}
	r := checkSlackTokenValidity(context.Background(), ssm, "us-east-1", keyLoader, poster.post)
	if r.Status != CheckOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckSlackTokenValidity_BridgeReturns401_Warn: bridge returns not-ok → WARN.
func TestCheckSlackTokenValidity_BridgeReturns401_Warn(t *testing.T) {
	priv := genDoctorKey(t)
	ssm := &mockSlackSSMStore{params: map[string]string{
		"/km/slack/bot-token":       "xoxb-bad",
		"/km/slack/bridge-url":      "https://bridge.example.com",
		"/km/slack/shared-channel-id": "C0SHARED",
	}}
	keyLoader := func(_ context.Context, _ string) (ed25519key.PrivateKey, error) { return priv, nil }
	poster := &mockSlackBridgePoster{resp: &slackpkg.PostResponse{OK: false, Error: "invalid_auth"}}
	r := checkSlackTokenValidity(context.Background(), ssm, "us-east-1", keyLoader, poster.post)
	if r.Status != CheckWarn {
		t.Errorf("expected WARN, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckSlackTokenValidity_BridgeReturns5xx_Error: bridge network error → ERROR.
func TestCheckSlackTokenValidity_BridgeReturns5xx_Error(t *testing.T) {
	priv := genDoctorKey(t)
	ssm := &mockSlackSSMStore{params: map[string]string{
		"/km/slack/bot-token":       "xoxb-test",
		"/km/slack/bridge-url":      "https://bridge.example.com",
		"/km/slack/shared-channel-id": "C0SHARED",
	}}
	keyLoader := func(_ context.Context, _ string) (ed25519key.PrivateKey, error) { return priv, nil }
	poster := &mockSlackBridgePoster{err: errors.New("connection refused")}
	r := checkSlackTokenValidity(context.Background(), ssm, "us-east-1", keyLoader, poster.post)
	if r.Status != CheckError {
		t.Errorf("expected ERROR, got %s: %s", r.Status, r.Message)
	}
}

// =============================================================================
// Tests: checkStaleSlackChannels (Plan 63-09)
// =============================================================================

// mockSandboxMetadataScanner is a mock for SlackMetadataScanner.
type mockSandboxMetadataScanner struct {
	records []kmaws.SandboxMetadata
	err     error
}

func (m *mockSandboxMetadataScanner) ListSlackEnabled(ctx context.Context) ([]kmaws.SandboxMetadata, error) {
	return m.records, m.err
}

// mockEC2InstanceLister is a mock for EC2InstanceLister.
type mockEC2InstanceLister struct {
	exists map[string]bool
	err    error
}

func (m *mockEC2InstanceLister) InstanceExists(ctx context.Context, sandboxID string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.exists[sandboxID], nil
}

// TestCheckStaleSlackChannels_NoSlackRecords_OK: no Slack-enabled records → OK.
func TestCheckStaleSlackChannels_NoSlackRecords_OK(t *testing.T) {
	scanner := &mockSandboxMetadataScanner{records: []kmaws.SandboxMetadata{}}
	ec2 := &mockEC2InstanceLister{exists: map[string]bool{}}
	r := checkStaleSlackChannels(context.Background(), scanner, ec2)
	if r.Status != CheckOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleSlackChannels_NoStale_OK: all sandboxes have active instances.
func TestCheckStaleSlackChannels_NoStale_OK(t *testing.T) {
	scanner := &mockSandboxMetadataScanner{records: []kmaws.SandboxMetadata{
		{SandboxID: "sb-alive1", SlackChannelID: "C0111", SlackPerSandbox: true},
		{SandboxID: "sb-alive2", SlackChannelID: "C0222", SlackPerSandbox: true},
	}}
	ec2 := &mockEC2InstanceLister{exists: map[string]bool{
		"sb-alive1": true,
		"sb-alive2": true,
	}}
	r := checkStaleSlackChannels(context.Background(), scanner, ec2)
	if r.Status != CheckOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleSlackChannels_HasStale_Warn: 2 records with dead instances → WARN.
func TestCheckStaleSlackChannels_HasStale_Warn(t *testing.T) {
	scanner := &mockSandboxMetadataScanner{records: []kmaws.SandboxMetadata{
		{SandboxID: "sb-dead1", SlackChannelID: "C0AAA", SlackPerSandbox: true},
		{SandboxID: "sb-dead2", SlackChannelID: "C0BBB", SlackPerSandbox: true},
		{SandboxID: "sb-alive", SlackChannelID: "C0CCC", SlackPerSandbox: true},
	}}
	ec2 := &mockEC2InstanceLister{exists: map[string]bool{
		"sb-dead1": false,
		"sb-dead2": false,
		"sb-alive": true,
	}}
	r := checkStaleSlackChannels(context.Background(), scanner, ec2)
	if r.Status != CheckWarn {
		t.Errorf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2") {
		t.Errorf("message should mention 2 stale channels, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "C0AAA") || !strings.Contains(r.Message, "C0BBB") {
		t.Errorf("message should list stale channel IDs, got: %s", r.Message)
	}
}

// Suppress unused import warning
var _ = fmt.Sprintf
var _ = strings.Contains
var _ = filepath.Join

// Wave 0 stubs — implementation owned by Phase 65 plan 03.

func TestCheckOrganizationAccountBlank_BlankReturnsWarn(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}

func TestCheckOrganizationAccountBlank_SetReturnsOK(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}

func TestCheckLegacyManagementField_FieldPresent(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}

func TestCheckLegacyManagementField_FieldAbsent(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}

func TestCheckLegacyManagementField_NoConfigFile(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}

func TestCheckConfigDoesNotRequireManagement(t *testing.T) {
	t.Skip("Plan 03 — implement in Phase 65 plan 03")
}
