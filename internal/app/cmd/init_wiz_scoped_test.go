package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

func TestResolveScopedModule_Wiz(t *testing.T) {
	got, err := cmd.ResolveScopedModule("", false, false, false, false, false, true)
	if err != nil || got != "wiz-outpost-connector" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, err = cmd.ResolveScopedModule("", true, false, false, false, false, true)
	if err == nil || !strings.Contains(err.Error(), "--wiz") {
		t.Fatalf("want error mentioning --wiz, got %v", err)
	}
}

func TestPrintWizPasteOut(t *testing.T) {
	var buf bytes.Buffer
	cmd.PrintWizPasteOut(&buf, "arn:aws:iam::111122223333:role/WizAccess-Role",
		"arn:aws:iam::111122223333:role/WizScannerRole-km", "abcdef12-3456-7890-abcd-ef1234567890")
	want := "Wiz Outpost connector — paste into the Wiz UI:\n" +
		"  Access role ARN   arn:aws:iam::111122223333:role/WizAccess-Role\n" +
		"  Scanner role ARN  arn:aws:iam::111122223333:role/WizScannerRole-km\n" +
		"  External ID       abcdef12-3456-7890-abcd-ef1234567890\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRunInitScoped_WizPrintsPasteOutAfterApply(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"wiz-outpost-connector"})
	t.Setenv("KM_WIZ_OUTPOST_JSON", "{}")
	mock := &mockRunner{outputs: map[string]interface{}{
		"access_role_arn":  map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizAccess-Role"},
		"scanner_role_arn": map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizScannerRole-km"},
		"external_id":      map[string]interface{}{"value": "abcdef12-3456-7890-abcd-ef1234567890"},
	}}
	var err error
	out := captureStdout(t, func() {
		err = cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "wiz-outpost-connector", false, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "paste into the Wiz UI") || !strings.Contains(out, "abcdef12-3456-7890-abcd-ef1234567890") {
		t.Fatalf("missing paste-out:\n%s", out)
	}

	// Dry-run: no paste-out, no apply.
	mock2 := &mockRunner{}
	out = captureStdout(t, func() {
		err = cmd.RunInitScopedWithRunner(mock2, repoRoot, "us-east-1", "wiz-outpost-connector", true, false)
	})
	if err != nil || len(mock2.applied) != 0 {
		t.Fatalf("dry-run: err=%v applied=%v", err, mock2.applied)
	}
	if !strings.Contains(out, "Would apply: wiz-outpost-connector") || strings.Contains(out, "paste into the Wiz UI") {
		t.Fatalf("dry-run output wrong:\n%s", out)
	}
}

func TestRunInitScoped_WizMissingOutputsWarnsNotFails(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"wiz-outpost-connector"})
	t.Setenv("KM_WIZ_OUTPOST_JSON", "{}")
	mock := &mockRunner{outputs: map[string]interface{}{}}
	var err error
	out := captureStdout(t, func() {
		err = cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "wiz-outpost-connector", false, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "km wiz status") {
		t.Fatalf("want warning naming km wiz status:\n%s", out)
	}
}

func TestRunInitScoped_WizWithoutBlock_ErrorsClearly(t *testing.T) {
	err := cmd.RequireWizBlock(&config.Config{}, "wiz-outpost-connector")
	if err == nil || !strings.Contains(err.Error(), "wiz-outpost-connector (--wiz) requires a wiz.outpost block in km-config.yaml") {
		t.Fatalf("got %v", err)
	}
	if err := cmd.RequireWizBlock(&config.Config{}, "lambda-github-bridge"); err != nil {
		t.Fatalf("other module should pass: %v", err)
	}
	cfg := &config.Config{}
	cfg.Wiz.Outpost = &wizoutpost.Settings{}
	if err := cmd.RequireWizBlock(cfg, "wiz-outpost-connector"); err != nil {
		t.Fatalf("with block: %v", err)
	}
}

// UAT 2026-11-10: the scoped wiz apply ended with the bridge-Lambda notes
// ("refreshed env+IAM only … stale code zip"), which do not apply to a
// CloudFormation stack. It must print the Wiz next step instead.
func TestRunInitScoped_WizClosingNoteIsWizSpecific(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"wiz-outpost-connector"})
	t.Setenv("KM_WIZ_OUTPOST_JSON", "{}")
	mock := &mockRunner{outputs: map[string]interface{}{
		"access_role_arn":  map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizAccess-Role"},
		"scanner_role_arn": map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizScannerRole-km"},
		"external_id":      map[string]interface{}{"value": "abcdef12-3456-7890-abcd-ef1234567890"},
	}}
	var err error
	out := captureStdout(t, func() {
		err = cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "wiz-outpost-connector", false, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "stale code zip") || strings.Contains(out, "refreshed env+IAM only") {
		t.Fatalf("bridge-Lambda notes printed for the wiz module:\n%s", out)
	}
	if !strings.Contains(out, "Next: paste the three values into the Wiz UI connector form, then run km doctor.") {
		t.Fatalf("missing wiz next-step note:\n%s", out)
	}
}
