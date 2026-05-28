package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// sopsBundleProfile returns a SandboxProfile with SopsBundlePresent (Spec.Secrets.SopsFile set).
func sopsBundleProfile() *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.Secrets = &profile.SecretsSpec{
		SopsFile: "./secrets/test.enc.yaml",
	}
	return p
}

// TestUserdataSopsBlock_AbsentWhenFalse verifies that when SopsBundlePresent=false
// (the default — no Spec.Secrets set), the SOPS section 5.5 block is NOT emitted.
// Existing pre-Phase-89 profiles must be byte-identical: no SOPS noise in output.
func TestUserdataSopsBlock_AbsentWhenFalse(t *testing.T) {
	p := baseProfile() // Spec.Secrets == nil → SopsBundlePresent=false
	out, err := generateUserData(p, "sb-default", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// None of the SOPS-specific markers must appear.
	for _, banned := range []string{
		"SOPS secret injection",
		"/etc/sandbox-secrets.env",
		"sops decrypt",
		"sandbox-secrets",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("SOPS block must be absent when SopsBundlePresent=false; found %q in output", banned)
		}
	}
}

// TestUserdataSopsBlock_PresentWhenTrue verifies that when Spec.Secrets.SopsFile is set,
// the SOPS section 5.5 block IS emitted with all required elements (SOPS-12, SOPS-14, WARNING 7).
func TestUserdataSopsBlock_PresentWhenTrue(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-abc123", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	required := []string{
		// SOPS-12: sops binary + bundle download paths
		`s3://${KM_ARTIFACTS_BUCKET}/binaries/sops`,
		`s3://${KM_ARTIFACTS_BUCKET}/sandboxes/sb-abc123/secrets.enc.yaml`,
		// SOPS-13: decrypt invocation
		`sops decrypt --output-type dotenv`,
		// SOPS-14: profile.d sourcing
		`/etc/sandbox-secrets.env`,
		`/etc/profile.d/zz-sandbox-secrets.sh`,
		// WARNING 7: encrypted-file chmod must be present (not just decrypted-file chmod)
		`chmod 0400 /etc/sandbox-secrets.enc.yaml`,
		// Decrypted file must be readable by the sandbox user (group), NOT root-only —
		// otherwise the profile.d login-shell sourcing silently fails the [ -r ] guard.
		`chown root:sandbox /etc/sandbox-secrets.env`,
		`chmod 0440 /etc/sandbox-secrets.env`,
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in SOPS block output; not found", want)
		}
	}
}

// TestUserdataSopsBlock_FailAbortExit1 verifies that the sops decrypt failure path
// emits an exit 1 within a few lines after the sops decrypt invocation (SOPS-15).
func TestUserdataSopsBlock_FailAbortExit1(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-abort", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Find "sops decrypt" and assert "exit 1" appears within 200 chars after it.
	idx := strings.Index(out, "sops decrypt")
	if idx < 0 {
		t.Fatal("expected 'sops decrypt' in output; not found")
	}
	window := out[idx:]
	if len(window) > 200 {
		window = window[:200]
	}
	if !strings.Contains(window, "exit 1") {
		t.Errorf("expected 'exit 1' within 200 chars after 'sops decrypt'; window: %q", window)
	}
}

// TestUserdataSopsBlock_ProfileDSourcing verifies the /etc/profile.d script uses
// the correct set -a / source / set +a pattern (SOPS-14).
func TestUserdataSopsBlock_ProfileDSourcing(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-profiledtest", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// All three must appear and in this order.
	setA := strings.Index(out, "set -a")
	dotSource := strings.Index(out, ". /etc/sandbox-secrets.env")
	setPlus := strings.Index(out, "set +a")

	if setA < 0 {
		t.Error("expected 'set -a' in output")
	}
	if dotSource < 0 {
		t.Error("expected '. /etc/sandbox-secrets.env' in output")
	}
	if setPlus < 0 {
		t.Error("expected 'set +a' in output")
	}
	if setA >= 0 && dotSource >= 0 && setPlus >= 0 {
		if !(setA < dotSource && dotSource < setPlus) {
			t.Errorf("expected set -a (%d) < '. /etc/sandbox-secrets.env' (%d) < set +a (%d)", setA, dotSource, setPlus)
		}
	}
}

// TestUserdataSopsBlock_HeredocByteCheck locks down the heredoc body between
// << 'SOPSENV' and SOPSENV so Go template trim-semantics ({{- }}) cannot
// silently elide leading whitespace or newlines (WARNING 4 regression check).
func TestUserdataSopsBlock_HeredocByteCheck(t *testing.T) {
	const expectedHeredocBody = "# Phase 89: load decrypted secrets into login-shell env.\n# set -a/+a flips auto-export so dotenv KEY=VALUE lines become exported vars.\nif [ -r /etc/sandbox-secrets.env ]; then\n  set -a\n  . /etc/sandbox-secrets.env\n  set +a\nfi"

	p := sopsBundleProfile()
	p.Spec.Secrets.SopsFile = "./secrets/test.enc.yaml"
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Find the heredoc markers.
	startMarker := "<< 'SOPSENV'\n"
	endMarker := "\nSOPSENV\n"

	startIdx := strings.Index(out, startMarker)
	if startIdx < 0 {
		t.Fatalf("expected heredoc marker '<< 'SOPSENV'\\n' in output; not found")
	}
	bodyStart := startIdx + len(startMarker)

	endIdx := strings.Index(out[bodyStart:], endMarker)
	if endIdx < 0 {
		t.Fatalf("expected heredoc terminator '\\nSOPSENV\\n' after body start; not found")
	}

	actualBody := out[bodyStart : bodyStart+endIdx]
	if actualBody != expectedHeredocBody {
		t.Errorf("heredoc body mismatch (WARNING 4 byte-check)\nwant: %q\n got: %q", expectedHeredocBody, actualBody)
	}
}
