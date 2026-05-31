package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// phase92LearnV2UserdataGolden is the on-disk path (relative to this test file)
// of the pre-Phase-92 userdata baseline captured from profiles/learn.v2.yaml.
const phase92LearnV2UserdataGolden = "userdata_learn_v2_pre92_baseline.golden.sh"

// generateLearnV2Userdata loads profiles/learn.v2.yaml and runs the current
// compiler's userdata generator with the same fixed inputs used to capture the
// Wave 0 baseline. Centralizing the call site guarantees the capture path
// (TestCapturePre92Userdata) and the verification path
// (TestUserdataLearnV2Phase92ByteIdentity) drive identical inputs — otherwise a
// byte-identity comparison would be meaningless.
func generateLearnV2Userdata(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	// pkg/compiler/<thisfile> -> repo root is two dirs up.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	profPath := filepath.Join(repoRoot, "profiles", "learn.v2.yaml")

	raw, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile %s: %v", profPath, err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile %s: %v", profPath, err)
	}

	// Fixed inputs mirror the existing golden-test convention
	// (TestUserdataAdditionalVolumeOnly_GoldenByteIdentical): deterministic
	// sandbox ID + bucket, no spot, nil network. learn.v2's own spec drives the
	// rest of the rendered output.
	got, err := generateUserData(p, "sb-phase92-baseline", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return got
}

// goldenPath92 resolves a testdata-relative golden filename to an absolute path.
func goldenPath92(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", name)
}

// TestCapturePre92Userdata writes the pre-Phase-92 userdata baseline golden.
// It is a CAPTURE helper, not an assertion: it only runs when
// CAPTURE_PRE92_BASELINE=1 is set, so normal `go test` runs skip it. Capture once
// on pre-Phase-92 main, commit the golden, then never run it again — Wave 1-5
// must keep the byte-identity test (below) green against the captured baseline.
//
//	CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata
func TestCapturePre92Userdata(t *testing.T) {
	if os.Getenv("CAPTURE_PRE92_BASELINE") != "1" {
		t.Skip("set CAPTURE_PRE92_BASELINE=1 to (re)capture the pre-Phase-92 userdata baseline")
	}
	got := generateLearnV2Userdata(t)
	out := goldenPath92(t, phase92LearnV2UserdataGolden)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", out, err)
	}
	t.Logf("captured pre-Phase-92 userdata baseline (%d bytes) -> %s", len(got), out)
}

// TestUserdataLearnV2Phase92ByteIdentity verifies that the userdata generated for
// profiles/learn.v2.yaml is byte-identical to the pre-Phase-92 baseline captured
// in Wave 0. This guards the Phase 92 contract that the spec restructure (IAM
// rename, notification block, dead-field removal, structured agent gating) is
// semantically transparent: the YAML surface changes, but the effective userdata
// emitted for an equivalent profile does not.
//
// On pre-Phase-92 main: PASS (golden matches generated output).
// After Waves 1-5 land: must STILL PASS (byte-identity contract).
//
// VC-3
func TestUserdataLearnV2Phase92ByteIdentity(t *testing.T) {
	golden := goldenPath92(t, phase92LearnV2UserdataGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
	}

	got := generateLearnV2Userdata(t)

	if got != string(want) {
		t.Errorf("userdata for profiles/learn.v2.yaml drifted from pre-Phase-92 baseline:\n%s",
			diffStrings(string(want), got))
	}
}
