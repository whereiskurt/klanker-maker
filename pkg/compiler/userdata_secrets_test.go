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
// (the default — no Spec.Secrets set), the secrets blocks are NOT emitted.
// Existing profiles must be byte-identical: no secrets noise in output.
func TestUserdataSopsBlock_AbsentWhenFalse(t *testing.T) {
	p := baseProfile() // Spec.Secrets == nil → SopsBundlePresent=false
	out, err := generateUserData(p, "sb-default", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// None of the secrets-specific markers must appear.
	for _, banned := range []string{
		"Brokered secret unsealing",
		"/etc/sandbox-secrets.env",
		"sops decrypt",
		"sandbox-secrets",
		"km-secretsd",
		"km-env",
		"/opt/km/shims",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("secrets block must be absent when SopsBundlePresent=false; found %q in output", banned)
		}
	}
}

// TestUserdataSopsBlock_PresentWhenTrue verifies that when Spec.Secrets.SopsFile is
// set, the Phase 133 broker blocks ARE emitted: the binaries land, the ciphertext
// lands root-only, and the daemon unit is written and started.
func TestUserdataSopsBlock_PresentWhenTrue(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-abc123", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	required := []string{
		// Binaries: sops for the daemon's own decrypt, plus the broker and client.
		`s3://${KM_ARTIFACTS_BUCKET}/binaries/sops`,
		`s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-secretsd`,
		`s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-env`,
		`ln -sf /opt/km/bin/km-env /usr/local/bin/km-env`,
		// The bundle at rest, per sandbox.
		`s3://${KM_ARTIFACTS_BUCKET}/sandboxes/sb-abc123/secrets.enc.yaml`,
		// Ciphertext is root-only: no group read, unlike the Phase 89 plaintext file.
		`chown root:root /etc/sandbox-secrets.enc.yaml`,
		`chmod 0400 /etc/sandbox-secrets.enc.yaml`,
		// The daemon.
		`/etc/systemd/system/km-secretsd.service`,
		`ExecStart=/opt/km/bin/km-secretsd serve`,
		`systemctl enable --now km-secretsd.service`,
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in secrets block output; not found", want)
		}
	}
}

// TestUserdataSopsBlock_NoPlaintextAtRest is the phase in one assertion: the
// bundle is never decrypted to disk and never auto-exported into a login shell.
// A regression here silently restores the exact exposure Phase 133 removes —
// an `env` dump inside an agent turn collecting the whole bundle.
func TestUserdataSopsBlock_NoPlaintextAtRest(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-plain", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, banned := range []string{
		"/etc/sandbox-secrets.env",
		"/etc/profile.d/zz-sandbox-secrets.sh",
		"sops decrypt --output-type dotenv",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("userdata still emits %q: the bundle would be readable from every login shell again", banned)
		}
	}
}

// TestUserdataSopsBlock_StartLimitInUnitSection pins the km-execlog lesson.
// Since systemd v230 the [Service] section only recognises the pre-rename
// StartLimitInterval= spelling, so StartLimitIntervalSec=/StartLimitBurst=
// placed there are silently dropped as unknown — leaving a permanently broken
// broker hidden behind an endless restart loop instead of one failed unit.
func TestUserdataSopsBlock_StartLimitInUnitSection(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-startlimit", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	unitIdx := strings.Index(out, "Description=km secrets broker")
	if unitIdx < 0 {
		t.Fatal("km-secretsd unit not found in output")
	}
	serviceIdx := strings.Index(out[unitIdx:], "\n[Service]\n")
	if serviceIdx < 0 {
		t.Fatal("[Service] section not found in km-secretsd unit")
	}

	unitSection := out[unitIdx : unitIdx+serviceIdx]
	for _, want := range []string{"StartLimitIntervalSec=60", "StartLimitBurst=5"} {
		if !strings.Contains(unitSection, want) {
			t.Errorf("%q must appear in the [Unit] section, before [Service]; it would be silently dropped otherwise", want)
		}
	}
}

// TestUserdataSopsBlock_GrantsJSONIsSingleQuoted guards a systemd parsing trap.
// systemd strips double quotes from an unquoted Environment= value, so
// {"claude":["A"]} would reach the daemon as {claude:[A]} and fail to parse.
// Enclosing the whole assignment in single quotes makes the inner quotes literal.
func TestUserdataSopsBlock_GrantsJSONIsSingleQuoted(t *testing.T) {
	p := sopsBundleProfile()
	p.Spec.Secrets.Grants = map[string][]string{"claude": {"ANTHROPIC_API_KEY"}}
	out, err := generateUserData(p, "sb-grantq", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	want := `Environment='KM_SECRETS_GRANTS={"claude":["ANTHROPIC_API_KEY"]}'`
	if !strings.Contains(out, want) {
		t.Errorf("expected single-quoted grants env %q; not found", want)
	}
	if strings.Contains(out, `Environment=KM_SECRETS_GRANTS={"`) {
		t.Error("grants env is unquoted: systemd would strip the JSON's double quotes")
	}
}

// TestUserdataSopsBlock_ConsumersDefaultAndFromGrants pins the shim set.
// Absent grants the consumers are claude+codex; with grants they are the grant
// keys, sorted so the rendered userdata is deterministic across map iterations.
func TestUserdataSopsBlock_ConsumersDefaultAndFromGrants(t *testing.T) {
	t.Run("default consumers when no grants", func(t *testing.T) {
		p := sopsBundleProfile()
		out, err := generateUserData(p, "sb-defcons", nil, "my-bucket", false, nil)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}
		for _, want := range []string{"/opt/km/shims/claude", "/opt/km/shims/codex"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected default shim %q; not found", want)
			}
		}
		if !strings.Contains(out, `Environment='KM_SECRETS_GRANTS={}'`) {
			t.Error("expected an empty grants object when the profile declares none")
		}
	})

	t.Run("grant keys become the consumers, sorted", func(t *testing.T) {
		p := sopsBundleProfile()
		p.Spec.Secrets.Grants = map[string][]string{
			"zeta":  {"Z"},
			"alpha": {"A"},
		}
		out, err := generateUserData(p, "sb-grantcons", nil, "my-bucket", false, nil)
		if err != nil {
			t.Fatalf("generateUserData failed: %v", err)
		}
		alpha := strings.Index(out, "/opt/km/shims/alpha")
		zeta := strings.Index(out, "/opt/km/shims/zeta")
		if alpha < 0 || zeta < 0 {
			t.Fatalf("expected shims for both grant keys; alpha=%d zeta=%d", alpha, zeta)
		}
		if alpha > zeta {
			t.Error("shim generation is not sorted: userdata would differ run to run for the same profile")
		}
		if strings.Contains(out, "/opt/km/shims/claude") {
			t.Error("declared grants must replace the default consumer set, not extend it")
		}
	})
}

// TestUserdataSopsBlock_ShimExecsAbsolutePath verifies the shim never resolves
// its target by bare name: doing so would re-find the shim on PATH and recurse
// until the process runs out of stack.
func TestUserdataSopsBlock_ShimExecsAbsolutePath(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-shimexec", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, `exec /opt/km/bin/km-env exec --as claude -- "\$KM_REAL" "\$@"`) {
		t.Error("shim must exec an absolute path via km-env, not the bare consumer name")
	}
	// The fallback search must strip the shim dir, or it re-finds the shim.
	if !strings.Contains(out, `grep -v '^/opt/km/shims$'`) {
		t.Error("shim fallback must remove /opt/km/shims from PATH before searching")
	}
}

// TestUserdataSopsBlock_ShimCarriesLiteralTargetForSelftest pins a cross-task
// contract. cmd/km-secretsd/selftest.go:shimTarget reads the generated shim to
// check the real binary still exists. The exec line is NOT the place to read it
// from: it carries the escaped token "\$KM_REAL", which the heredoc renders as a
// literal "$KM_REAL" on disk — os.Stat on that always fails, so the shim check
// would report FATAL on every boot of every sops sandbox (aborting the boot) and
// the PATH-race check after it would never run at all.
//
// The baked absolute path lives on the KM_REAL= line, which the UNQUOTED heredoc
// expands at generation time. That line is what a parser must read, and the
// generated shim says so in a comment so the coupling is discoverable from the
// file itself.
func TestUserdataSopsBlock_ShimCarriesLiteralTargetForSelftest(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-shimparse", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Unescaped on purpose: this one expands when the heredoc is written, which
	// is what puts a real path in the file. Escaping it would break the parser.
	if !strings.Contains(out, "\nKM_REAL=\"$KM_SHIM_TARGET\"\n") {
		t.Error("the shim's KM_REAL= line must carry the generation-time-expanded " +
			"absolute path: km-secretsd's selftest parses it to verify the target exists")
	}
	if !strings.Contains(out, "# NOTE: km-secretsd selftest (shimTarget) parses this KM_REAL= line") {
		t.Error("the generated shim must say that a parser depends on the KM_REAL= line, " +
			"or the next person to reformat the shim silently breaks the boot check")
	}
}

// TestUserdataSopsBlock_BootCheckAborts verifies the boot self-test runs inline
// during userdata. Under `set -euo pipefail` a non-zero exit aborts the boot —
// the same disposition as the Phase 89 sops-decrypt FATAL it replaces. A box
// that looks healthy and fails at first turn is worse, because an autonomous
// trigger finds it before a human does.
func TestUserdataSopsBlock_BootCheckAborts(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-bootchk", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "/opt/km/bin/km-secretsd selftest") {
		t.Fatal("expected an inline km-secretsd selftest invocation; not found")
	}
	// Resume coverage: userdata does not re-run on stop/start, so the oneshot
	// unit is the only thing that re-checks a rotated bundle on a resumed box.
	for _, want := range []string{
		"/etc/systemd/system/km-secrets-check.service",
		"Requires=km-secretsd.service",
		"systemctl enable km-secrets-check.service",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q for resume coverage; not found", want)
		}
	}
}

// TestUserdataSopsBlock_ShimPathHeredocByteCheck locks down the heredoc body
// between << 'KMSHIMPATH' and KMSHIMPATH so Go template trim-semantics ({{- }})
// cannot silently elide leading whitespace or newlines. A mangled case statement
// here fails open: PATH keeps its default and every interactive km shell runs
// the unshimmed agent with no secrets.
func TestUserdataSopsBlock_ShimPathHeredocByteCheck(t *testing.T) {
	const expectedHeredocBody = "case \":$PATH:\" in\n  *\":/opt/km/shims:\"*) ;;\n  *) PATH=\"/opt/km/shims:$PATH\"; export PATH ;;\nesac"

	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	startMarker := "<< 'KMSHIMPATH'\n"
	endMarker := "\nKMSHIMPATH\n"

	startIdx := strings.Index(out, startMarker)
	if startIdx < 0 {
		t.Fatal("expected heredoc marker \"<< 'KMSHIMPATH'\\n\" in output; not found")
	}
	bodyStart := startIdx + len(startMarker)

	endIdx := strings.Index(out[bodyStart:], endMarker)
	if endIdx < 0 {
		t.Fatal("expected heredoc terminator '\\nKMSHIMPATH\\n' after body start; not found")
	}

	actualBody := out[bodyStart : bodyStart+endIdx]
	if actualBody != expectedHeredocBody {
		t.Errorf("shim PATH heredoc body mismatch\nwant: %q\n got: %q", expectedHeredocBody, actualBody)
	}
}

// TestUserdataSopsBlock_DispatchPrependIsGatedAndRendered verifies both halves of
// the dormancy contract for the dispatch-site PATH prepend: it renders when a
// bundle is present, and it leaves no trace at all when one is not.
func TestUserdataSopsBlock_DispatchPrependIsGatedAndRendered(t *testing.T) {
	// Backslash-escaped in the emitted poller script: the outer double-quoted
	// string belongs to the poller shell, and only the inner `bash -lc`/`bash -c`
	// may expand PATH.
	const prepend = `PATH=/opt/km/shims:\$PATH; `

	with, err := generateUserData(sopsBundleProfile(), "sb-disp1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(with, prepend) {
		t.Error("dispatch sites must prepend the shim dir when a bundle is present")
	}

	without, err := generateUserData(baseProfile(), "sb-disp2", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(without, prepend) {
		t.Error("dispatch sites must be byte-identical to pre-Phase-133 when no bundle is present")
	}
}
