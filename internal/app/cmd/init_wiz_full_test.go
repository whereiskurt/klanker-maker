package cmd_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

func wizFullMock() *mockRunner {
	return &mockRunner{outputs: map[string]interface{}{
		"access_role_arn":  map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizAccess-Role"},
		"scanner_role_arn": map[string]interface{}{"value": "arn:aws:iam::111122223333:role/WizScannerRole-km"},
		"external_id":      map[string]interface{}{"value": "abcdef12-3456-7890-abcd-ef1234567890"},
	}}
}

func TestRunInitFull_WizPrintsPasteOut(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"wiz-outpost-connector"})
	t.Setenv("KM_WIZ_OUTPOST_JSON", "{}")
	mock := wizFullMock()
	var err error
	out := captureStdout(t, func() { err = cmd.RunInitWithRunner(mock, repoRoot, "us-east-1") })
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.applied) != 1 || !strings.HasSuffix(mock.applied[0], "wiz-outpost-connector") {
		t.Fatalf("applied = %v", mock.applied)
	}
	want := "Wiz Outpost connector — paste into the Wiz UI:\n" +
		"  Access role ARN   arn:aws:iam::111122223333:role/WizAccess-Role\n" +
		"  Scanner role ARN  arn:aws:iam::111122223333:role/WizScannerRole-km\n" +
		"  External ID       abcdef12-3456-7890-abcd-ef1234567890\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing exact paste-out block:\n%s", out)
	}
}

func TestRunInitFull_WizSkippedWithoutEnv(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"wiz-outpost-connector"})
	t.Setenv("KM_WIZ_OUTPOST_JSON", "")
	os.Unsetenv("KM_WIZ_OUTPOST_JSON") //nolint:errcheck
	mock := wizFullMock()
	var err error
	out := captureStdout(t, func() { err = cmd.RunInitWithRunner(mock, repoRoot, "us-east-1") })
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.applied) != 0 {
		t.Fatalf("applied = %v", mock.applied)
	}
	if strings.Contains(out, "paste into the Wiz UI") || !strings.Contains(out, "[skip] wiz-outpost-connector") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
