package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/scp"
)

func scpCfg() *config.Config {
	return &config.Config{
		ResourcePrefix:        "km",
		PrimaryRegion:         "us-east-1",
		Domain:                "example.com",
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
	}
}

type fakeSCPOrgs struct {
	content string
	listErr error
}

func (f *fakeSCPOrgs) ListPoliciesForTarget(_ context.Context, _ *organizations.ListPoliciesForTargetInput, _ ...func(*organizations.Options)) (*organizations.ListPoliciesForTargetOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &organizations.ListPoliciesForTargetOutput{Policies: []orgtypes.PolicySummary{
		{Id: awssdk.String("p-FullAWSAccess"), Name: awssdk.String("FullAWSAccess")},
		{Id: awssdk.String("p-km"), Name: awssdk.String("km-sandbox-containment")},
	}}, nil
}

func (f *fakeSCPOrgs) DescribePolicy(_ context.Context, in *organizations.DescribePolicyInput, _ ...func(*organizations.Options)) (*organizations.DescribePolicyOutput, error) {
	if awssdk.ToString(in.PolicyId) != "p-km" {
		return nil, errors.New("AccessDeniedException: aws-managed policy")
	}
	return &organizations.DescribePolicyOutput{Policy: &orgtypes.Policy{Content: awssdk.String(f.content)}}, nil
}

// The review's live drift: the console-added kmv-github-deploy exemption. With
// it absent from km-config.yaml, doctor must name it as live-only (an apply
// would remove it); once in scp.trusted_iam_roles, the check is green.
func TestCheckSCPContent_NamesLiveOnlyExemption(t *testing.T) {
	withKMV := scpCfg()
	withKMV.SCP.TrustedIAMRoles = []string{"arn:aws:iam::333333333333:role/kmv-github-deploy"}
	live, err := renderSCP(withKMV)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeSCPOrgs{content: string(live.JSON())}

	r := checkSCPContent(context.Background(), client, scpInputFromConfig(scpCfg()), true)
	if r.Status != CheckWarn || !strings.Contains(r.Message, "DenyIAMEscalation") ||
		!strings.Contains(r.Message, "kmv-github-deploy") || !strings.Contains(r.Message, "an apply removes it") {
		t.Fatalf("drift not reported: %+v", r)
	}

	r = checkSCPContent(context.Background(), client, scpInputFromConfig(withKMV), true)
	if r.Status != CheckOK {
		t.Fatalf("matching policy not OK: %+v", r)
	}
}

func TestCheckSCPContent_Degrades(t *testing.T) {
	in := scpInputFromConfig(scpCfg())
	if r := checkSCPContent(context.Background(), nil, in, true); r.Status != CheckSkipped {
		t.Errorf("nil client: %+v", r)
	}
	if r := checkSCPContent(context.Background(), &fakeSCPOrgs{}, in, false); r.Status != CheckSkipped {
		t.Errorf("no org: %+v", r)
	}
	denied := &fakeSCPOrgs{listErr: errors.New("AccessDeniedException: nope")}
	if r := checkSCPContent(context.Background(), denied, in, true); r.Status != CheckWarn {
		t.Errorf("access denied: %+v", r)
	}
	bad := in
	bad.TrustedRoles = []string{"not-an-arn"}
	if r := checkSCPContent(context.Background(), &fakeSCPOrgs{}, bad, true); r.Status != CheckError {
		t.Errorf("bad config: %+v", r)
	}
}

// `eval $(km env)` must hand terragrunt the exact rendered document. Runs the
// real output through sh rather than grepping it, because the value is JSON.
func TestEnvExport_SCPPolicySurvivesEval(t *testing.T) {
	cfg := scpCfg()
	cfg.SCP.TrustedIAMRoles = []string{"arn:aws:iam::333333333333:role/kmv-github-deploy"}
	var buf bytes.Buffer
	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", buf.String()+"\nprintf %s \"$KM_SCP_POLICY_JSON\"").Output()
	if err != nil {
		t.Fatalf("eval failed: %v\n%s", err, buf.String())
	}
	want, _ := renderSCP(cfg)
	if string(out) != string(want.JSON()) {
		t.Fatalf("evaluated KM_SCP_POLICY_JSON differs:\n got %s\nwant %s", out, want.JSON())
	}

	cfg.SCP.TrustedRoles = []string{"bogus"}
	buf.Reset()
	_ = runEnvExport(cfg, &buf, false)
	if !strings.Contains(buf.String(), "# KM_SCP_POLICY_JSON not exported") {
		t.Fatalf("render error not reported as a comment:\n%s", buf.String())
	}
	if _, err := exec.Command("sh", "-c", buf.String()).Output(); err != nil {
		t.Fatalf("output with a render error no longer evals: %v", err)
	}
}

func runBootstrapArgs(t *testing.T, cfg *config.Config, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := &cobra.Command{Use: "km"}
	root.AddCommand(NewBootstrapCmdWithWriter(cfg, &buf))
	root.SetArgs(append([]string{"bootstrap"}, args...))
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	return buf.String(), err
}

// --scp-only --dry-run=false applies infra/live/management/scp with the
// rendered policy in the environment, and nothing else (no KMS, no bucket).
func TestBootstrapSCPOnly_AppliesRenderedPolicy(t *testing.T) {
	cfg := scpCfg()
	cfg.SCP.TrustedIAMRoles = []string{"arn:aws:iam::333333333333:role/kmv-github-deploy"}
	t.Setenv(scpPolicyEnv, `{"stale":"from an earlier eval"}`)

	var dirs []string
	var policyAtApply string
	orig := ApplyTerragruntFunc
	ApplyTerragruntFunc = func(_ context.Context, dir string) error {
		dirs = append(dirs, dir)
		policyAtApply = os.Getenv(scpPolicyEnv)
		return nil
	}
	t.Cleanup(func() { ApplyTerragruntFunc = orig })

	if out, err := runBootstrapArgs(t, cfg, "--scp-only", "--dry-run=false"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(dirs) != 1 || !strings.HasSuffix(dirs[0], "infra/live/management/scp") {
		t.Fatalf("applied %v, want only infra/live/management/scp", dirs)
	}
	want, _ := renderSCP(cfg)
	if policyAtApply != string(want.JSON()) {
		t.Fatalf("policy at apply = %s", policyAtApply)
	}
}

// A bad scp: block stops the run before terragrunt starts.
func TestBootstrapSCPOnly_BadConfigNeverApplies(t *testing.T) {
	cfg := scpCfg()
	cfg.SCP.TrustedRoles = []string{"arn:aws:iam::*:role/*-provisioner-*"}
	orig := ApplyTerragruntFunc
	ApplyTerragruntFunc = func(context.Context, string) error { t.Fatal("apply ran"); return nil }
	t.Cleanup(func() { ApplyTerragruntFunc = orig })
	if _, err := runBootstrapArgs(t, cfg, "--scp-only", "--dry-run=false"); err == nil || !strings.Contains(err.Error(), "sandbox instance role") {
		t.Fatalf("want sandbox-role rejection, got %v", err)
	}
}

func TestBootstrapSCPOnly_DryRunPrintsPolicy(t *testing.T) {
	out, err := runBootstrapArgs(t, scpCfg(), "--scp-only")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"km-sandbox-containment", "DenyIAMEscalation", "--scp-only --plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run missing %q:\n%s", want, out)
		}
	}
}

// --plan alone, --scp-only --plan and --all --plan each reach the SCP plan;
// --all --plan plans SES exactly once (it used to plan SES twice, SCP never).
func TestBootstrapPlanRoutesToSCP(t *testing.T) {
	var calls []string
	origSCP, origSES, origSecrets := RunBootstrapSCPPlanFunc, RunBootstrapSharedSESPlanFunc, RunBootstrapSharedSecretsKeyPlanFunc
	RunBootstrapSCPPlanFunc = func(*config.Config, bool) error { calls = append(calls, "scp"); return nil }
	RunBootstrapSharedSESPlanFunc = func(*config.Config, bool) error { calls = append(calls, "ses"); return nil }
	RunBootstrapSharedSecretsKeyPlanFunc = func(*config.Config, bool) error { calls = append(calls, "secrets"); return nil }
	t.Cleanup(func() {
		RunBootstrapSCPPlanFunc, RunBootstrapSharedSESPlanFunc, RunBootstrapSharedSecretsKeyPlanFunc = origSCP, origSES, origSecrets
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--plan"}, "scp"},
		{[]string{"--scp-only", "--plan"}, "scp"},
		// Subflow 3 calls its writer-aware plan directly and fails on the test
		// binary's no-credentials seam; subflows 1 and 2 are what changed here.
		{[]string{"--all", "--plan"}, "scp,ses"},
	} {
		calls = nil
		out, err := runBootstrapArgs(t, scpCfg(), tc.args...)
		if err != nil && !strings.Contains(err.Error(), "subflow 3") {
			t.Fatalf("%v: %v\n%s", tc.args, err, out)
		}
		if got := strings.Join(calls, ","); got != tc.want {
			t.Errorf("%v: plans %s, want %s", tc.args, got, tc.want)
		}
	}
	if _, err := runBootstrapArgs(t, scpCfg(), "--scp-only", "--all"); err == nil {
		t.Error("--scp-only --all should be rejected")
	}
}

var _ = scp.MaxBytes

// The live unit must deploy the rendered policy (v3.0.0, KM_SCP_POLICY_JSON
// with no default). Re-authoring the policy in HCL would bring back the
// preview/deploy drift; a default would let an unset variable apply a stale
// or empty document.
func TestSCPLiveUnit_DeploysRenderedPolicy(t *testing.T) {
	b, err := os.ReadFile(findRepoRoot() + "/infra/live/management/scp/terragrunt.hcl")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{`infra/modules/scp//v3.0.0"`, `policy_json            = get_env("KM_SCP_POLICY_JSON")`} {
		if !strings.Contains(src, want) {
			t.Errorf("infra/live/management/scp/terragrunt.hcl lacks %s", want)
		}
	}
	if strings.Contains(src, "trusted_role_arns") || strings.Contains(src, `get_env("KM_SCP_POLICY_JSON",`) {
		t.Error("live unit authors trust lists or defaults the policy; it must take pkg/scp's output verbatim")
	}
}
