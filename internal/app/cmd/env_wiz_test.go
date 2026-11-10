package cmd

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

// UAT 2026-11-10: `km env` is the documented way to drive terragrunt by hand,
// but it never exported KM_WIZ_OUTPOST_JSON, so a hand-run plan of
// infra/live/use1/wiz-outpost-connector silently fell back to empty parameters.
func TestEnvCmd_ExportsWizOutpostJSON(t *testing.T) {
	cfg := makeSampleEnvConfig()
	cfg.Wiz.Outpost = &wizoutpost.Settings{
		TrustRoleARN:          "arn:aws:iam::111122223333:role/wiz-dc",
		OrchestratorAccountID: "444455556666",
		ExternalID:            "abcdef12-3456-7890-abcd-ef1234567890",
		RoleSuffix:            "km",
	}
	want, err := wizoutpost.ExportJSON(*cfg.Wiz.Outpost)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatalf("runEnvExport: %v", err)
	}
	line := "export KM_WIZ_OUTPOST_JSON='" + want + "'"
	if !strings.Contains(buf.String(), line) {
		t.Fatalf("missing %q in:\n%s", line, buf.String())
	}

	// The value must survive eval byte-for-byte.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command("bash", "-c", buf.String()+"\nprintf %s \"$KM_WIZ_OUTPOST_JSON\"").Output()
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if string(out) != want {
		t.Fatalf("eval round-trip:\n got %s\nwant %s", out, want)
	}
}

func TestEnvCmd_NoWizBlock_NoWizExport(t *testing.T) {
	cfg := makeSampleEnvConfig()
	var buf bytes.Buffer
	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "KM_WIZ_OUTPOST_JSON") {
		t.Fatalf("dormant install must not export KM_WIZ_OUTPOST_JSON:\n%s", buf.String())
	}
}

// role_suffix is free text; a single quote in it must not break eval.
func TestEnvCmd_WizOutpostJSON_SingleQuoteSurvivesEval(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cfg := makeSampleEnvConfig()
	cfg.Wiz.Outpost = &wizoutpost.Settings{
		TrustRoleARN:          "arn:aws:iam::111122223333:role/wiz-dc",
		OrchestratorAccountID: "444455556666",
		ExternalID:            "abcdef12-3456-7890-abcd-ef1234567890",
		RoleSuffix:            "k'm",
	}
	want, _ := wizoutpost.ExportJSON(*cfg.Wiz.Outpost)
	var buf bytes.Buffer
	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "-c", buf.String()+"\nprintf %s \"$KM_WIZ_OUTPOST_JSON\"").Output()
	if err != nil {
		t.Fatalf("eval failed: %v\n%s", err, buf.String())
	}
	if string(out) != want {
		t.Fatalf("eval round-trip:\n got %s\nwant %s", out, want)
	}
}
