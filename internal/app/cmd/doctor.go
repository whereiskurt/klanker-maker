package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	appcfg "github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// CheckStatus is the result classification for a single doctor check.
type CheckStatus string

const (
	CheckOK      CheckStatus = "OK"
	CheckWarn    CheckStatus = "WARN"
	CheckError   CheckStatus = "ERROR"
	CheckSkipped CheckStatus = "SKIPPED"
)

// Check output symbols — used in formatted (non-JSON) output.
const (
	checkOKSymbol      = "✓"
	checkWarnSymbol    = "⚠"
	checkErrorSymbol   = "✗"
	checkSkippedSymbol = "-"
)

// CheckResult is the result of a single platform health check.
type CheckResult struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Message     string      `json:"message"`
	Remediation string      `json:"remediation,omitempty"`
}

// =============================================================================
// DI interfaces — narrow, one method per service API surface used
// =============================================================================

// STSCallerAPI covers STS GetCallerIdentity.
type STSCallerAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// S3HeadBucketAPI covers S3 HeadBucket.
type S3HeadBucketAPI interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// DynamoDescribeAPI covers DynamoDB DescribeTable.
type DynamoDescribeAPI interface {
	DescribeTable(ctx context.Context, params *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
}

// KMSDescribeAPI covers KMS DescribeKey.
type KMSDescribeAPI interface {
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

// OrgsListPoliciesAPI covers Organizations ListPoliciesForTarget.
type OrgsListPoliciesAPI interface {
	ListPoliciesForTarget(ctx context.Context, params *organizations.ListPoliciesForTargetInput, optFns ...func(*organizations.Options)) (*organizations.ListPoliciesForTargetOutput, error)
}

// SSMReadAPI covers SSM GetParameter.
type SSMReadAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// EC2DescribeAPI covers EC2 DescribeVpcs and DescribeSubnets.
type EC2DescribeAPI interface {
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

// LambdaGetFunctionAPI covers Lambda GetFunction for existence check.
type LambdaGetFunctionAPI interface {
	GetFunction(ctx context.Context, params *lambda.GetFunctionInput, optFns ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
}

// SESGetEmailIdentityAPI covers SES GetEmailIdentity for domain verification check.
type SESGetEmailIdentityAPI interface {
	GetEmailIdentity(ctx context.Context, params *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
}

// DoctorConfigProvider abstracts config fields consumed by doctor checks.
// Both *config.Config (production) and test stubs implement this interface.
type DoctorConfigProvider interface {
	GetDomain() string
	GetManagementAccountID() string
	GetTerraformAccountID() string
	GetApplicationAccountID() string
	GetSSOStartURL() string
	GetPrimaryRegion() string
	GetStateBucket() string
	GetBudgetTableName() string
	GetIdentityTableName() string
	GetAWSProfile() string
}

// appConfigAdapter wraps *config.Config to satisfy DoctorConfigProvider.
type appConfigAdapter struct {
	cfg *appcfg.Config
}

func (a *appConfigAdapter) GetDomain() string              { return a.cfg.Domain }
func (a *appConfigAdapter) GetManagementAccountID() string  { return a.cfg.ManagementAccountID }
func (a *appConfigAdapter) GetTerraformAccountID() string   { return a.cfg.TerraformAccountID }
func (a *appConfigAdapter) GetApplicationAccountID() string { return a.cfg.ApplicationAccountID }
func (a *appConfigAdapter) GetSSOStartURL() string          { return a.cfg.SSOStartURL }
func (a *appConfigAdapter) GetPrimaryRegion() string        { return a.cfg.PrimaryRegion }
func (a *appConfigAdapter) GetStateBucket() string          { return a.cfg.StateBucket }
func (a *appConfigAdapter) GetBudgetTableName() string      { return a.cfg.BudgetTableName }
func (a *appConfigAdapter) GetIdentityTableName() string    { return a.cfg.IdentityTableName }
func (a *appConfigAdapter) GetAWSProfile() string           { return a.cfg.AWSProfile }

// DoctorDeps holds all injected AWS clients for doctor checks.
// Nil fields cause their corresponding checks to be skipped.
type DoctorDeps struct {
	STSClient     STSCallerAPI
	S3Client      S3HeadBucketAPI
	DynamoClient  DynamoDescribeAPI
	KMSClient     KMSDescribeAPI
	OrgsClient    OrgsListPoliciesAPI
	SSMReadClient SSMReadAPI
	// EC2Clients is a map from region name to EC2 client (one per region checked).
	EC2Clients map[string]EC2DescribeAPI
	// Lambda client for TTL handler existence check.
	LambdaClient LambdaGetFunctionAPI
	// SES client for domain verification check.
	SESClient SESGetEmailIdentityAPI
	// Lister for sandbox summary check.
	Lister SandboxLister
}

// =============================================================================
// Check functions — each returns CheckResult independently
// =============================================================================

// checkConfig verifies that required configuration fields are non-empty.
func checkConfig(cfg DoctorConfigProvider) CheckResult {
	type field struct {
		name  string
		value string
	}
	required := []field{
		{"domain", cfg.GetDomain()},
		{"management_account_id", cfg.GetManagementAccountID()},
		{"terraform_account_id", cfg.GetTerraformAccountID()},
		{"application_account_id", cfg.GetApplicationAccountID()},
		{"sso_start_url", cfg.GetSSOStartURL()},
		{"primary_region", cfg.GetPrimaryRegion()},
	}
	var missing []string
	for _, f := range required {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return CheckResult{
			Name:        "Config",
			Status:      CheckError,
			Message:     fmt.Sprintf("missing required config fields: %s", strings.Join(missing, ", ")),
			Remediation: "Run 'km configure' to set up platform configuration, or check km-config.yaml",
		}
	}
	return CheckResult{
		Name:    "Config",
		Status:  CheckOK,
		Message: fmt.Sprintf("domain=%s region=%s", cfg.GetDomain(), cfg.GetPrimaryRegion()),
	}
}

// checkCredential calls STS GetCallerIdentity to verify AWS credentials.
// Nil client returns CheckSkipped; error returns CheckError with sso login remediation.
func checkCredential(ctx context.Context, client STSCallerAPI, profile string) CheckResult {
	name := fmt.Sprintf("Credentials (%s)", profile)
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "no AWS client configured",
		}
	}
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("credential check failed: %v", err),
			Remediation: fmt.Sprintf("Run 'aws sso login --profile %s' to refresh credentials", profile),
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("authenticated as %s", awssdk.ToString(out.Arn)),
	}
}

// checkStateBucket verifies the S3 state bucket exists via HeadBucket.
// Empty bucket name or nil client returns CheckSkipped; error returns CheckError.
func checkStateBucket(ctx context.Context, client S3HeadBucketAPI, bucketName string) CheckResult {
	if bucketName == "" || client == nil {
		return CheckResult{
			Name:    "State Bucket",
			Status:  CheckSkipped,
			Message: "state bucket not configured (KM_STATE_BUCKET not set)",
		}
	}
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: awssdk.String(bucketName),
	})
	if err != nil {
		return CheckResult{
			Name:        "State Bucket",
			Status:      CheckError,
			Message:     fmt.Sprintf("bucket %q not found or not accessible: %v", bucketName, err),
			Remediation: "Run 'km bootstrap' to create the state bucket, or check your AWS credentials",
		}
	}
	return CheckResult{
		Name:    "State Bucket",
		Status:  CheckOK,
		Message: fmt.Sprintf("bucket %q is accessible", bucketName),
	}
}

// checkDynamoTable verifies a DynamoDB table exists via DescribeTable.
// Returns CheckError on missing or inaccessible table — callers may demote to CheckWarn.
func checkDynamoTable(ctx context.Context, client DynamoDescribeAPI, tableName, checkName string) CheckResult {
	if client == nil {
		return CheckResult{
			Name:    checkName,
			Status:  CheckSkipped,
			Message: "DynamoDB client not available",
		}
	}
	_, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		return CheckResult{
			Name:        checkName,
			Status:      CheckError,
			Message:     fmt.Sprintf("table %q not found or not accessible: %v", tableName, err),
			Remediation: "Run 'km bootstrap' to create DynamoDB tables",
		}
	}
	return CheckResult{
		Name:    checkName,
		Status:  CheckOK,
		Message: fmt.Sprintf("table %q exists", tableName),
	}
}

// checkKMSKey verifies a KMS key exists by alias.
// Uses alias/ prefix when calling DescribeKey.
func checkKMSKey(ctx context.Context, client KMSDescribeAPI, alias string) CheckResult {
	name := fmt.Sprintf("KMS Key (%s)", alias)
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "KMS client not available",
		}
	}
	keyID := "alias/" + alias
	_, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: awssdk.String(keyID),
	})
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("KMS key %q not found: %v", keyID, err),
			Remediation: "Run 'km bootstrap' to create the KMS key",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("key %q exists", keyID),
	}
}

// checkSCP checks that the km-sandbox-containment SCP is applied to the target account.
// Empty accountID or nil client returns CheckSkipped.
func checkSCP(ctx context.Context, client OrgsListPoliciesAPI, accountID string) CheckResult {
	if accountID == "" || client == nil {
		return CheckResult{
			Name:    "SCP (Sandbox Containment)",
			Status:  CheckSkipped,
			Message: "management account ID not configured or Organizations client unavailable",
		}
	}
	out, err := client.ListPoliciesForTarget(ctx, &organizations.ListPoliciesForTargetInput{
		TargetId: awssdk.String(accountID),
		Filter:   "SERVICE_CONTROL_POLICY",
	})
	if err != nil {
		return CheckResult{
			Name:        "SCP (Sandbox Containment)",
			Status:      CheckError,
			Message:     fmt.Sprintf("failed to list SCPs for account %s: %v", accountID, err),
			Remediation: "Check Organizations permissions or run the SCP Terraform module",
		}
	}
	const scpName = "km-sandbox-containment"
	for _, p := range out.Policies {
		if awssdk.ToString(p.Name) == scpName {
			return CheckResult{
				Name:    "SCP (Sandbox Containment)",
				Status:  CheckOK,
				Message: fmt.Sprintf("policy %q attached to account %s", scpName, accountID),
			}
		}
	}
	return CheckResult{
		Name:        "SCP (Sandbox Containment)",
		Status:      CheckError,
		Message:     fmt.Sprintf("policy %q not found on account %s", scpName, accountID),
		Remediation: "Apply the SCP Terraform module to attach km-sandbox-containment to the application account",
	}
}

// checkGitHubConfig verifies GitHub App config exists in SSM Parameter Store.
// Missing parameters returns CheckWarn (not ERROR) — GitHub integration is optional.
func checkGitHubConfig(ctx context.Context, client SSMReadAPI) CheckResult {
	const (
		appClientIDParam   = "/km/config/github/app-client-id"
		installationIDParam = "/km/config/github/installation-id"
	)
	for _, param := range []string{appClientIDParam, installationIDParam} {
		_, err := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name: awssdk.String(param),
		})
		if err != nil {
			var notFound *ssmtypes.ParameterNotFound
			if errors.As(err, &notFound) {
				return CheckResult{
					Name:        "GitHub App Config",
					Status:      CheckWarn,
					Message:     fmt.Sprintf("parameter %q not found — GitHub integration not configured", param),
					Remediation: "Run 'km configure github' to set up GitHub App integration",
				}
			}
			return CheckResult{
				Name:        "GitHub App Config",
				Status:      CheckWarn,
				Message:     fmt.Sprintf("could not read parameter %q: %v", param, err),
				Remediation: "Run 'km configure github' to set up GitHub App integration",
			}
		}
	}
	return CheckResult{
		Name:    "GitHub App Config",
		Status:  CheckOK,
		Message: "app-client-id and installation-id are configured",
	}
}

// checkRegionVPC verifies the km-managed VPC exists in the given region.
// Looks for VPCs with tag km:managed=true.
func checkRegionVPC(ctx context.Context, ec2Client EC2DescribeAPI, region string) CheckResult {
	name := fmt.Sprintf("VPC (%s)", region)
	out, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("failed to list VPCs in %s: %v", region, err),
			Remediation: "Check EC2 permissions or run the network Terraform module",
		}
	}
	if len(out.Vpcs) == 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("no km-managed VPC found in region %s", region),
			Remediation: "Apply the network Terragrunt module to create the VPC",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("found %d km-managed VPC(s) in %s", len(out.Vpcs), region),
	}
}

// checkSandboxSummary lists all running sandboxes and reports a count summary.
// Nil lister returns CheckSkipped.
func checkSandboxSummary(ctx context.Context, lister SandboxLister) CheckResult {
	if lister == nil {
		return CheckResult{
			Name:    "Active Sandboxes",
			Status:  CheckSkipped,
			Message: "sandbox lister not available (state bucket not configured)",
		}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{
			Name:    "Active Sandboxes",
			Status:  CheckWarn,
			Message: fmt.Sprintf("could not list sandboxes: %v", err),
		}
	}
	statusCounts := make(map[string]int)
	for _, r := range records {
		statusCounts[r.Status]++
	}
	parts := make([]string, 0, len(statusCounts))
	for status, count := range statusCounts {
		parts = append(parts, fmt.Sprintf("%s=%d", status, count))
	}
	sort.Strings(parts)
	msg := fmt.Sprintf("total=%d", len(records))
	if len(parts) > 0 {
		msg += " (" + strings.Join(parts, ", ") + ")"
	}
	return CheckResult{
		Name:    "Active Sandboxes",
		Status:  CheckOK,
		Message: msg,
	}
}

// checkLambdaFunction verifies the given Lambda function exists.
// Returns CheckSkipped when client is nil, CheckWarn when function is not found
// (ResourceNotFoundException), and CheckOK on success.
func checkLambdaFunction(ctx context.Context, client LambdaGetFunctionAPI, funcName string) CheckResult {
	name := fmt.Sprintf("Lambda (%s)", funcName)
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Lambda client not available",
		}
	}
	_, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: awssdk.String(funcName),
	})
	if err != nil {
		var notFound *lambdatypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return CheckResult{
				Name:        name,
				Status:      CheckWarn,
				Message:     fmt.Sprintf("function %q not found", funcName),
				Remediation: "Run 'km init' to deploy the TTL handler Lambda",
			}
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("could not check Lambda function %q: %v", funcName, err),
			Remediation: "Check Lambda permissions or run 'km init'",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("function %q deployed", funcName),
	}
}

// checkSESIdentity verifies the SES domain identity is verified.
// The identity checked is "sandboxes.{domain}" derived from cfg.GetDomain().
// Returns CheckSkipped when client is nil, CheckWarn when not found or not verified.
func checkSESIdentity(ctx context.Context, client SESGetEmailIdentityAPI, domain string) CheckResult {
	identity := fmt.Sprintf("sandboxes.%s", domain)
	name := "SES Domain Identity"
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "SES client not available",
		}
	}
	out, err := client.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{
		EmailIdentity: awssdk.String(identity),
	})
	if err != nil {
		var notFound *sesv2types.NotFoundException
		if errors.As(err, &notFound) {
			return CheckResult{
				Name:        name,
				Status:      CheckWarn,
				Message:     fmt.Sprintf("SES identity %q not configured", identity),
				Remediation: "Run 'km init' to configure SES domain identity",
			}
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("could not check SES identity %q: %v", identity, err),
			Remediation: "Check SES permissions or run 'km init'",
		}
	}
	if out.VerificationStatus != sesv2types.VerificationStatusSuccess {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("SES domain %q pending verification (status: %s)", identity, out.VerificationStatus),
			Remediation: "Add the DNS TXT record provided by AWS SES to complete domain verification",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("domain %q verified", identity),
	}
}

// =============================================================================
// Parallel execution helper
// =============================================================================

// runChecks executes all check functions in parallel and returns results sorted by Name.
func runChecks(ctx context.Context, checks []func(context.Context) CheckResult) []CheckResult {
	results := make([]CheckResult, 0, len(checks))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, check := range checks {
		wg.Add(1)
		go func(fn func(context.Context) CheckResult) {
			defer wg.Done()
			r := fn(ctx)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(check)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}

// isCredentialError returns true if the error message indicates an SSO/credential
// failure (expired token, invalid grant, etc.) rather than a permissions issue.
func isCredentialError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "refresh cached sso token") ||
		strings.Contains(lower, "invalidgrantexception") ||
		strings.Contains(lower, "sso token") ||
		strings.Contains(lower, "no credentials") ||
		strings.Contains(lower, "expired sso") ||
		strings.Contains(lower, "refresh cached credentials")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// =============================================================================
// Output formatting
// =============================================================================

// formatCheckLine returns a human-readable line for a check result.
// Colors are applied when isTTY is true. Remediation is printed on an indented line.
func formatCheckLine(r CheckResult, isTTY bool) string {
	var symbol, colorCode string
	switch r.Status {
	case CheckOK:
		symbol = checkOKSymbol
		colorCode = ansiGreen
	case CheckWarn:
		symbol = checkWarnSymbol
		colorCode = ansiYellow
	case CheckError:
		symbol = checkErrorSymbol
		colorCode = ansiRed
	default:
		symbol = checkSkippedSymbol
		colorCode = ""
	}

	var line string
	if isTTY && colorCode != "" {
		line = fmt.Sprintf("%s%s%s %-35s %s", colorCode, symbol, ansiReset, r.Name, r.Message)
	} else {
		line = fmt.Sprintf("%s %-35s %s", symbol, r.Name, r.Message)
	}
	if r.Remediation != "" {
		line += fmt.Sprintf("\n  → %s", r.Remediation)
	}
	return line
}

// filterNonOK returns only results with Status WARN or ERROR (not OK or Skipped).
func filterNonOK(results []CheckResult) []CheckResult {
	var out []CheckResult
	for _, r := range results {
		if r.Status == CheckWarn || r.Status == CheckError {
			out = append(out, r)
		}
	}
	return out
}

// =============================================================================
// Cobra command
// =============================================================================

// NewDoctorCmd creates the "km doctor" command using real AWS clients.
func NewDoctorCmd(cfg *appcfg.Config) *cobra.Command {
	return NewDoctorCmdWithDeps(cfg, nil)
}

// NewDoctorCmdWithDeps creates the "km doctor" command with injected dependencies.
// Pass nil deps for production use (real AWS clients will be initialized at run time).
// This overload is used in tests to inject mock AWS clients.
func NewDoctorCmdWithDeps(cfg interface{}, deps *DoctorDeps) *cobra.Command {
	var jsonOutput bool
	var quietMode bool

	cmd := &cobra.Command{
		Use:          "doctor",
		Short:        "Check platform health and bootstrap verification",
		Long:         helpText("doctor"),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var provider DoctorConfigProvider
			switch v := cfg.(type) {
			case *appcfg.Config:
				provider = &appConfigAdapter{cfg: v}
			case DoctorConfigProvider:
				provider = v
			default:
				return fmt.Errorf("unsupported config type %T", cfg)
			}
			return runDoctor(cmd, provider, deps, jsonOutput, quietMode)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON array")
	cmd.Flags().BoolVar(&quietMode, "quiet", false, "Suppress OK results; show only warnings and errors")
	return cmd
}

// runDoctor is the core execution logic for km doctor.
func runDoctor(cmd *cobra.Command, cfg DoctorConfigProvider, deps *DoctorDeps, jsonOutput, quietMode bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Initialize real AWS clients when deps is nil or partially nil.
	if deps == nil {
		deps = initRealDeps(ctx, cfg)
	}

	// Run credential check first — if SSO is expired, skip all AWS checks
	// rather than repeating the same credential error for every check.
	profile := cfg.GetAWSProfile()
	if profile == "" {
		profile = "klanker-terraform"
	}
	credResult := checkCredential(ctx, deps.STSClient, profile)
	if credResult.Status == CheckError && isCredentialError(credResult.Message) {
		configResult := checkConfig(cfg)
		results := []CheckResult{configResult, {
			Name:        credResult.Name,
			Status:      CheckError,
			Message:     fmt.Sprintf("SSO session expired for profile %q", profile),
			Remediation: fmt.Sprintf("Run 'aws sso login --profile %s' then re-run 'km doctor'", profile),
		}}
		out := cmd.OutOrStdout()
		if jsonOutput {
			return json.NewEncoder(out).Encode(results)
		}
		isTTY := isTerminal(out)
		for _, r := range results {
			fmt.Fprintln(out, formatCheckLine(r, isTTY))
		}
		summaryLine := fmt.Sprintf("\n%d checks passed, 0 warnings, 1 error (remaining checks skipped — no credentials)", boolToInt(configResult.Status == CheckOK))
		if isTTY {
			summaryLine = ansiRed + summaryLine + ansiReset
		}
		fmt.Fprintln(out, summaryLine)
		return fmt.Errorf("platform health check failed: SSO credentials expired")
	}

	// Build the check list.
	checks := buildChecks(cfg, deps)

	// Run all checks in parallel.
	results := runChecks(ctx, checks)

	// Count outcomes.
	var passCount, warnCount, errorCount int
	for _, r := range results {
		switch r.Status {
		case CheckOK:
			passCount++
		case CheckWarn:
			warnCount++
		case CheckError:
			errorCount++
		}
	}

	// Output results.
	out := cmd.OutOrStdout()
	if jsonOutput {
		toEncode := results
		if quietMode {
			toEncode = filterNonOK(results)
		}
		return json.NewEncoder(out).Encode(toEncode)
	}

	isTTY := isTerminal(out)
	for _, r := range results {
		if quietMode && (r.Status == CheckOK || r.Status == CheckSkipped) {
			continue
		}
		fmt.Fprintln(out, formatCheckLine(r, isTTY))
	}

	// Summary line.
	summaryLine := fmt.Sprintf("\n%d checks passed, %d warnings, %d errors", passCount, warnCount, errorCount)
	if isTTY {
		if errorCount > 0 {
			summaryLine = ansiRed + summaryLine + ansiReset
		} else if warnCount > 0 {
			summaryLine = ansiYellow + summaryLine + ansiReset
		} else {
			summaryLine = ansiGreen + summaryLine + ansiReset
		}
	}
	fmt.Fprintln(out, summaryLine)

	if errorCount > 0 {
		return fmt.Errorf("platform health check failed: %d error(s) found", errorCount)
	}
	return nil
}

// buildChecks assembles the full list of check closures.
func buildChecks(cfg DoctorConfigProvider, deps *DoctorDeps) []func(context.Context) CheckResult {
	checks := []func(context.Context) CheckResult{
		// Config check is synchronous (no AWS calls).
		func(ctx context.Context) CheckResult { return checkConfig(cfg) },
	}

	// Credential checks — one per AWS profile.
	profile := cfg.GetAWSProfile()
	if profile == "" {
		profile = "klanker-terraform"
	}
	stsClient := deps.STSClient
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkCredential(ctx, stsClient, profile)
	})

	// State bucket check.
	s3Client := deps.S3Client
	stateBucket := cfg.GetStateBucket()
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkStateBucket(ctx, s3Client, stateBucket)
	})

	// DynamoDB: budget table.
	dynamoClient := deps.DynamoClient
	budgetTable := cfg.GetBudgetTableName()
	if budgetTable == "" {
		budgetTable = "km-budgets"
	}
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkDynamoTable(ctx, dynamoClient, budgetTable, "Budget Table (km-budgets)")
	})

	// DynamoDB: identity table — demote error to warn.
	identityTable := cfg.GetIdentityTableName()
	if identityTable == "" {
		identityTable = "km-identities"
	}
	checks = append(checks, func(ctx context.Context) CheckResult {
		r := checkDynamoTable(ctx, dynamoClient, identityTable, "Identity Table (km-identities)")
		if r.Status == CheckError {
			r.Status = CheckWarn // identity table is optional
		}
		return r
	})

	// KMS key check.
	kmsClient := deps.KMSClient
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkKMSKey(ctx, kmsClient, "km-platform")
	})

	// SCP check — uses management account ID.
	orgsClient := deps.OrgsClient
	mgmtAccount := cfg.GetManagementAccountID()
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkSCP(ctx, orgsClient, mgmtAccount)
	})

	// GitHub config check.
	ssmClient := deps.SSMReadClient
	checks = append(checks, func(ctx context.Context) CheckResult {
		if ssmClient == nil {
			return CheckResult{
				Name:    "GitHub App Config",
				Status:  CheckSkipped,
				Message: "SSM client not available",
			}
		}
		return checkGitHubConfig(ctx, ssmClient)
	})

	// Per-region VPC checks.
	if deps.EC2Clients != nil {
		for region, ec2Client := range deps.EC2Clients {
			r := region
			c := ec2Client
			checks = append(checks, func(ctx context.Context) CheckResult {
				return checkRegionVPC(ctx, c, r)
			})
		}
	} else {
		// No EC2 clients — add a skipped placeholder for primary region.
		primaryRegion := cfg.GetPrimaryRegion()
		if primaryRegion == "" {
			primaryRegion = "us-east-1"
		}
		checks = append(checks, func(ctx context.Context) CheckResult {
			return CheckResult{
				Name:    fmt.Sprintf("VPC (%s)", primaryRegion),
				Status:  CheckSkipped,
				Message: "EC2 client not available",
			}
		})
	}

	// Lambda TTL handler check.
	lambdaClient := deps.LambdaClient
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkLambdaFunction(ctx, lambdaClient, "km-ttl-handler")
	})

	// SES domain identity check.
	sesClient := deps.SESClient
	domain := cfg.GetDomain()
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkSESIdentity(ctx, sesClient, domain)
	})

	// Sandbox summary check.
	lister := deps.Lister
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkSandboxSummary(ctx, lister)
	})

	return checks
}

// initRealDeps creates real AWS clients from configuration.
// Client creation failures are non-fatal — the corresponding field is set to nil
// and the check will be skipped.
func initRealDeps(ctx context.Context, cfg DoctorConfigProvider) *DoctorDeps {
	deps := &DoctorDeps{}

	profile := cfg.GetAWSProfile()
	if profile == "" {
		profile = "klanker-terraform"
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, profile)
	if err != nil {
		// All checks requiring AWS credentials will be skipped.
		return deps
	}

	deps.STSClient = sts.NewFromConfig(awsCfg)
	deps.S3Client = s3.NewFromConfig(awsCfg)
	deps.DynamoClient = dynamodb.NewFromConfig(awsCfg)
	deps.KMSClient = kms.NewFromConfig(awsCfg)
	deps.SSMReadClient = ssm.NewFromConfig(awsCfg)

	// Organizations client (for SCP check) — requires management account creds.
	deps.OrgsClient = organizations.NewFromConfig(awsCfg)

	// Lambda and SES clients for regional infra checks.
	deps.LambdaClient = lambda.NewFromConfig(awsCfg)
	deps.SESClient = sesv2.NewFromConfig(awsCfg)

	// Per-region EC2 clients.
	deps.EC2Clients = make(map[string]EC2DescribeAPI)
	primaryRegion := cfg.GetPrimaryRegion()
	if primaryRegion == "" {
		primaryRegion = "us-east-1"
	}
	ec2Cfg := awsCfg.Copy()
	ec2Cfg.Region = primaryRegion
	deps.EC2Clients[primaryRegion] = ec2.NewFromConfig(ec2Cfg)

	// Optional replica region.
	if replicaRegion := os.Getenv("KM_REPLICA_REGION"); replicaRegion != "" && replicaRegion != primaryRegion {
		replicaCfg, err := config.LoadDefaultConfig(ctx,
			config.WithSharedConfigProfile(profile),
			config.WithRegion(replicaRegion),
		)
		if err == nil {
			deps.EC2Clients[replicaRegion] = ec2.NewFromConfig(replicaCfg)
		}
	}

	// Sandbox lister — only if state bucket is configured.
	if stateBucket := cfg.GetStateBucket(); stateBucket != "" {
		listerCfg := awsCfg.Copy()
		deps.Lister = &doctorSandboxLister{
			awsCfg: listerCfg,
			bucket: stateBucket,
		}
	}

	return deps
}

// doctorSandboxLister wraps the real AWS lister for the doctor command.
type doctorSandboxLister struct {
	awsCfg awssdk.Config
	bucket string
}

func (l *doctorSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	inner := newRealLister(l.awsCfg, l.bucket)
	return inner.ListSandboxes(ctx, useTagScan)
}
