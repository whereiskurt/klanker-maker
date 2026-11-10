package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/scp"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt/planreport"
)

// OrgsSCPReadAPI reads the attached SCP's content. The real
// *organizations.Client satisfies it; km-org-admin grants both calls.
type OrgsSCPReadAPI interface {
	ListPoliciesForTarget(ctx context.Context, params *organizations.ListPoliciesForTargetInput, optFns ...func(*organizations.Options)) (*organizations.ListPoliciesForTargetOutput, error)
	DescribePolicy(ctx context.Context, params *organizations.DescribePolicyInput, optFns ...func(*organizations.Options)) (*organizations.DescribePolicyOutput, error)
}

// fetchLiveSCP returns the content of the policy named name attached to
// account, or found=false when none is attached.
func fetchLiveSCP(ctx context.Context, client OrgsSCPReadAPI, account, name string) (content []byte, found bool, err error) {
	var token *string
	for {
		out, err := client.ListPoliciesForTarget(ctx, &organizations.ListPoliciesForTargetInput{
			TargetId:  awssdk.String(account),
			Filter:    "SERVICE_CONTROL_POLICY",
			NextToken: token,
		})
		if err != nil {
			return nil, false, err
		}
		for _, p := range out.Policies {
			if awssdk.ToString(p.Name) != name {
				continue
			}
			d, err := client.DescribePolicy(ctx, &organizations.DescribePolicyInput{PolicyId: p.Id})
			if err != nil {
				return nil, true, err
			}
			if d.Policy == nil {
				return nil, true, fmt.Errorf("DescribePolicy %s returned no policy", awssdk.ToString(p.Id))
			}
			return []byte(awssdk.ToString(d.Policy.Content)), true, nil
		}
		if out.NextToken == nil {
			return nil, false, nil
		}
		token = out.NextToken
	}
}

// formatSCPDiff renders statement differences for a human. Missing = in the
// rendered policy, absent live (an apply ADDS it). Extra = live only (an apply
// REMOVES it — the case that bites when someone edited the SCP by hand).
func formatSCPDiff(diffs []scp.StatementDiff) string {
	var b strings.Builder
	for _, d := range diffs {
		fmt.Fprintf(&b, "  %s\n", d.Sid)
		for _, m := range d.Missing {
			fmt.Fprintf(&b, "    + %s\n", m)
		}
		for _, e := range d.Extra {
			fmt.Fprintf(&b, "    - %s   (live only — an apply removes it)\n", e)
		}
	}
	return b.String()
}

// checkSCPContent compares the live SCP with the policy pkg/scp renders from
// km-config.yaml. The presence check (checkSCP) stayed green through a hand
// edit of the live policy; this one names the drift and the statement.
func checkSCPContent(ctx context.Context, client OrgsSCPReadAPI, in scp.Input, orgConfigured bool) CheckResult {
	const name = "SCP content"
	if client == nil || !orgConfigured {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "accounts.organization not configured or Organizations client unavailable"}
	}
	want, err := scp.Render(in)
	if err != nil {
		return CheckResult{Name: name, Status: CheckError, Message: "render SCP from km-config.yaml: " + err.Error(),
			Remediation: "Fix the scp: block in km-config.yaml"}
	}
	policyName := scp.PolicyName(in.ResourcePrefix)
	live, found, err := fetchLiveSCP(ctx, client, in.ApplicationAccountID, policyName)
	if err != nil {
		if strings.Contains(err.Error(), "AccessDenied") || strings.Contains(err.Error(), "don't have permissions") {
			return CheckResult{Name: name, Status: CheckWarn,
				Message: fmt.Sprintf("cannot read %s (needs the org-admin role in the management account): %v", policyName, err)}
		}
		return CheckResult{Name: name, Status: CheckError, Message: fmt.Sprintf("read %s: %v", policyName, err)}
	}
	if !found {
		return CheckResult{Name: name, Status: CheckSkipped, Message: policyName + " not attached (see the SCP presence check)"}
	}
	diffs, err := scp.Compare(want.JSON(), live)
	if err != nil {
		return CheckResult{Name: name, Status: CheckError, Message: err.Error()}
	}
	if len(diffs) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: policyName + " matches km-config.yaml"}
	}
	sids := make([]string, 0, len(diffs))
	for _, d := range diffs {
		sids = append(sids, d.Sid)
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%s differs from km-config.yaml in %s:\n%s", policyName, strings.Join(sids, ", "), strings.TrimRight(formatSCPDiff(diffs), "\n")),
		Remediation: "If a live-only entry is wanted, add it to the scp: block of km-config.yaml; " +
			"then review with `km bootstrap --scp-only --plan` and apply with `km bootstrap --scp-only --dry-run=false`",
	}
}

// orgAdminOrgsClient assumes {prefix}-org-admin in the management account and
// returns an Organizations client — the same role the terragrunt provider
// assumes, so a read here sees exactly what an apply would change.
func orgAdminOrgsClient(ctx context.Context, cfg *config.Config) (*organizations.Client, error) {
	region := cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}
	base, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s-org-admin", cfg.OrganizationAccountID, cfg.GetResourcePrefix())
	out, err := sts.NewFromConfig(base).AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         awssdk.String(roleARN),
		RoleSessionName: awssdk.String("km-scp-plan"),
	})
	if err != nil {
		return nil, fmt.Errorf("assume %s: %w", roleARN, err)
	}
	orgsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(newStaticCredentials(
			awssdk.ToString(out.Credentials.AccessKeyId),
			awssdk.ToString(out.Credentials.SecretAccessKey),
			awssdk.ToString(out.Credentials.SessionToken),
		)),
	)
	if err != nil {
		return nil, err
	}
	return organizations.NewFromConfig(orgsCfg), nil
}

// RunBootstrapSCPPlanFunc is the seam for `km bootstrap --scp-only --plan`
// (and the SCP step of `--plan` / `--all --plan`).
var RunBootstrapSCPPlanFunc = runBootstrapSCPPlan

// RunBootstrapSCPOnlyFunc is the seam for `km bootstrap --scp-only` without --plan.
var RunBootstrapSCPOnlyFunc = runBootstrapSCPOnly

// runBootstrapSCPPlan renders the SCP, prints its statement-level difference
// from the live policy (read through {prefix}-org-admin), then runs a real
// terragrunt plan on infra/live/management/scp through the destroy-class gate.
// Never applies.
func runBootstrapSCPPlan(cfg *config.Config, acceptDestroys bool) error {
	ctx := context.Background()
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}
	w := os.Stdout
	if loadedCfg.OrganizationAccountID == "" {
		fmt.Fprintln(w, "SCP: skipped — accounts.organization not set (no SCP to plan).")
		return nil
	}
	ExportTerragruntEnvVars(loadedCfg)
	want, err := setBootstrapSCPEnv(loadedCfg)
	if err != nil {
		return err
	}
	name := scp.PolicyName(loadedCfg.GetResourcePrefix())
	fmt.Fprintln(w, "km bootstrap --scp-only --plan")
	fmt.Fprintf(w, "  %s → application account %s, rendered %d of %d bytes\n\n", name, loadedCfg.ApplicationAccountID, len(want.JSON()), scp.MaxBytes)

	if client, cerr := orgAdminOrgsClient(ctx, loadedCfg); cerr != nil {
		fmt.Fprintf(w, "  (cannot read the live policy: %v)\n\n", cerr)
	} else if live, found, ferr := fetchLiveSCP(ctx, client, loadedCfg.ApplicationAccountID, name); ferr != nil {
		fmt.Fprintf(w, "  (cannot read the live policy: %v)\n\n", ferr)
	} else if !found {
		fmt.Fprintf(w, "  %s is not attached yet — an apply creates it.\n\n", name)
	} else if diffs, derr := scp.Compare(want.JSON(), live); derr != nil {
		fmt.Fprintf(w, "  (cannot compare: %v)\n\n", derr)
	} else if len(diffs) == 0 {
		fmt.Fprintln(w, "  Live policy already matches km-config.yaml.")
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "  Statement changes (live → rendered):")
		fmt.Fprint(w, formatSCPDiff(diffs))
		fmt.Fprintln(w)
	}

	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner("klanker-terraform", repoRoot)
	m := regionalModule{name: "scp", dir: filepath.Join(repoRoot, "infra", "live", "management", "scp")}
	report, err := planModule(ctx, runner, m, false)
	if err != nil {
		return fmt.Errorf("planning %s: %w", m.name, err)
	}
	result := planreport.Evaluate([]planreport.Report{report}, acceptDestroys)
	if len(result.Trips) > 0 {
		printTripBlock("km bootstrap --scp-only --plan", result.Trips)
	}
	if result.Blocked {
		return fmt.Errorf("destroy-class gate tripped (re-run with --i-accept-destroys to override)")
	}
	fmt.Fprintln(w, "\nApply with: km bootstrap --scp-only --dry-run=false")
	return nil
}

// runBootstrapSCPOnly is `km bootstrap --scp-only`: the SCP alone, without the
// KMS key and artifacts bucket the plain `km bootstrap` also ensures. Dry run
// prints the rendered policy; --dry-run=false applies it.
func runBootstrapSCPOnly(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}
	if dryRun {
		if loadedCfg.OrganizationAccountID == "" {
			fmt.Fprintln(w, "SCP: skipped — accounts.organization not set.")
			return nil
		}
		p, err := renderSCP(loadedCfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "# %s (%d of %d bytes) — rendered from km-config.yaml\n", scp.PolicyName(loadedCfg.GetResourcePrefix()), len(p.JSON()), scp.MaxBytes)
		fmt.Fprintln(w, p.Indented())
		fmt.Fprintln(w, "\nDry run. Diff against the live policy: km bootstrap --scp-only --plan")
		fmt.Fprintln(w, "Apply:                              km bootstrap --scp-only --dry-run=false")
		return nil
	}
	ExportTerragruntEnvVars(loadedCfg)
	return applyBootstrapSCP(ctx, loadedCfg, w)
}
