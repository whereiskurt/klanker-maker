package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// phase92IAMHCLGolden is the on-disk path (relative to this test file, under
// testdata/) of the pre-Phase-92 IAM HCL baseline.
const phase92IAMHCLGolden = "security_iam_pre92_baseline.golden.hcl"

// phase92IAMFixture loads pkg/profile/builtins/restricted-dev.yaml (which sets
// identity.roleSessionDuration + identity.allowedRegions) and injects a
// representative identity.allowedSecretPaths so the captured baseline exercises
// ALL THREE security.go identity reads that Wave 1's IdentitySpec -> IAMSpec
// rename touches:
//
//	security.go:50  RoleSessionDuration -> max_session_duration
//	security.go:56  AllowedRegions      -> region-lock allowed_regions
//	security.go:74  AllowedSecretPaths  -> SSM parameter allow-list (service_hcl)
func phase92IAMFixture(t *testing.T) *profile.SandboxProfile {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	// pkg/compiler/<thisfile> -> repo root is two dirs up.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	profPath := filepath.Join(repoRoot, "pkg", "profile", "builtins", "restricted-dev.yaml")

	raw, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatalf("read profile %s: %v", profPath, err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile %s: %v", profPath, err)
	}
	// Inject a representative allowedSecretPaths so the SSM-allowlist serialization
	// is covered by the byte-identity contract (restricted-dev.yaml itself does
	// not set this field).
	p.Spec.Identity.AllowedSecretPaths = []string{
		"/sandbox/shared/db-password",
		"/sandbox/shared/api-key",
	}
	return p
}

// emitCombinedIAMHCLForTest renders the IAM-relevant HCL fragments using the SAME
// compiler entry points and serialization the production path uses:
//
//   - compileIAMPolicy(p) drives max_session_duration + allowed_regions, rendered
//     exactly as service_hcl.go's iam_session_policy block (lines 129-132).
//   - compileSecrets(p) drives the KM_SECRET_PATHS comma-join, rendered exactly as
//     service_hcl.go's KMSecretPaths serialization (lines 1032-1033).
//
// Wave 1's IdentitySpec -> IAMSpec rename MUST leave compileIAMPolicy /
// compileSecrets output byte-identical, so this fragment is a precise, low-noise
// guard (no subnet/AMI churn from the full EC2 service HCL).
func emitCombinedIAMHCLForTest(p *profile.SandboxProfile) string {
	pol := compileIAMPolicy(p)

	quoted := make([]string, len(pol.AllowedRegions))
	for i, r := range pol.AllowedRegions {
		quoted[i] = fmt.Sprintf("%q", r)
	}
	allowedRegions := strings.Join(quoted, ", ")

	secretPaths := strings.Join(compileSecrets(p), ",")

	var sb strings.Builder
	sb.WriteString("iam_session_policy = {\n")
	sb.WriteString(fmt.Sprintf("  max_session_duration = %d\n", pol.MaxSessionDuration))
	sb.WriteString(fmt.Sprintf("  allowed_regions      = [%s]\n", allowedRegions))
	sb.WriteString("}\n")
	sb.WriteString(fmt.Sprintf("km_secret_paths = %q\n", secretPaths))
	return sb.String()
}

// TestCapturePre92IAMHCL writes the pre-Phase-92 IAM HCL baseline golden. CAPTURE
// helper only — runs solely when CAPTURE_PRE92_IAM_BASELINE=1 is set. Capture once
// on pre-Phase-92 main, commit the golden, then let the byte-identity test (below)
// guard Wave 1's rename.
//
//	CAPTURE_PRE92_IAM_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92IAMHCL
func TestCapturePre92IAMHCL(t *testing.T) {
	if os.Getenv("CAPTURE_PRE92_IAM_BASELINE") != "1" {
		t.Skip("set CAPTURE_PRE92_IAM_BASELINE=1 to (re)capture the pre-Phase-92 IAM HCL baseline")
	}
	got := emitCombinedIAMHCLForTest(phase92IAMFixture(t))
	out := goldenPath92(t, phase92IAMHCLGolden)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", out, err)
	}
	t.Logf("captured pre-Phase-92 IAM HCL baseline (%d bytes) -> %s", len(got), out)
}

// TestIAMHCLPhase92ByteIdentity verifies that after Wave 1's IdentitySpec -> IAMSpec
// rename (with SessionPolicy DELETED, AllowedSecretPaths PRESERVED), the emitted
// IAM role HCL (max_session_duration), region-lock policy HCL (allowed_regions),
// and SSM-allowlist serialization (km_secret_paths) remain byte-identical. This is
// the contract that the rename is purely lexical at the Go-source layer and does
// NOT change Terraform output.
//
// On pre-Phase-92 main: PASS. After Wave 1's rename: must STILL PASS.
//
// VC-4
func TestIAMHCLPhase92ByteIdentity(t *testing.T) {
	golden := goldenPath92(t, phase92IAMHCLGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
	}

	got := emitCombinedIAMHCLForTest(phase92IAMFixture(t))

	if got != string(want) {
		t.Errorf("IAM HCL output drifted from pre-Phase-92 baseline:\n%s",
			diffStrings(string(want), got))
	}
}
