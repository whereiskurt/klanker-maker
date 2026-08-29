package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func pinOpts(t *testing.T, out, errb *bytes.Buffer) opts {
	t.Helper()
	pinFile := filepath.Join(t.TempDir(), "allow.pins")
	// The real file is created at boot with chattr +a. Pin refuses to create it
	// itself, so the test must pre-create it exactly as the box does.
	if err := os.WriteFile(pinFile, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	return opts{flowDir: seedFlows(t), pinFile: pinFile, stdout: out, stderr: errb}
}

func TestPin_DryRunWritesNothing(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--dry-run"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	body, err := os.ReadFile(o.pinFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("--dry-run must not write: %q", body)
	}
	got := out.String()
	if !strings.Contains(got, ".github.com") {
		t.Errorf("dry-run must show the candidate set:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "irreversible") {
		t.Errorf("dry-run must state that pinning cannot be undone:\n%s", got)
	}
}

func TestPin_RequiresYes(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin(nil, o); code == 0 {
		t.Fatal("pin without --yes must refuse")
	}
	body, _ := os.ReadFile(o.pinFile)
	if len(body) != 0 {
		t.Fatalf("refused pin must not write: %q", body)
	}
}

func TestPin_YesAppendsGeneration(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--yes"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(gens) != 1 {
		t.Fatalf("want 1 generation, got %d", len(gens))
	}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("observed host must survive its own pin")
	}
	if netpolicy.PinsAllow("unrelated.example", gens) {
		t.Error("unobserved host must be denied after pinning")
	}
}

func TestPin_SecondPinOnlyNarrows(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	if code := runPin([]string{"--yes"}, o); code != 0 {
		t.Fatalf("first pin exit %d", code)
	}

	// A second pin taken from an emptier census must not restore anything.
	o2 := o
	o2.flowDir = t.TempDir()
	if code := runPin([]string{"--yes", "--allow-empty"}, o2); code != 0 {
		t.Fatalf("second pin exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(gens) != 2 {
		t.Fatalf("want 2 generations, got %d", len(gens))
	}
	if netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("a later empty pin must narrow, never restore")
	}
}

func TestPin_EmptyCensusRefusedWithoutAllowEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	o.flowDir = filepath.Join(t.TempDir(), "absent")

	if code := runPin([]string{"--yes"}, o); code == 0 {
		t.Fatal("pinning an empty census is deny-all and must be refused by default")
	}
	if !strings.Contains(errb.String(), "--allow-empty") {
		t.Errorf("refusal must name the override:\n%s", errb.String())
	}
}

func TestPin_MissingFileRefuses(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), pinFile: filepath.Join(t.TempDir(), "absent"), stdout: &out, stderr: &errb}

	if code := runPin([]string{"--yes"}, o); code == 0 {
		t.Fatal("pin must refuse when the append-only file does not exist")
	}
	if _, err := os.Stat(o.pinFile); err == nil {
		t.Fatal("pin must not create the file itself — it would lack chattr +a and nothing would enforce it")
	}
}
