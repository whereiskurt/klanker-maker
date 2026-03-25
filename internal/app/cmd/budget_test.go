package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Fake DynamoDB budget client ----

type fakeBudgetClient struct {
	computeLimit     float64
	aiLimit          float64
	warningThreshold float64
	updateItemCalls  []string
}

func newFakeBudgetClient(computeLimit, aiLimit, warningThreshold float64) *fakeBudgetClient {
	return &fakeBudgetClient{
		computeLimit:     computeLimit,
		aiLimit:          aiLimit,
		warningThreshold: warningThreshold,
	}
}

func (f *fakeBudgetClient) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if input.UpdateExpression != nil {
		f.updateItemCalls = append(f.updateItemCalls, *input.UpdateExpression)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeBudgetClient) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (f *fakeBudgetClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	items := []map[string]dynamodbtypes.AttributeValue{
		{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#test-sb"},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#limits"},
			"computeLimit": &dynamodbtypes.AttributeValueMemberN{
				Value: fmt.Sprintf("%f", f.computeLimit),
			},
			"aiLimit": &dynamodbtypes.AttributeValueMemberN{
				Value: fmt.Sprintf("%f", f.aiLimit),
			},
			"warningThreshold": &dynamodbtypes.AttributeValueMemberN{
				Value: fmt.Sprintf("%f", f.warningThreshold),
			},
		},
	}
	return &dynamodb.QueryOutput{Items: items, Count: int32(len(items))}, nil
}

// ---- Fake EC2 client ----

type fakeEC2StartAPI struct {
	instanceState ec2types.InstanceStateName
	startCalled   bool
}

func (f *fakeEC2StartAPI) StartInstances(_ context.Context, _ *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalled = true
	return &ec2.StartInstancesOutput{}, nil
}

func (f *fakeEC2StartAPI) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{
				Instances: []ec2types.Instance{
					{
						InstanceId: awssdk.String("i-0abc123"),
						State:      &ec2types.InstanceState{Name: f.instanceState},
					},
				},
			},
		},
	}, nil
}

// ---- Fake IAM client ----

type fakeIAMAttachAPI struct {
	attachedPolicies []string
	attachCalled     bool
}

func (f *fakeIAMAttachAPI) AttachRolePolicy(_ context.Context, _ *iam.AttachRolePolicyInput, _ ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
	f.attachCalled = true
	return &iam.AttachRolePolicyOutput{}, nil
}

func (f *fakeIAMAttachAPI) ListAttachedRolePolicies(_ context.Context, _ *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	var attached []iamtypes.AttachedPolicy
	for _, arn := range f.attachedPolicies {
		arnCopy := arn
		attached = append(attached, iamtypes.AttachedPolicy{PolicyArn: &arnCopy})
	}
	return &iam.ListAttachedRolePoliciesOutput{AttachedPolicies: attached}, nil
}

// ---- Fake sandbox metadata fetcher ----

type fakeSandboxMetaFetcher struct {
	meta *kmaws.SandboxMetadata
	err  error
}

func (f *fakeSandboxMetaFetcher) FetchSandboxMeta(_ context.Context, _ string) (*kmaws.SandboxMetadata, error) {
	return f.meta, f.err
}

// ---- Helper: run budget command ----

func runBudgetCmd(t *testing.T, budgetClient kmaws.BudgetAPI, ec2Client cmd.EC2StartAPI, iamClient cmd.IAMAttachAPI, metaFetcher cmd.SandboxMetaFetcher, args ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{BudgetTableName: "km-budgets"}
	root := &cobra.Command{Use: "km"}
	budgetCmd := cmd.NewBudgetCmdWithDeps(cfg, budgetClient, ec2Client, iamClient, metaFetcher)
	root.AddCommand(budgetCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"budget"}, args...))

	err := root.Execute()
	return buf.String(), err
}

// ---- Tests ----

// TestBudgetAdd_UpdatesDynamoDBLimits verifies budget add --compute --ai updates DynamoDB (additive)
func TestBudgetAdd_UpdatesDynamoDBLimits(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameRunning}
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{"arn:aws:iam::aws:policy/AmazonBedrockFullAccess"}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-001",
			Substrate: "ec2",
		},
	}

	out, err := runBudgetCmd(t, budgetClient, ec2Client, iamClient, metaFetcher,
		"add", "sb-001", "--compute", "2.00", "--ai", "3.00")
	if err != nil {
		t.Fatalf("budget add returned error: %v\noutput: %s", err, out)
	}

	// Must call SetBudgetLimits (UpdateItem)
	if len(budgetClient.updateItemCalls) == 0 {
		t.Error("expected UpdateItem to be called for new limits")
	}

	// Output must show updated budget
	if !strings.Contains(out, "Budget updated") {
		t.Errorf("expected 'Budget updated' in output, got:\n%s", out)
	}
}

// TestBudgetAdd_AIOnlyUpdate verifies that --ai only flag updates just the AI limit
func TestBudgetAdd_AIOnlyUpdate(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameRunning}
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{"arn:aws:iam::aws:policy/AmazonBedrockFullAccess"}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-002",
			Substrate: "ec2",
		},
	}

	out, err := runBudgetCmd(t, budgetClient, ec2Client, iamClient, metaFetcher,
		"add", "sb-002", "--ai", "5.00")
	if err != nil {
		t.Fatalf("budget add AI-only returned error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Budget updated") {
		t.Errorf("expected 'Budget updated' in output, got:\n%s", out)
	}
}

// TestBudgetAdd_ResumesStoppedEC2 verifies auto-resume calls StartInstances for stopped EC2
func TestBudgetAdd_ResumesStoppedEC2(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameStopped}
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{"arn:aws:iam::aws:policy/AmazonBedrockFullAccess"}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-003",
			Substrate: "ec2",
		},
	}

	out, err := runBudgetCmd(t, budgetClient, ec2Client, iamClient, metaFetcher,
		"add", "sb-003", "--compute", "2.00")
	if err != nil {
		t.Fatalf("budget add returned error: %v\noutput: %s", err, out)
	}

	if !ec2Client.startCalled {
		t.Error("expected StartInstances to be called for stopped EC2 instance")
	}

	if !strings.Contains(out, "resumed") {
		t.Errorf("expected 'resumed' in output, got:\n%s", out)
	}
}

// TestBudgetAdd_RestoresBedrockIAM verifies that missing Bedrock policy is re-attached
func TestBudgetAdd_RestoresBedrockIAM(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameRunning}
	// No Bedrock policy attached
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-004",
			Substrate: "ec2",
		},
	}

	out, err := runBudgetCmd(t, budgetClient, ec2Client, iamClient, metaFetcher,
		"add", "sb-004", "--ai", "5.00")
	if err != nil {
		t.Fatalf("budget add returned error: %v\noutput: %s", err, out)
	}

	if !iamClient.attachCalled {
		t.Error("expected AttachRolePolicy to be called to restore Bedrock policy")
	}
}

// TestBudgetAdd_RequiresSandboxID verifies error returned when sandbox-id is missing
func TestBudgetAdd_RequiresSandboxID(t *testing.T) {
	budgetClient := newFakeBudgetClient(0, 0, 0)
	ec2Client := &fakeEC2StartAPI{}
	iamClient := &fakeIAMAttachAPI{}
	metaFetcher := &fakeSandboxMetaFetcher{}

	_, err := runBudgetCmd(t, budgetClient, ec2Client, iamClient, metaFetcher, "add")
	if err == nil {
		t.Fatal("expected error when sandbox-id is missing, got nil")
	}
}

// TestBudgetAdd_ECSSubstrate verifies that when substrate=ecs, the budget add enters the
// ECS re-provisioning branch. Since reprovisionECSSandbox calls real AWS (compiler, terragrunt, S3),
// this test verifies the observable behaviour at the unit-test level: the budget update succeeds
// and the output contains an ECS-related message ("re-provision" or "artifact bucket").
// reprovisionECSSandbox is non-fatal on error, so budget update completes regardless.
func TestBudgetAdd_ECSSubstrate(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameRunning}
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{"arn:aws:iam::aws:policy/AmazonBedrockFullAccess"}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-ecs-001",
			Substrate: "ecs",
		},
	}

	// Set artifact bucket so the ECS branch is entered (not the missing-bucket branch)
	t.Setenv("KM_ARTIFACTS_BUCKET", "km-sandbox-artifacts-test")

	cfg := &config.Config{
		BudgetTableName: "km-budgets",
		ArtifactsBucket: "km-sandbox-artifacts-test",
	}
	root := &cobra.Command{Use: "km"}
	budgetCmd := cmd.NewBudgetCmdWithDeps(cfg, budgetClient, ec2Client, iamClient, metaFetcher)
	root.AddCommand(budgetCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"budget", "add", "sb-ecs-001", "--compute", "2.00"})

	err := root.Execute()
	out := buf.String()

	if err != nil {
		t.Fatalf("budget add for ECS returned unexpected error: %v\noutput: %s", err, out)
	}

	// Budget update must succeed
	if !strings.Contains(out, "Budget updated") {
		t.Errorf("expected 'Budget updated' in output, got:\n%s", out)
	}

	// The ECS branch must emit a message related to re-provisioning (warning on failure is expected
	// since no real AWS is available in unit tests)
	if !strings.Contains(out, "re-provision") && !strings.Contains(out, "artifact bucket") && !strings.Contains(out, "resumed") {
		t.Errorf("expected ECS re-provisioning message in output, got:\n%s", out)
	}
}

// TestBudgetAdd_ECSMissingArtifactBucket verifies that when substrate=ecs and ArtifactsBucket is
// empty, the output contains an actionable warning and the budget update still succeeds.
func TestBudgetAdd_ECSMissingArtifactBucket(t *testing.T) {
	budgetClient := newFakeBudgetClient(5.00, 10.00, 0.80)
	ec2Client := &fakeEC2StartAPI{instanceState: ec2types.InstanceStateNameRunning}
	iamClient := &fakeIAMAttachAPI{attachedPolicies: []string{"arn:aws:iam::aws:policy/AmazonBedrockFullAccess"}}
	metaFetcher := &fakeSandboxMetaFetcher{
		meta: &kmaws.SandboxMetadata{
			SandboxID: "sb-ecs-002",
			Substrate: "ecs",
		},
	}

	// Ensure KM_ARTIFACTS_BUCKET is not set so cfg.ArtifactsBucket is the sole source
	t.Setenv("KM_ARTIFACTS_BUCKET", "")

	cfg := &config.Config{
		BudgetTableName: "km-budgets",
		ArtifactsBucket: "", // empty — triggers the warning path
	}
	root := &cobra.Command{Use: "km"}
	budgetCmd := cmd.NewBudgetCmdWithDeps(cfg, budgetClient, ec2Client, iamClient, metaFetcher)
	root.AddCommand(budgetCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"budget", "add", "sb-ecs-002", "--compute", "2.00"})

	err := root.Execute()
	out := buf.String()

	if err != nil {
		t.Fatalf("budget add with empty artifact bucket returned unexpected error: %v\noutput: %s", err, out)
	}

	// Must emit actionable warning about missing artifact bucket
	if !strings.Contains(out, "artifact bucket not configured") {
		t.Errorf("expected 'artifact bucket not configured' warning in output, got:\n%s", out)
	}

	// Budget update must still succeed
	if !strings.Contains(out, "Budget updated") {
		t.Errorf("expected 'Budget updated' in output after artifact bucket warning, got:\n%s", out)
	}
}

// TestResumeEC2Sandbox_UsesCorrectTagKey verifies that resumeEC2Sandbox uses the correct
// tag key "tag:km:sandbox-id" (not "tag:sandbox-id") to filter EC2 instances.
// This is a source-level verification test following the Phase 07-02 pattern.
func TestResumeEC2Sandbox_UsesCorrectTagKey(t *testing.T) {
	src, err := os.ReadFile("budget.go")
	if err != nil {
		t.Fatalf("could not read budget.go: %v", err)
	}
	content := string(src)

	// Must contain the correct namespaced tag key
	if !strings.Contains(content, `tag:km:sandbox-id`) {
		t.Errorf("budget.go must use tag key %q in DescribeInstances filter, but it was not found", "tag:km:sandbox-id")
	}

	// Must NOT contain the broken tag key as a standalone string literal
	// (note: "tag:km:sandbox-id" contains "tag:sandbox-id" as a substring, so we
	// check for the exact broken Go string literal form)
	brokenPattern := `awssdk.String("tag:sandbox-id")`
	if strings.Contains(content, brokenPattern) {
		t.Errorf("budget.go contains broken tag key pattern %q — fix to use %q", brokenPattern, `awssdk.String("tag:km:sandbox-id")`)
	}
}

// TestBudgetAdd_ECSSourceLevelVerification verifies that budget.go contains the required
// ECS re-provisioning function and branch (following Phase 07-02 source-level verification pattern).
func TestBudgetAdd_ECSSourceLevelVerification(t *testing.T) {
	src, err := os.ReadFile("budget.go")
	if err != nil {
		t.Fatalf("could not read budget.go: %v", err)
	}
	content := string(src)

	checks := []struct {
		pattern string
		desc    string
	}{
		{`reprovisionECSSandbox`, "reprovisionECSSandbox function must exist"},
		{`substrate == "ecs"`, `ECS branch condition must exist`},
		{`compiler.Compile`, "compiler.Compile call must exist in ECS re-provisioning"},
	}

	for _, c := range checks {
		if !strings.Contains(content, c.pattern) {
			t.Errorf("budget.go missing %s: pattern %q not found", c.desc, c.pattern)
		}
	}
}
