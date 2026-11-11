package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

// makeUninitRepo creates repoRoot/infra/live/use1/<mod> for each module.
func makeUninitRepo(t *testing.T, mods ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, m := range mods {
		if err := os.MkdirAll(filepath.Join(root, "infra", "live", "use1", m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// region.hcl is written by ensureRegionHCL if absent; create it up front.
	if err := os.WriteFile(filepath.Join(root, "infra", "live", "use1", "region.hcl"),
		[]byte("locals {\n  region_label = \"use1\"\n  region_full  = \"us-east-1\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// km uninit --wiz destroys exactly the wiz-outpost-connector unit — never a
// sibling module — and reconfigures it first, like the full uninit loop.
func TestRunUninitScoped_WizDestroysOnlyTheWizUnit(t *testing.T) {
	root := makeUninitRepo(t, "wiz-outpost-connector", "ses", "network")
	m := &mockUninitRunner{}
	var err error
	out := captureStdout(t, func() {
		err = cmd.RunUninitScopedWithRunner(m, root, "us-east-1", "wiz-outpost-connector")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 1 || !strings.HasSuffix(m.calls[0], filepath.Join("use1", "wiz-outpost-connector")) {
		t.Fatalf("destroy calls = %v, want only the wiz unit", m.calls)
	}
	if len(m.reconfigureCalls) != 1 {
		t.Fatalf("reconfigure calls = %v, want 1", m.reconfigureCalls)
	}
	if !strings.Contains(out, "Destroying wiz-outpost-connector... done") {
		t.Fatalf("missing progress line:\n%s", out)
	}
	if !strings.Contains(out, "remove the connector in the Wiz UI") {
		t.Fatalf("missing Wiz-side reminder:\n%s", out)
	}
}

func TestRunUninitScoped_MissingUnitDirErrors(t *testing.T) {
	root := makeUninitRepo(t, "ses")
	m := &mockUninitRunner{}
	err := cmd.RunUninitScopedWithRunner(m, root, "us-east-1", "wiz-outpost-connector")
	if err == nil || !strings.Contains(err.Error(), "wiz-outpost-connector") {
		t.Fatalf("err = %v, want a not-found error naming the module", err)
	}
	if len(m.calls) != 0 {
		t.Fatalf("destroy must not run: %v", m.calls)
	}
}

// Unlike the full uninit loop (warn and continue), a scoped destroy has one
// target, so its failure is the command's failure.
func TestRunUninitScoped_DestroyFailureIsReturned(t *testing.T) {
	root := makeUninitRepo(t, "wiz-outpost-connector")
	m := &mockUninitRunner{errs: map[string]error{"wiz-outpost-connector": errors.New("boom")}}
	var err error
	captureStdout(t, func() {
		err = cmd.RunUninitScopedWithRunner(m, root, "us-east-1", "wiz-outpost-connector")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the destroy error", err)
	}
}

func TestValidateUninitFlags_WizExcludesIncludeSCP(t *testing.T) {
	if err := cmd.ValidateUninitFlags(true, true); err == nil {
		t.Fatal("--wiz with --include-scp must be rejected")
	}
	for _, c := range [][2]bool{{true, false}, {false, true}, {false, false}} {
		if err := cmd.ValidateUninitFlags(c[0], c[1]); err != nil {
			t.Fatalf("ValidateUninitFlags(%v,%v) = %v", c[0], c[1], err)
		}
	}
}

func TestUninitWizPrompt_NamesStackAndRoles(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "km"}
	cfg.Wiz.Outpost = &wizoutpost.Settings{RoleSuffix: "km", AccessRoleName: "WizAccess-Role"}
	p := cmd.UninitWizPrompt(cfg, "us-east-1")
	for _, want := range []string{"km-wiz-outpost-connector", "WizAccess-Role", "WizScannerRole-km", "us-east-1", "[y/N]"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q: %s", want, p)
		}
	}

	// The block may already be gone from km-config.yaml (that is when you
	// tear it down); the prompt must still name the stack.
	bare := cmd.UninitWizPrompt(&config.Config{ResourcePrefix: "km"}, "us-east-1")
	if !strings.Contains(bare, "km-wiz-outpost-connector") {
		t.Errorf("prompt without wiz block missing stack name: %s", bare)
	}
}
