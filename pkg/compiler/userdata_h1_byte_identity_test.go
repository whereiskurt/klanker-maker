package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// h1ByteIdentityGolden is the testdata-relative filename of the pre-H1 userdata
// dormancy baseline. It is captured in Phase 103 Wave 0 (Plan 01) BEFORE any
// HackerOne poller heredoc exists in userdata.go, so the post-change assertion
// (Plan 08) is meaningful: after the H1 poller block lands in Plan 07, this test
// MUST still pass for an H1-free profile — i.e. the poller heredoc only renders
// when notification.h1.inbound.enabled is true.
const h1ByteIdentityGolden = "h1_byte_identity_golden.txt"

// h1BaselineProfile is an unrelated, HackerOne-free profile. It is an in-tree
// testdata profile (not a top-level profiles/ file) so the golden stays stable
// even if the shipped profile inventory churns. It declares no notification.h1
// block of any kind.
const h1BaselineProfile = "ec2-basic.yaml"

// generateH1BaselineUserdata loads the H1-free baseline profile and runs the
// current compiler's userdata generator with fixed, deterministic inputs.
// Centralizing the call site guarantees the capture path
// (TestCapturePreH1Userdata) and the verification path
// (TestUserdataH1ByteIdentity) drive identical inputs — otherwise the
// byte-identity comparison would be meaningless. Mirrors
// generateLearnV2Userdata in userdata_phase92_byte_identity_test.go.
func generateH1BaselineUserdata(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	profPath := filepath.Join(filepath.Dir(thisFile), "testdata", h1BaselineProfile)

	raw, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile %s: %v", profPath, err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile %s: %v", profPath, err)
	}

	// Fixed inputs mirror the phase92 byte-identity convention: deterministic
	// sandbox ID + bucket, no spot, nil network. The profile's own spec drives
	// the rest of the rendered output.
	got, err := generateUserData(p, "sb-phase103-baseline", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return got
}

// goldenPathH1 resolves a testdata-relative golden filename to an absolute path.
func goldenPathH1(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", name)
}

// TestCapturePreH1Userdata writes the pre-H1 userdata dormancy golden. It is a
// CAPTURE helper, not an assertion: it only runs when CAPTURE_PRE_H1_BASELINE=1
// is set, so normal `go test` runs skip it. Capture once on pre-Phase-103 main,
// commit the golden, then never run it again — the H1 poller change (Plan 07)
// must keep TestUserdataH1ByteIdentity green against this captured baseline.
//
//	CAPTURE_PRE_H1_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePreH1Userdata
func TestCapturePreH1Userdata(t *testing.T) {
	if os.Getenv("CAPTURE_PRE_H1_BASELINE") != "1" {
		t.Skip("set CAPTURE_PRE_H1_BASELINE=1 to (re)capture the pre-Phase-103 userdata dormancy baseline")
	}
	got := generateH1BaselineUserdata(t)
	out := goldenPathH1(t, h1ByteIdentityGolden)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", out, err)
	}
	t.Logf("captured pre-Phase-103 userdata dormancy baseline (%d bytes) -> %s", len(got), out)
}

// TestUserdataH1ByteIdentity verifies that the userdata generated for an
// H1-free profile is byte-identical to the pre-Phase-103 dormancy baseline
// captured in Wave 0. This is the dormancy invariant: adding the HackerOne
// inbound poller heredoc to userdata.go (Plan 07) MUST NOT change the rendered
// userdata for any profile that does not opt in via
// notification.h1.inbound.enabled. Plan 08 re-runs this after the poller lands;
// any drift here means the H1 block leaked into the dormant path.
//
// On pre-Phase-103 main: PASS (golden matches generated output verbatim).
func TestUserdataH1ByteIdentity(t *testing.T) {
	golden := goldenPathH1(t, h1ByteIdentityGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 0 dormancy baseline was not committed)", golden, err)
	}

	got := generateH1BaselineUserdata(t)

	if got != string(want) {
		t.Errorf("userdata drifted from the pre-Phase-103 dormancy baseline for the H1-free "+
			"profile %s (the H1 poller block must NOT render unless notification.h1.inbound.enabled "+
			"is true):\n%s", h1BaselineProfile, diffStrings(string(want), got))
	}
}
