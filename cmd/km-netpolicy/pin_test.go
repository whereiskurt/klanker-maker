package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
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

// --exact was inert: pin wrote ONE flat generation that both matchers read, so
// the collapsed suffixes it always needs on the DNS side matched every
// subdomain on the host side too and the literal hosts beside them narrowed
// nothing. The file now scopes each entry, so the two matchers give genuinely
// different answers — which is the whole documented point of the flag.
func TestPin_ExactActuallyNarrowsTheHostSide(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--exact", "--yes"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()

	if !netpolicy.PinsAllowDNS("gist.github.com", gens) {
		t.Error("DNS must stay collapsed under --exact, or the box stops resolving almost at once")
	}
	if netpolicy.PinsAllowHost("gist.github.com", gens) {
		t.Error("--exact must refuse a host the census never saw — otherwise the flag does nothing")
	}
	if !netpolicy.PinsAllowHost("api.github.com", gens) {
		t.Error("the observed literal host must survive its own exact pin")
	}
}

// The collapsed default must keep behaving as documented: both scopes hold the
// same eTLD+1 suffixes, so a sibling hostname the session happened not to touch
// is not stranded.
func TestPin_DefaultCollapsesBothScopes(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--yes"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if !netpolicy.PinsAllowHost("gist.github.com", gens) {
		t.Error("the collapsed default must not strand a sibling hostname")
	}
}

// A pin is irreversible, so a census it can prove is missing records must not
// be sealed silently: the destinations lost to rotation would be pinned out
// with no other signal they ever existed.
func TestPin_RefusesATruncatedCensusWithoutAck(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	// Two rotations means the ".1" generation was overwritten.
	counter := flowlog.FileFor(o.flowDir, flowlog.SrcHTTP) + ".rotations"
	if err := os.WriteFile(counter, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runPin([]string{"--yes"}, o); code == 0 {
		t.Fatal("pin must refuse a census known to have lost records")
	}
	if !strings.Contains(errb.String(), "--accept-truncated") {
		t.Errorf("refusal must name the override:\n%s", errb.String())
	}
	if code := runPin([]string{"--yes", "--accept-truncated"}, o); code != 0 {
		t.Fatalf("the ack must let a deliberate operator through: exit %d, stderr=%s", code, errb.String())
	}
}

// The platform recovery floor is invisible in the candidate lists, and an
// operator who believed a pin sealed EVERYTHING unnamed would be wrong about
// the one host set that decides whether the box can still be recovered.
func TestPin_NamesThePlatformCarveOut(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	if code := runPin([]string{"--dry-run"}, o); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), ".amazonaws.com") {
		t.Errorf("pin must state what it can never seal:\n%s", out.String())
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
