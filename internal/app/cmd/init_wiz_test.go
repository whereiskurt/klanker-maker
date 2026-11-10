package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

func wizTestSettings() *wizoutpost.Settings {
	return &wizoutpost.Settings{
		TrustRoleARN:          "arn:aws:iam::444455556666:role/wiz-trust",
		OrchestratorAccountID: "111122223333",
		ExternalID:            "abcdefgh-1234-5678-9abc-def012345678",
		RoleSuffix:            "example",
	}
}

func wizRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestExportTerragruntEnvVars_WizOutpost(t *testing.T) {
	t.Setenv("KM_WIZ_OUTPOST_JSON", "")
	os.Unsetenv("KM_WIZ_OUTPOST_JSON") //nolint:errcheck

	s := wizTestSettings()
	ExportTerragruntEnvVars(&config.Config{Wiz: config.WizConfig{Outpost: s}})
	want, err := wizoutpost.ExportJSON(*s)
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("KM_WIZ_OUTPOST_JSON"); got != want {
		t.Errorf("KM_WIZ_OUTPOST_JSON = %q, want %q", got, want)
	}

	os.Unsetenv("KM_WIZ_OUTPOST_JSON") //nolint:errcheck
	ExportTerragruntEnvVars(&config.Config{})
	if got := os.Getenv("KM_WIZ_OUTPOST_JSON"); got != "" {
		t.Errorf("absent wiz block must leave KM_WIZ_OUTPOST_JSON unset, got %q", got)
	}
}

func TestExportTerragruntEnvVars_WizOutpostEnvDriftWarns(t *testing.T) {
	t.Setenv("KM_WIZ_OUTPOST_JSON", `{"parameters":{"RoleSuffix":"other"}}`)
	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(&config.Config{Wiz: config.WizConfig{Outpost: wizTestSettings()}})
	})
	if !strings.Contains(stderr, "KM_WIZ_OUTPOST_JSON") {
		t.Errorf("expected drift WARN naming KM_WIZ_OUTPOST_JSON, got %q", stderr)
	}
	if got := os.Getenv("KM_WIZ_OUTPOST_JSON"); got != `{"parameters":{"RoleSuffix":"other"}}` {
		t.Errorf("env value must win, got %q", got)
	}
}

func TestRegionalModules_WizOutpostEntry(t *testing.T) {
	mods := regionalModules(t.TempDir())
	if mods[len(mods)-1].name != "ses" {
		t.Fatalf("last module = %q, want ses", mods[len(mods)-1].name)
	}
	last := mods[len(mods)-2]
	if last.name != "wiz-outpost-connector" {
		t.Fatalf("module before ses = %q, want wiz-outpost-connector", last.name)
	}
	if len(last.envReqs) != 1 || last.envReqs[0] != "KM_WIZ_OUTPOST_JSON" {
		t.Errorf("envReqs = %v, want [KM_WIZ_OUTPOST_JSON]", last.envReqs)
	}
}

func TestWizLiveUnit_ToleratesEmptyEnv(t *testing.T) {
	hcl := wizRepoFile(t, "infra/live/use1/wiz-outpost-connector/terragrunt.hcl")
	for _, want := range []string{
		`get_env("KM_WIZ_OUTPOST_JSON", "{}")`,
		`try(jsondecode(`,
		`infra/modules/wiz-outpost-connector/v1.0.0`,
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("live unit missing %q", want)
		}
	}
}

// stack_name must equal wizoutpost.StackName(prefix) where prefix is the same
// source lambda-webhook-bridge passes as resource_prefix.
func TestWizLiveUnit_StackNameMatchesStackName(t *testing.T) {
	webhook := wizRepoFile(t, "infra/live/use1/lambda-webhook-bridge/terragrunt.hcl")
	m := regexp.MustCompile(`resource_prefix\s*=\s*(local\.[A-Za-z0-9_.]+)`).FindStringSubmatch(webhook)
	if m == nil {
		t.Fatal("cannot find resource_prefix source in lambda-webhook-bridge unit")
	}
	hcl := wizRepoFile(t, "infra/live/use1/wiz-outpost-connector/terragrunt.hcl")
	want := `stack_name = "${` + m[1] + `}` + wizoutpost.StackName("") + `"`
	if !strings.Contains(hcl, want) {
		t.Errorf("live unit stack_name expression not %s", want)
	}
	if wizoutpost.StackName("km") != "km-wiz-outpost-connector" {
		t.Errorf("StackName contract changed")
	}
	site := wizRepoFile(t, "infra/live/site.hcl")
	if !strings.Contains(site, `label           = get_env("KM_RESOURCE_PREFIX", "km")`) {
		t.Errorf("site.label is no longer KM_RESOURCE_PREFIX")
	}
}

func TestWizModule_NoRequiredProviders(t *testing.T) {
	for _, f := range []string{"main.tf", "variables.tf", "outputs.tf"} {
		if strings.Contains(wizRepoFile(t, "infra/modules/wiz-outpost-connector/v1.0.0/"+f), "required_providers") {
			t.Errorf("%s declares required_providers", f)
		}
	}
}

func TestWizModule_OutputsIndexOnlyParametersMainSets(t *testing.T) {
	main := wizRepoFile(t, "infra/modules/wiz-outpost-connector/v1.0.0/main.tf")
	outputs := wizRepoFile(t, "infra/modules/wiz-outpost-connector/v1.0.0/outputs.tf")
	if !regexp.MustCompile(`parameters\s*=\s*merge\(var\.parameters,\s*\{[^}]*WizVersionLastUpdated\s*=`).MatchString(main) {
		t.Errorf("main.tf parameters must merge WizVersionLastUpdated into var.parameters")
	}
	for _, m := range regexp.MustCompile(`\.parameters\["([A-Za-z0-9_]+)"\]`).FindAllStringSubmatch(outputs, -1) {
		if !regexp.MustCompile(`(?m)^\s*` + m[1] + `\s*=`).MatchString(main) {
			t.Errorf("outputs.tf indexes parameters[%q] but main.tf never sets it", m[1])
		}
	}
}

func TestRunInitDryRun_WizModuleNotSkippedWhenConfigured(t *testing.T) {
	t.Setenv("KM_WIZ_OUTPOST_JSON", "")
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	cfg.Wiz.Outpost = wizTestSettings()
	out := captureStdout(func() { _ = runInitDryRun(cfg, "us-east-1") })
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "wiz-outpost-connector") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no wiz-outpost-connector line in output:\n%s", out)
	}
	if strings.Contains(line, "[skip") {
		t.Errorf("configured wiz module skipped: %q", line)
	}
}
