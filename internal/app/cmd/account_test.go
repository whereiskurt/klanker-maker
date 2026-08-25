// Package cmd_test provides tests for the km account subcommand (Phase 126 Plan 05).
package cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"gopkg.in/yaml.v3"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ======================== Test doubles ==========================================

// mockAccountRunner is a test double for the AccountRunner interface declared
// in account.go. Optional Log lets ordering-sensitive tests interleave its
// calls with mockLinkStateS3/mockLinkLockDynamoDB calls into one timeline.
type mockAccountRunner struct {
	Log *[]string

	PlanCalled          bool
	Applied             []string
	InitNoBackendCalled bool
	ValidateCalled      bool
	OutputCalled        bool
	OutputResult        map[string]interface{}

	PlanErr, ApplyErr, OutputErr, InitNoBackendErr, ValidateErr error
}

func (m *mockAccountRunner) Plan(_ context.Context, _ string) error {
	m.PlanCalled = true
	if m.Log != nil {
		*m.Log = append(*m.Log, "plan")
	}
	return m.PlanErr
}

func (m *mockAccountRunner) Apply(_ context.Context, dir string) error {
	if m.ApplyErr != nil {
		return m.ApplyErr
	}
	m.Applied = append(m.Applied, dir)
	if m.Log != nil {
		*m.Log = append(*m.Log, "apply")
	}
	return nil
}

func (m *mockAccountRunner) Destroy(_ context.Context, _ string) error { return nil }

func (m *mockAccountRunner) Output(_ context.Context, _ string) (map[string]interface{}, error) {
	m.OutputCalled = true
	if m.OutputErr != nil {
		return nil, m.OutputErr
	}
	if m.OutputResult != nil {
		return m.OutputResult, nil
	}
	return defaultAccountModuleOutputs(), nil
}

func (m *mockAccountRunner) InitNoBackend(_ context.Context, _ string) error {
	m.InitNoBackendCalled = true
	if m.Log != nil {
		*m.Log = append(*m.Log, "init-no-backend")
	}
	return m.InitNoBackendErr
}

func (m *mockAccountRunner) Validate(_ context.Context, _ string) error {
	m.ValidateCalled = true
	if m.Log != nil {
		*m.Log = append(*m.Log, "validate")
	}
	return m.ValidateErr
}

func defaultAccountModuleOutputs() map[string]interface{} {
	wrap := func(v interface{}) map[string]interface{} { return map[string]interface{}{"value": v} }
	return map[string]interface{}{
		"launcher_role_arn":  wrap("arn:aws:iam::555566667777:role/km-gpu-launcher"),
		"box_role_arn":       wrap("arn:aws:iam::555566667777:role/km-gpu-box"),
		"subnet_ids":         wrap([]interface{}{"subnet-1", "subnet-2"}),
		"availability_zones": wrap([]interface{}{"us-east-1a", "us-east-1b"}),
		"security_group_id":  wrap("sg-0123"),
		"results_bucket":     wrap("km-results-555566667777"),
		"efs_id":             wrap(""),
		"account_id":         wrap("555566667777"),
		"region":             wrap("us-east-1"),
	}
}

// mockLinkStateS3 is a test double for LinkStateS3API.
type mockLinkStateS3 struct {
	Log *[]string

	HeadErr error // returned by HeadBucket every call

	CreateCalled bool
	CreateInput  *s3.CreateBucketInput

	VersioningCalls int
	PABCalls        int
	PABConfig       *s3types.PublicAccessBlockConfiguration
	EncryptionCalls int

	FailIfCalled *testing.T // when set, any method call fails the test immediately
}

func (m *mockLinkStateS3) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkStateS3.HeadBucket should not have been called")
	}
	if m.HeadErr != nil {
		return nil, m.HeadErr
	}
	return &s3.HeadBucketOutput{}, nil
}

func (m *mockLinkStateS3) CreateBucket(_ context.Context, in *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkStateS3.CreateBucket should not have been called")
	}
	m.CreateCalled = true
	m.CreateInput = in
	if m.Log != nil {
		*m.Log = append(*m.Log, "s3-create-bucket")
	}
	return &s3.CreateBucketOutput{}, nil
}

func (m *mockLinkStateS3) PutBucketVersioning(_ context.Context, _ *s3.PutBucketVersioningInput, _ ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkStateS3.PutBucketVersioning should not have been called")
	}
	m.VersioningCalls++
	return &s3.PutBucketVersioningOutput{}, nil
}

func (m *mockLinkStateS3) PutPublicAccessBlock(_ context.Context, in *s3.PutPublicAccessBlockInput, _ ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkStateS3.PutPublicAccessBlock should not have been called")
	}
	m.PABCalls++
	m.PABConfig = in.PublicAccessBlockConfiguration
	return &s3.PutPublicAccessBlockOutput{}, nil
}

func (m *mockLinkStateS3) PutBucketEncryption(_ context.Context, _ *s3.PutBucketEncryptionInput, _ ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkStateS3.PutBucketEncryption should not have been called")
	}
	m.EncryptionCalls++
	return &s3.PutBucketEncryptionOutput{}, nil
}

// mockLinkLockDynamoDB is a test double for LinkLockDynamoDBAPI.
type mockLinkLockDynamoDB struct {
	Log *[]string

	NotFoundFirst bool                   // first DescribeTable call returns ResourceNotFoundException
	Statuses      []ddbtypes.TableStatus // status sequence for calls after the not-found call (or from call 0 if !NotFoundFirst); last value repeats
	calls         int
	CreateCalled  bool

	FailIfCalled *testing.T
}

func (m *mockLinkLockDynamoDB) DescribeTable(_ context.Context, _ *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkLockDynamoDB.DescribeTable should not have been called")
	}
	call := m.calls
	m.calls++
	if m.NotFoundFirst && call == 0 {
		return nil, &ddbtypes.ResourceNotFoundException{Message: aws.String("table not found")}
	}
	idx := call
	if m.NotFoundFirst {
		idx = call - 1
	}
	status := ddbtypes.TableStatusActive
	if len(m.Statuses) > 0 {
		if idx >= len(m.Statuses) {
			idx = len(m.Statuses) - 1
		}
		if idx < 0 {
			idx = 0
		}
		status = m.Statuses[idx]
	}
	return &dynamodb.DescribeTableOutput{Table: &ddbtypes.TableDescription{TableStatus: status}}, nil
}

func (m *mockLinkLockDynamoDB) CreateTable(_ context.Context, _ *dynamodb.CreateTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error) {
	if m.FailIfCalled != nil {
		m.FailIfCalled.Fatal("mockLinkLockDynamoDB.CreateTable should not have been called")
	}
	m.CreateCalled = true
	if m.Log != nil {
		*m.Log = append(*m.Log, "ddb-create-table")
	}
	return &dynamodb.CreateTableOutput{}, nil
}

// installAccountSeams overrides every AWS-touching seam account.go exposes
// and registers t.Cleanup restores. Individual tests further customize the
// returned mocks before calling RunAccountAdd.
func installAccountSeams(t *testing.T, runner *mockAccountRunner, s3c *mockLinkStateS3, ddbc *mockLinkLockDynamoDB, caller cmd.AccountCallerIdentity) {
	t.Helper()

	origRunner := cmd.NewAccountRunnerFunc
	cmd.NewAccountRunnerFunc = func(_, _ string) cmd.AccountRunner { return runner }
	t.Cleanup(func() { cmd.NewAccountRunnerFunc = origRunner })

	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })

	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })

	origCaller := cmd.NewAccountCallerIdentityFunc
	cmd.NewAccountCallerIdentityFunc = func(_ context.Context, _ aws.Config) (cmd.AccountCallerIdentity, error) {
		return caller, nil
	}
	t.Cleanup(func() { cmd.NewAccountCallerIdentityFunc = origCaller })

	origPoll := cmd.LinkLockTablePollInterval
	cmd.LinkLockTablePollInterval = time.Millisecond
	t.Cleanup(func() { cmd.LinkLockTablePollInterval = origPoll })
}

func baseAccountAddOpts(name string) cmd.AccountAddOpts {
	return cmd.AccountAddOpts{
		Name:           name,
		TrustAccountID: "111111111111",
		Region:         "us-east-1",
		InstanceTypes:  []string{"g6e.12xlarge"},
		AWSProfile:     "target-admin",
		DryRun:         true,
	}
}

// testCaller is a plain (non-SSO) assumed-role caller — the case that IS
// re-homeable into the trust account. Used as the default fixture so tests
// unrelated to principal derivation are not entangled with it.
var testCaller = cmd.AccountCallerIdentity{
	AccountID: "222222222222",
	ARN:       "arn:aws:sts::222222222222:assumed-role/OrgAdmin/operator@example.com",
}

// testCallerSSO is an IAM Identity Center caller. Its role name carries a
// per-account hash and lives under /aws-reserved/sso.amazonaws.com/, so it
// can never be re-homed into another account — enrollment must reject it and
// demand an explicit --trust-principal.
var testCallerSSO = cmd.AccountCallerIdentity{
	AccountID: "222222222222",
	ARN:       "arn:aws:sts::222222222222:assumed-role/AWSReservedSSO_Admin_574bfc731f4810d3/operator@example.com",
}

// ======================== Task 1: HCL generation ================================

func TestGenerateAccountLinkHCL(t *testing.T) {
	p := cmd.AccountLinkHCLParams{
		Name:                        "mgmt-gpu",
		ModuleSource:                "/repo/infra/modules/gpu-launcher-account/v1.0.0",
		StateBucket:                 cmd.LinkStateBucketName("km", "111111111111", "use1"),
		StateKey:                    cmd.LinkStateKey("mgmt-gpu"),
		LockTable:                   cmd.LinkLockTableName("km", "use1"),
		Region:                      "us-east-1",
		ResourcePrefix:              "km",
		TrustAccountID:              "111111111111",
		TrustedPrincipalARNs:        []string{"arn:aws:iam::111111111111:role/km-create-handler", "arn:aws:iam::111111111111:role/km-ttl-handler"},
		TrustedPrincipalARNPatterns: []string{"arn:aws:iam::111111111111:role/AWSReservedSSO_*"},
		ExternalID:                  "deadbeef",
		InstanceTypes:               []string{"g6e.12xlarge", "g6e.48xlarge"},
		ProvisionNetwork:            true,
		AZCount:                     3,
		ProvisionEFS:                true,
		EnableBedrock:               true,
	}
	hcl := cmd.GenerateAccountLinkHCL(p)

	if strings.Contains(hcl, `include "root"`) {
		t.Error("generated unit must not include the root config")
	}
	if !strings.Contains(hcl, `backend = "s3"`) {
		t.Error("generated unit must declare an S3 remote_state backend")
	}
	if strings.Contains(hcl, `backend "local"`) {
		t.Error("generated unit must not declare a local backend")
	}
	for _, want := range []string{p.StateBucket, p.StateKey, p.LockTable, p.ModuleSource, p.TrustAccountID, p.ExternalID} {
		if !strings.Contains(hcl, want) {
			t.Errorf("rendered HCL missing expected value %q", want)
		}
	}
	// No residual {PLACEHOLDER} markers.
	if m := regexp.MustCompile(`\{[A-Z_]+\}`).FindString(hcl); m != "" {
		t.Errorf("rendered HCL still contains an unconsumed placeholder: %s", m)
	}
}

// ======================== Task 2: EnsureLinkStateBackend =========================

func TestEnsureLinkStateBackend_CreatesWhenAbsent(t *testing.T) {
	s3c := &mockLinkStateS3{HeadErr: &smithy.GenericAPIError{Code: "NotFound"}}
	ddbc := &mockLinkLockDynamoDB{NotFoundFirst: true}

	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })
	origPoll := cmd.LinkLockTablePollInterval
	cmd.LinkLockTablePollInterval = time.Millisecond
	t.Cleanup(func() { cmd.LinkLockTablePollInterval = origPoll })

	awsCfg := aws.Config{Region: "us-east-1"}
	bucket, table, key, err := cmd.EnsureLinkStateBackend(context.Background(), awsCfg, "km", "111111111111", "use1", "mgmt-gpu", "", "")
	if err != nil {
		t.Fatalf("EnsureLinkStateBackend: %v", err)
	}
	if bucket != cmd.LinkStateBucketName("km", "111111111111", "use1") {
		t.Errorf("bucket = %q", bucket)
	}
	if table != cmd.LinkLockTableName("km", "use1") {
		t.Errorf("table = %q", table)
	}
	if key != cmd.LinkStateKey("mgmt-gpu") {
		t.Errorf("key = %q", key)
	}
	if !s3c.CreateCalled {
		t.Error("expected CreateBucket to be called")
	}
	if !ddbc.CreateCalled {
		t.Error("expected CreateTable to be called")
	}
	if s3c.VersioningCalls != 1 || s3c.PABCalls != 1 || s3c.EncryptionCalls != 1 {
		t.Errorf("expected each control call exactly once, got versioning=%d pab=%d encryption=%d",
			s3c.VersioningCalls, s3c.PABCalls, s3c.EncryptionCalls)
	}
	if s3c.PABConfig == nil {
		t.Fatal("PutPublicAccessBlock config not captured")
	}
	if !aws.ToBool(s3c.PABConfig.BlockPublicAcls) || !aws.ToBool(s3c.PABConfig.BlockPublicPolicy) ||
		!aws.ToBool(s3c.PABConfig.IgnorePublicAcls) || !aws.ToBool(s3c.PABConfig.RestrictPublicBuckets) {
		t.Error("all four public-access-block settings must be true")
	}
}

func TestEnsureLinkStateBackend_ReconcilesWhenAlreadyExists(t *testing.T) {
	// Test 2 + Test 2b: bucket/table already exist, but the function still
	// reconciles (re-applies) the controls rather than early-returning.
	s3c := &mockLinkStateS3{} // HeadErr nil => exists
	ddbc := &mockLinkLockDynamoDB{}

	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })

	awsCfg := aws.Config{Region: "us-east-1"}
	bucket, table, _, err := cmd.EnsureLinkStateBackend(context.Background(), awsCfg, "km", "111111111111", "use1", "mgmt-gpu", "", "")
	if err != nil {
		t.Fatalf("EnsureLinkStateBackend: %v", err)
	}
	if bucket == "" || table == "" {
		t.Fatal("expected non-empty resolved names")
	}
	if s3c.CreateCalled {
		t.Error("bucket already existed; CreateBucket must not be called")
	}
	if ddbc.CreateCalled {
		t.Error("table already existed; CreateTable must not be called")
	}
	// Reconciliation: controls applied even though nothing was created.
	if s3c.VersioningCalls != 1 || s3c.PABCalls != 1 || s3c.EncryptionCalls != 1 {
		t.Errorf("expected controls reconciled against the pre-existing bucket, got versioning=%d pab=%d encryption=%d",
			s3c.VersioningCalls, s3c.PABCalls, s3c.EncryptionCalls)
	}
}

func TestEnsureLinkStateBackend_LocationConstraint(t *testing.T) {
	t.Run("us-east-1 omits LocationConstraint", func(t *testing.T) {
		s3c := &mockLinkStateS3{HeadErr: &smithy.GenericAPIError{Code: "NotFound"}}
		ddbc := &mockLinkLockDynamoDB{NotFoundFirst: true}
		origS3 := cmd.NewLinkStateS3Client
		cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
		t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
		origDDB := cmd.NewLinkLockDynamoDBClient
		cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
		t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })
		origPoll := cmd.LinkLockTablePollInterval
		cmd.LinkLockTablePollInterval = time.Millisecond
		t.Cleanup(func() { cmd.LinkLockTablePollInterval = origPoll })

		_, _, _, err := cmd.EnsureLinkStateBackend(context.Background(), aws.Config{Region: "us-east-1"}, "km", "111111111111", "use1", "mgmt-gpu", "", "")
		if err != nil {
			t.Fatalf("EnsureLinkStateBackend: %v", err)
		}
		if s3c.CreateInput.CreateBucketConfiguration != nil {
			t.Error("us-east-1 must omit CreateBucketConfiguration")
		}
	})

	t.Run("other region includes LocationConstraint", func(t *testing.T) {
		s3c := &mockLinkStateS3{HeadErr: &smithy.GenericAPIError{Code: "NotFound"}}
		ddbc := &mockLinkLockDynamoDB{NotFoundFirst: true}
		origS3 := cmd.NewLinkStateS3Client
		cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
		t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
		origDDB := cmd.NewLinkLockDynamoDBClient
		cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
		t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })
		origPoll := cmd.LinkLockTablePollInterval
		cmd.LinkLockTablePollInterval = time.Millisecond
		t.Cleanup(func() { cmd.LinkLockTablePollInterval = origPoll })

		_, _, _, err := cmd.EnsureLinkStateBackend(context.Background(), aws.Config{Region: "us-west-2"}, "km", "111111111111", "usw2", "mgmt-gpu", "", "")
		if err != nil {
			t.Fatalf("EnsureLinkStateBackend: %v", err)
		}
		if s3c.CreateInput.CreateBucketConfiguration == nil ||
			string(s3c.CreateInput.CreateBucketConfiguration.LocationConstraint) != "us-west-2" {
			t.Error("expected LocationConstraint=us-west-2")
		}
	})
}

func TestEnsureLinkStateBackend_NameOverrides(t *testing.T) {
	s3c := &mockLinkStateS3{}
	ddbc := &mockLinkLockDynamoDB{}
	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })

	bucket, table, _, err := cmd.EnsureLinkStateBackend(context.Background(), aws.Config{Region: "us-east-1"}, "km", "111111111111", "use1", "mgmt-gpu", "custom-bucket", "custom-table")
	if err != nil {
		t.Fatalf("EnsureLinkStateBackend: %v", err)
	}
	if bucket != "custom-bucket" || table != "custom-table" {
		t.Errorf("overrides not honoured: bucket=%q table=%q", bucket, table)
	}
}

func TestEnsureLinkStateBackend_WaitsForActive(t *testing.T) {
	ddbc := &mockLinkLockDynamoDB{
		Statuses: []ddbtypes.TableStatus{ddbtypes.TableStatusCreating, ddbtypes.TableStatusCreating, ddbtypes.TableStatusActive},
	}
	s3c := &mockLinkStateS3{}
	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })
	origPoll := cmd.LinkLockTablePollInterval
	cmd.LinkLockTablePollInterval = time.Millisecond
	t.Cleanup(func() { cmd.LinkLockTablePollInterval = origPoll })

	_, _, _, err := cmd.EnsureLinkStateBackend(context.Background(), aws.Config{Region: "us-east-1"}, "km", "111111111111", "use1", "mgmt-gpu", "", "")
	if err != nil {
		t.Fatalf("EnsureLinkStateBackend: %v", err)
	}
	if ddbc.calls < 3 {
		t.Errorf("expected at least 3 DescribeTable polls before ACTIVE, got %d", ddbc.calls)
	}
}

func TestEnsureLinkStateBackend_AccessDeniedIsHardError(t *testing.T) {
	s3c := &mockLinkStateS3{HeadErr: &smithy.GenericAPIError{Code: "Forbidden"}}
	ddbc := &mockLinkLockDynamoDB{}
	origS3 := cmd.NewLinkStateS3Client
	cmd.NewLinkStateS3Client = func(_ aws.Config) cmd.LinkStateS3API { return s3c }
	t.Cleanup(func() { cmd.NewLinkStateS3Client = origS3 })
	origDDB := cmd.NewLinkLockDynamoDBClient
	cmd.NewLinkLockDynamoDBClient = func(_ aws.Config) cmd.LinkLockDynamoDBAPI { return ddbc }
	t.Cleanup(func() { cmd.NewLinkLockDynamoDBClient = origDDB })

	bucket := cmd.LinkStateBucketName("km", "111111111111", "use1")
	_, _, _, err := cmd.EnsureLinkStateBackend(context.Background(), aws.Config{Region: "us-east-1"}, "km", "111111111111", "use1", "mgmt-gpu", "", "")
	if err == nil {
		t.Fatal("expected an error on access-denied HeadBucket")
	}
	if !strings.Contains(err.Error(), bucket) {
		t.Errorf("error must name the bucket %q, got: %v", bucket, err)
	}
	if s3c.CreateCalled {
		t.Error("access-denied must never be treated as \"already exists\" — CreateBucket must not be called")
	}
	t.Logf("access-denied error message (captured for SUMMARY): %v", err)
}

// ======================== Task 3: RunAccountAdd ==================================

func TestAccountAdd_ExternalID(t *testing.T) {
	t.Run("minted when absent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		var extIDs []string
		for i, n := range []string{"link-a", "link-b"} {
			runner := &mockAccountRunner{}
			installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
			cfg := &config.Config{}
			opts := baseAccountAddOpts(n)
			opts.DryRun = true
			if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
			hcl := readUnitHCL(t, home, n)
			extIDs = append(extIDs, extractExternalID(t, hcl))
		}
		if len(extIDs[0]) < 32 {
			t.Errorf("minted external id too short: %d chars", len(extIDs[0]))
		}
		if extIDs[0] == extIDs[1] {
			t.Error("minted external ids must differ across invocations")
		}
	})

	t.Run("explicit value used verbatim", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		runner := &mockAccountRunner{}
		installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
		cfg := &config.Config{}
		opts := baseAccountAddOpts("link-explicit")
		opts.DryRun = true
		opts.ExternalID = "explicit-external-id-0123456789"
		if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
			t.Fatalf("RunAccountAdd: %v", err)
		}
		hcl := readUnitHCL(t, home, "link-explicit")
		if got := extractExternalID(t, hcl); got != opts.ExternalID {
			t.Errorf("external_id = %q, want %q", got, opts.ExternalID)
		}
	})
}

// testCaller is an SSO caller, which can NEVER be re-homed into the trust
// account (per-account name hash + /aws-reserved/sso.amazonaws.com/ path).
// Enrollment must fail fast and name the fix rather than emitting an ARN that
// IAM rejects at CreateRole with MalformedPolicyDocument.
func TestAccountAdd_TrustedPrincipals_SSOCallerIsRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCallerSSO)
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true

	err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{})
	if err == nil {
		t.Fatal("RunAccountAdd must fail when the caller is an SSO role and no --trust-principal is given")
	}
	// The message has to be actionable — it is the operator's only signal.
	for _, want := range []string{"--trust-principal", "aws-reserved/sso.amazonaws.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must mention %q, got:\n%s", want, err.Error())
		}
	}
	// A guessed SSO ARN must never reach the rendered unit.
	if strings.Contains(err.Error(), "arn:aws:iam::111111111111:role/AWSReservedSSO_Admin") {
		t.Error("must not emit a re-homed SSO ARN — its per-account hash cannot resolve in the trust account")
	}
}

// A non-SSO assumed-role caller IS re-homeable and must still work.
func TestAccountAdd_TrustedPrincipals_NonSSOCallerIsDerived(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, cmd.AccountCallerIdentity{
		AccountID: "222222222222",
		ARN:       "arn:aws:sts::222222222222:assumed-role/OrgAdmin/operator@example.com",
	})
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}
	hcl := readUnitHCL(t, home, "mgmt-gpu")
	for _, want := range []string{
		"arn:aws:iam::111111111111:role/km-create-handler",
		"arn:aws:iam::111111111111:role/km-ttl-handler",
		"arn:aws:iam::111111111111:role/OrgAdmin", // re-homed from the caller
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("trusted_principal_arns missing %q\nHCL:\n%s", want, hcl)
		}
	}
}

// An SSO caller WITH --trust-principal is the documented escape hatch and
// must succeed — the derivation is never consulted.
func TestAccountAdd_TrustedPrincipals_SSOCallerWithExplicitPrincipal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCallerSSO)
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true
	realARN := "arn:aws:iam::111111111111:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_Admin_024532ccbde75573"
	opts.TrustPrincipals = []string{realARN}

	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd with explicit --trust-principal: %v", err)
	}
	hcl := readUnitHCL(t, home, "mgmt-gpu")
	if !strings.Contains(hcl, realARN) {
		t.Errorf("explicit --trust-principal missing from trusted_principal_arns\nHCL:\n%s", hcl)
	}
}

// The link-state bucket is named after the TARGET account, never the home
// account. S3 names are global, so a home-id name collides across every
// target enrolled from one home — and desynchronises `add` from `register`,
// which already derives from the target id.
func TestAccountAdd_StateBucketNamedForTargetAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, cmd.AccountCallerIdentity{
		AccountID: "222222222222",
		ARN:       "arn:aws:sts::222222222222:assumed-role/OrgAdmin/operator@example.com",
	})
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}
	hcl := readUnitHCL(t, home, "mgmt-gpu")

	// 222222222222 is the target (caller) account; 111111111111 is the home
	// account passed as --trust.
	wantBucket := cmd.LinkStateBucketName("km", "222222222222", "use1")
	if !strings.Contains(hcl, wantBucket) {
		t.Errorf("state bucket must be named for the TARGET account, want %q\nHCL:\n%s", wantBucket, hcl)
	}
	badBucket := cmd.LinkStateBucketName("km", "111111111111", "use1")
	if strings.Contains(hcl, badBucket) {
		t.Errorf("state bucket must NOT be named for the home/trust account (%q) — "+
			"S3 names are global and every target from this home would collide", badBucket)
	}
}

func TestAccountAdd_TrustPrincipalFlags(t *testing.T) {
	t.Run("--trust-principal replaces the derived entry, not appends", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		runner := &mockAccountRunner{}
		installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
		cfg := &config.Config{}
		opts := baseAccountAddOpts("mgmt-gpu")
		opts.DryRun = true
		opts.TrustPrincipals = []string{"arn:aws:iam::111111111111:role/custom-operator"}
		if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
			t.Fatalf("RunAccountAdd: %v", err)
		}
		hcl := readUnitHCL(t, home, "mgmt-gpu")
		list := extractHCLList(t, hcl, "trusted_principal_arns")
		if len(list) != 3 {
			t.Fatalf("trusted_principal_arns: got %d entries, want 3 (create-handler, ttl-handler, custom): %v", len(list), list)
		}
		if strings.Contains(hcl, "AWSReservedSSO_Admin") {
			t.Error("derived caller principal must be replaced, not appended, when --trust-principal is given")
		}
		found := false
		for _, v := range list {
			if v == "arn:aws:iam::111111111111:role/custom-operator" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected explicit --trust-principal in list: %v", list)
		}
	})

	t.Run("--trust-principal-pattern populates the pattern variable", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		runner := &mockAccountRunner{}
		installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
		cfg := &config.Config{}
		opts := baseAccountAddOpts("mgmt-gpu")
		opts.DryRun = true
		opts.TrustPrincipalPatterns = []string{"arn:aws:iam::111111111111:role/AWSReservedSSO_*"}
		if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
			t.Fatalf("RunAccountAdd: %v", err)
		}
		hcl := readUnitHCL(t, home, "mgmt-gpu")
		list := extractHCLList(t, hcl, "trusted_principal_arn_patterns")
		if len(list) != 1 || list[0] != opts.TrustPrincipalPatterns[0] {
			t.Errorf("trusted_principal_arn_patterns = %v, want [%q]", list, opts.TrustPrincipalPatterns[0])
		}
	})
}

func TestAccountAdd_DryRunVsRealRun_RunnerCalls(t *testing.T) {
	t.Run("dry-run: InitNoBackend + Validate, never Plan/Apply", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		runner := &mockAccountRunner{}
		s3c := &mockLinkStateS3{FailIfCalled: t}
		ddbc := &mockLinkLockDynamoDB{FailIfCalled: t}
		installAccountSeams(t, runner, s3c, ddbc, testCaller)
		cfg := &config.Config{}
		opts := baseAccountAddOpts("mgmt-gpu")
		opts.DryRun = true
		if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
			t.Fatalf("RunAccountAdd: %v", err)
		}
		if !runner.InitNoBackendCalled || !runner.ValidateCalled {
			t.Error("expected InitNoBackend + Validate to be called on dry-run")
		}
		if runner.PlanCalled || len(runner.Applied) != 0 {
			t.Error("dry-run must never call Plan or Apply")
		}
	})

	t.Run("real run: Plan then Apply exactly once", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		runner := &mockAccountRunner{}
		installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
		cfg := &config.Config{}
		opts := baseAccountAddOpts("mgmt-gpu")
		opts.DryRun = false
		if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
			t.Fatalf("RunAccountAdd: %v", err)
		}
		if !runner.PlanCalled {
			t.Error("expected Plan to be called before Apply on a real run")
		}
		if len(runner.Applied) != 1 {
			t.Errorf("expected exactly 1 Apply call, got %d", len(runner.Applied))
		}
		if runner.InitNoBackendCalled || runner.ValidateCalled {
			t.Error("real run must not take the dry-run validation path")
		}
	})
}

func TestAccountAdd_LinkFragment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = false
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}

	fragPath := filepath.Join(home, ".km", "account-links", "mgmt-gpu.link.yaml")
	info, err := os.Stat(fragPath)
	if err != nil {
		t.Fatalf("link fragment not found: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("link fragment mode = %o, want 0600", perm)
	}
	data, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	var frag cmd.AccountLinkFragment
	if err := yaml.Unmarshal(data, &frag); err != nil {
		t.Fatalf("unmarshal link fragment: %v", err)
	}
	if frag.LauncherRoleARN == "" || frag.BoxRoleARN == "" || frag.ExternalID == "" {
		t.Errorf("link fragment missing required fields: %+v", frag)
	}
	// Test 11: state bucket/lock table/state key are carried.
	if frag.StateBucket == "" || frag.LockTable == "" || frag.StateKey == "" {
		t.Errorf("link fragment missing state backend fields: %+v", frag)
	}

	// No local Terraform state anywhere.
	unitDir := filepath.Join(home, ".km", "account-links", "mgmt-gpu")
	if _, err := os.Stat(filepath.Join(unitDir, "terraform.tfstate")); !os.IsNotExist(err) {
		t.Errorf("unit directory must contain no terraform.tfstate; stat err = %v", err)
	}
}

func TestAccountAdd_Idempotent(t *testing.T) {
	runner := &mockAccountRunner{}
	s3c := &mockLinkStateS3{FailIfCalled: t}
	ddbc := &mockLinkLockDynamoDB{FailIfCalled: t}
	installAccountSeams(t, runner, s3c, ddbc, testCaller)
	origCaller := cmd.NewAccountCallerIdentityFunc
	cmd.NewAccountCallerIdentityFunc = func(_ context.Context, _ aws.Config) (cmd.AccountCallerIdentity, error) {
		t.Fatal("caller identity must not be resolved for an already-enrolled name")
		return cmd.AccountCallerIdentity{}, nil
	}
	t.Cleanup(func() { cmd.NewAccountCallerIdentityFunc = origCaller })

	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {AccountID: "111111111111", LauncherRoleARN: "arn:aws:iam::111111111111:role/km-gpu-launcher"},
		},
	}
	opts := baseAccountAddOpts("mgmt-gpu")
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd (idempotent path): %v", err)
	}
	if runner.PlanCalled || len(runner.Applied) != 0 {
		t.Error("idempotent path must never invoke the runner")
	}
}

func TestAccountAdd_ProvisionNetworkSubnetConflict(t *testing.T) {
	origCaller := cmd.NewAccountCallerIdentityFunc
	cmd.NewAccountCallerIdentityFunc = func(_ context.Context, _ aws.Config) (cmd.AccountCallerIdentity, error) {
		t.Fatal("no AWS call should happen before flag validation")
		return cmd.AccountCallerIdentity{}, nil
	}
	t.Cleanup(func() { cmd.NewAccountCallerIdentityFunc = origCaller })

	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.ProvisionNetwork = true
	opts.SubnetID = "subnet-abc123"
	err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive-flags error, got: %v", err)
	}
}

func TestAccountAdd_RefusesSelfEnrollment(t *testing.T) {
	// T-126-25: the resolved caller account id must not equal --trust.
	runner := &mockAccountRunner{}
	s3c := &mockLinkStateS3{FailIfCalled: t}
	ddbc := &mockLinkLockDynamoDB{FailIfCalled: t}
	selfCaller := cmd.AccountCallerIdentity{AccountID: "111111111111", ARN: "arn:aws:iam::111111111111:root"}
	installAccountSeams(t, runner, s3c, ddbc, selfCaller)

	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu") // TrustAccountID is also "111111111111"
	err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{})
	if err == nil {
		t.Fatal("expected an error when the resolved account id equals --trust")
	}
	if runner.PlanCalled || runner.InitNoBackendCalled {
		t.Error("must fail before any terragrunt invocation")
	}
}

func TestAccountAdd_BackendBootstrapOrdering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var log []string
	runner := &mockAccountRunner{Log: &log}
	s3c := &mockLinkStateS3{Log: &log, HeadErr: &smithy.GenericAPIError{Code: "NotFound"}}
	ddbc := &mockLinkLockDynamoDB{Log: &log, NotFoundFirst: true}
	installAccountSeams(t, runner, s3c, ddbc, testCaller)

	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = false
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &discardWriter{}); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}

	planIdx, bucketIdx := -1, -1
	for i, e := range log {
		if e == "s3-create-bucket" && bucketIdx == -1 {
			bucketIdx = i
		}
		if e == "plan" && planIdx == -1 {
			planIdx = i
		}
	}
	if bucketIdx == -1 || planIdx == -1 || bucketIdx > planIdx {
		t.Errorf("expected the backend bucket to be created before terragrunt plan; log = %v", log)
	}

	hcl := readUnitHCL(t, home, "mgmt-gpu")
	wantBucket := cmd.LinkStateBucketName("km", "222222222222", "use1")
	wantTable := cmd.LinkLockTableName("km", "use1")
	if !strings.Contains(hcl, wantBucket) || !strings.Contains(hcl, wantTable) {
		t.Errorf("rendered unit must name exactly the bucket/table EnsureLinkStateBackend returned (%s / %s)\nHCL:\n%s", wantBucket, wantTable, hcl)
	}
}

func TestAccountAdd_DryRunProvisionsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	s3c := &mockLinkStateS3{FailIfCalled: t}
	ddbc := &mockLinkLockDynamoDB{FailIfCalled: t}
	installAccountSeams(t, runner, s3c, ddbc, testCaller)

	var out strings.Builder
	cfg := &config.Config{}
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true
	if err := cmd.RunAccountAdd(cfg, opts, t.TempDir(), &out); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}

	wantBucket := cmd.LinkStateBucketName("km", "222222222222", "use1")
	wantTable := cmd.LinkLockTableName("km", "use1")
	msg := out.String()
	if !strings.Contains(msg, wantBucket) || !strings.Contains(msg, wantTable) {
		t.Errorf("dry-run output must name the bucket/table it WOULD create; got:\n%s", msg)
	}
	if !strings.Contains(msg, "resource-level plan is unavailable") {
		t.Errorf("dry-run output must state that a full resource plan is unavailable on this path; got:\n%s", msg)
	}
}

// ======================== test helpers ===========================================

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func readUnitHCL(t *testing.T, home, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".km", "account-links", name, "terragrunt.hcl"))
	if err != nil {
		t.Fatalf("reading generated unit HCL: %v", err)
	}
	return string(data)
}

var externalIDLineRE = regexp.MustCompile(`external_id\s*=\s*"([^"]*)"`)

func extractExternalID(t *testing.T, hcl string) string {
	t.Helper()
	m := externalIDLineRE.FindStringSubmatch(hcl)
	if m == nil {
		t.Fatalf("could not find external_id in rendered HCL:\n%s", hcl)
	}
	return m[1]
}

// extractHCLList parses `key = ["a", "b"]` out of the rendered HCL and
// returns the unquoted string elements.
func extractHCLList(t *testing.T, hcl, key string) []string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*=\s*\[([^\]]*)\]`)
	m := re.FindStringSubmatch(hcl)
	if m == nil {
		t.Fatalf("could not find %q list in rendered HCL:\n%s", key, hcl)
	}
	inner := strings.TrimSpace(m[1])
	if inner == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(inner, ",") {
		out = append(out, strings.Trim(strings.TrimSpace(part), `"`))
	}
	return out
}
