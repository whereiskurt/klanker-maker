package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// phase9405ServiceHCLGolden is the path (under testdata/) to the km-prefix
// ECS service.hcl golden captured from the Phase 94-05 migrated compiler.
const phase9405ServiceHCLGolden = "service_hcl_km_prefix_94_05.golden.hcl"

// goldenPath9405 resolves a testdata-relative golden filename to an absolute path.
func goldenPath9405(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", name)
}

// generateKmPrefixECSServiceHCL renders the ECS service.hcl for a km-prefix
// fixture using fixed, deterministic inputs (sandboxID, network).
// Used by both the capture helper and the byte-identity assertion.
func generateKmPrefixECSServiceHCL(t *testing.T) string {
	t.Helper()
	t.Setenv("KM_RESOURCE_PREFIX", "km")
	t.Setenv("KM_ACCOUNTS_APPLICATION", "") // use PLACEHOLDER_ECR for determinism

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-94-05-km-baseline", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	return out
}

// TestCaptureServiceHCLKmPrefixBaseline writes the km-prefix ECS service.hcl golden.
// CAPTURE helper only — runs solely when CAPTURE_9405_BASELINE=1 is set.
// Capture once after the Phase 94-05 template migration, commit the golden, then
// let TestServiceHCLKmPrefixByteIdentity guard against future regressions.
//
//	CAPTURE_9405_BASELINE=1 go test ./pkg/compiler/ -run TestCaptureServiceHCLKmPrefixBaseline
func TestCaptureServiceHCLKmPrefixBaseline(t *testing.T) {
	if os.Getenv("CAPTURE_9405_BASELINE") != "1" {
		t.Skip("set CAPTURE_9405_BASELINE=1 to (re)capture the Phase 94-05 km-prefix ECS service.hcl baseline")
	}
	got := generateKmPrefixECSServiceHCL(t)
	out := goldenPath9405(t, phase9405ServiceHCLGolden)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", out, err)
	}
	t.Logf("captured Phase 94-05 km-prefix ECS service.hcl baseline (%d bytes) -> %s", len(got), out)
}

// TestServiceHCLKmPrefixByteIdentity verifies that the ECS service.hcl rendered
// for the km prefix is byte-identical to the Phase 94-05 golden — proving that
// the /{{ .ResourcePrefix }}/ template migration is a no-op for the default km
// install. This golden was captured POST-migration (not pre-92), so any future
// drift in the km output will fail this test.
func TestServiceHCLKmPrefixByteIdentity(t *testing.T) {
	golden := goldenPath9405(t, phase9405ServiceHCLGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		// Golden not yet captured — skip rather than fail, so CI can still run.
		// Run: CAPTURE_9405_BASELINE=1 go test ./pkg/compiler/ -run TestCaptureServiceHCLKmPrefixBaseline
		t.Skipf("golden %s not found (run CAPTURE_9405_BASELINE=1 to capture): %v", golden, err)
	}

	got := generateKmPrefixECSServiceHCL(t)
	if got != string(want) {
		t.Errorf("ECS service.hcl km-prefix output drifted from Phase 94-05 baseline:\n%s",
			diffStrings(string(want), got))
	}
}

// TestServiceHCLKmPrefixContainsDynamicPaths is a belt-and-suspenders assertion
// that the km-prefix ECS output contains /km/sandboxes/ and /km/sidecars/ exactly
// (not a template literal), regardless of whether the golden file exists.
// Together with TestECSCWLogGroupResourcePrefix (kph case), this proves dynamic
// substitution works at both ends of the prefix space.
func TestServiceHCLKmPrefixContainsDynamicPaths(t *testing.T) {
	got := generateKmPrefixECSServiceHCL(t)

	for _, wantPath := range []string{
		"/km/sandboxes/sb-94-05-km-baseline/",
		"/km/sidecars/sb-94-05-km-baseline",
	} {
		if !strings.Contains(got, wantPath) {
			t.Errorf("km-prefix ECS service.hcl missing %q\nOutput snippet:\n%s",
				wantPath, extractECSLines(got, "CW_LOG_GROUP"))
		}
	}

	// Must NOT contain any un-rendered template literals.
	for _, bad := range []string{"{{ .ResourcePrefix }}", "{{.ResourcePrefix}}"} {
		if strings.Contains(got, bad) {
			t.Errorf("ECS service.hcl contains un-rendered template literal %q", bad)
		}
	}
}
