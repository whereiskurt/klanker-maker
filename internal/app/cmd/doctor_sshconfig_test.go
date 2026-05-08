// Tests for checkStaleSSHConfig — operator-side VS Code Remote-SSH state
// orphan detection + optional cleanup gated on --delete-ssh.
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// makeSSHFixture writes a managed-block ssh-config file containing the given
// km-{sandboxID} entries, plus a chunk of operator content above the markers
// so tests can verify outside-marker bytes are preserved untouched.
func makeSSHFixture(t *testing.T, dir string, sandboxIDs []string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "config")
	for _, sid := range sandboxIDs {
		opts := HostOptions{
			HostName:     "localhost",
			Port:         2222,
			User:         "sandbox",
			IdentityFile: filepath.Join(dir, "keys", sid),
		}
		if err := UpsertHost(cfgPath, "km-"+sid, opts); err != nil {
			t.Fatalf("seed UpsertHost: %v", err)
		}
	}
	return cfgPath
}

// makeKeyFiles writes ~/.km/keys/<sandbox-id> + .pub stubs for each id,
// returning the keys dir path.
func makeKeyFiles(t *testing.T, dir string, sandboxIDs []string) string {
	t.Helper()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("mkdir keysDir: %v", err)
	}
	for _, sid := range sandboxIDs {
		if err := os.WriteFile(filepath.Join(keysDir, sid), []byte("priv"), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(keysDir, sid+".pub"), []byte("pub"), 0o644); err != nil {
			t.Fatalf("write pub: %v", err)
		}
	}
	return keysDir
}

// =============================================================================
// readManagedAliases / readKeyFileSandboxIDs — small parsing helpers
// =============================================================================

func TestReadManagedAliases_ExtractsKmAliasesOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-a", "sb-b"})
	// Sneak a non-km alias inside the markers — readManagedAliases must
	// ignore it (operator-edited content). UpsertHost won't put it there
	// for us, so write directly.
	raw, _ := os.ReadFile(cfgPath)
	mutated := strings.Replace(string(raw),
		"# END km vscode hosts",
		"Host my-laptop\n  User me\n# END km vscode hosts", 1)
	if err := os.WriteFile(cfgPath, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readManagedAliases(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !got["sb-a"] || !got["sb-b"] {
		t.Errorf("expected sb-a and sb-b, got: %v", got)
	}
	if got["my-laptop"] {
		t.Errorf("non-km alias must NOT appear; got: %v", got)
	}
}

func TestReadManagedAliases_MissingFile_EmptyNoError(t *testing.T) {
	got, err := readManagedAliases(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("missing file should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty set, got: %v", got)
	}
}

func TestReadKeyFileSandboxIDs_PairsCollapse(t *testing.T) {
	dir := t.TempDir()
	keysDir := makeKeyFiles(t, dir, []string{"sb-x", "sb-y"})
	got, err := readKeyFileSandboxIDs(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	if !got["sb-x"] || !got["sb-y"] {
		t.Errorf("expected sb-x and sb-y, got: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("priv+pub pair must collapse to one entry; got %d entries: %v", len(got), got)
	}
}

func TestReadKeyFileSandboxIDs_MissingDir_EmptyNoError(t *testing.T) {
	got, err := readKeyFileSandboxIDs(filepath.Join(t.TempDir(), "no-such-dir"))
	if err != nil {
		t.Errorf("missing dir should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir should yield empty set, got: %v", got)
	}
}

// =============================================================================
// checkStaleSSHConfig
// =============================================================================

func TestCheckStaleSSHConfig_NoState_OK(t *testing.T) {
	dir := t.TempDir()
	r := checkStaleSSHConfig(context.Background(),
		filepath.Join(dir, "config"),  // no file
		filepath.Join(dir, "no-keys"), // no dir
		&fakeSandboxLister{},
		true, false,
	)
	if r.Status != CheckOK {
		t.Fatalf("expected OK with no state, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckStaleSSHConfig_AllAlive_OK(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-alive1", "sb-alive2"})
	keysDir := makeKeyFiles(t, dir, []string{"sb-alive1", "sb-alive2"})
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-alive1"}, {SandboxID: "sb-alive2"},
	}}
	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, lister, true, false)
	if r.Status != CheckOK {
		t.Fatalf("expected OK when all sandboxes are live, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleSSHConfig_StaleEntries_DryRun_NoMutation: orphan entries
// are flagged but neither ssh-config nor key files are touched. dryRun=true
// even with deleteSSH=true must be a no-op.
func TestCheckStaleSSHConfig_StaleEntries_DryRun_NoMutation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-alive", "sb-ghost"})
	keysDir := makeKeyFiles(t, dir, []string{"sb-alive", "sb-ghost"})
	pre, _ := os.ReadFile(cfgPath)

	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}}}
	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, lister, true, true)

	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "sb-ghost") {
		t.Errorf("expected sb-ghost in report, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "[removed]") {
		t.Errorf("dry-run must not show [removed] markers, got: %s", r.Message)
	}
	post, _ := os.ReadFile(cfgPath)
	if string(pre) != string(post) {
		t.Errorf("dry-run must not mutate ssh-config; before=%q after=%q", pre, post)
	}
	for _, name := range []string{"sb-ghost", "sb-ghost.pub", "sb-alive", "sb-alive.pub"} {
		if _, err := os.Stat(filepath.Join(keysDir, name)); err != nil {
			t.Errorf("dry-run must not delete key file %s, got error: %v", name, err)
		}
	}
	if !strings.Contains(r.Remediation, "--dry-run=false --delete-ssh") {
		t.Errorf("expected dry-run remediation to point at --dry-run=false --delete-ssh, got: %s", r.Remediation)
	}
}

// TestCheckStaleSSHConfig_DryRunFalseWithoutDeleteSSH_NoMutation: explicit
// opt-in property — --dry-run=false alone is NOT enough. Same shape as
// every other --delete-* flag.
func TestCheckStaleSSHConfig_DryRunFalseWithoutDeleteSSH_NoMutation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-ghost"})
	keysDir := makeKeyFiles(t, dir, []string{"sb-ghost"})
	pre, _ := os.ReadFile(cfgPath)

	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, &fakeSandboxLister{}, false, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	post, _ := os.ReadFile(cfgPath)
	if string(pre) != string(post) {
		t.Errorf("--dry-run=false alone must NOT mutate ssh-config")
	}
	if _, err := os.Stat(filepath.Join(keysDir, "sb-ghost")); err != nil {
		t.Errorf("--dry-run=false alone must NOT delete key files")
	}
	if !strings.Contains(r.Remediation, "--delete-ssh") {
		t.Errorf("expected remediation to mention --delete-ssh, got: %s", r.Remediation)
	}
	if strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("remediation in --dry-run=false mode shouldn't repeat the flag, got: %s", r.Remediation)
	}
}

// TestCheckStaleSSHConfig_DeleteSSH_RemovesBoth: with --dry-run=false
// --delete-ssh, stale ssh-config entries AND their key files are removed.
// Live sandbox state is untouched.
func TestCheckStaleSSHConfig_DeleteSSH_RemovesBoth(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-alive", "sb-ghost"})
	keysDir := makeKeyFiles(t, dir, []string{"sb-alive", "sb-ghost"})
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}}}

	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, lister, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "1 cleaned, 0 failed") {
		t.Errorf("expected '1 cleaned, 0 failed' summary, got: %s", r.Message)
	}

	// sb-ghost ssh entry must be gone; sb-alive must remain.
	post, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(post), "Host km-sb-ghost") {
		t.Errorf("Host km-sb-ghost must be removed, got: %s", post)
	}
	if !strings.Contains(string(post), "Host km-sb-alive") {
		t.Errorf("Host km-sb-alive must be preserved, got: %s", post)
	}

	// sb-ghost key files must be gone; sb-alive's must remain.
	for _, name := range []string{"sb-ghost", "sb-ghost.pub"} {
		if _, err := os.Stat(filepath.Join(keysDir, name)); !os.IsNotExist(err) {
			t.Errorf("key file %s must be removed, stat err = %v", name, err)
		}
	}
	for _, name := range []string{"sb-alive", "sb-alive.pub"} {
		if _, err := os.Stat(filepath.Join(keysDir, name)); err != nil {
			t.Errorf("key file %s must be preserved, got: %v", name, err)
		}
	}
}

// TestCheckStaleSSHConfig_OrphanKeysWithoutSSHEntry: keys exist on disk but
// the ssh-config entry was hand-cleaned. The check must still classify the
// keys as orphans and remove them.
func TestCheckStaleSSHConfig_OrphanKeysWithoutSSHEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config") // no ssh-config file at all
	keysDir := makeKeyFiles(t, dir, []string{"sb-orphan-keys"})

	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, &fakeSandboxLister{}, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if _, err := os.Stat(filepath.Join(keysDir, "sb-orphan-keys")); !os.IsNotExist(err) {
		t.Error("orphan key file must be removed even without an ssh-config entry")
	}
}

// TestCheckStaleSSHConfig_OrphanSSHWithoutKeys: ssh-config entry exists but
// the key files were hand-cleaned. The check must still remove the
// ssh-config entry.
func TestCheckStaleSSHConfig_OrphanSSHWithoutKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-orphan-ssh"})
	keysDir := filepath.Join(dir, "no-keys-dir") // does not exist

	r := checkStaleSSHConfig(context.Background(), cfgPath, keysDir, &fakeSandboxLister{}, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	post, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(post), "Host km-sb-orphan-ssh") {
		t.Error("ssh-config entry must be removed even without matching key files")
	}
}

// TestCheckStaleSSHConfig_NonKMAliasInsideMarkers_Untouched verifies the
// safety property: an operator-edited non-km alias inside the managed
// markers must NEVER be removed by the cleanup pass.
func TestCheckStaleSSHConfig_NonKMAliasInsideMarkers_Untouched(t *testing.T) {
	dir := t.TempDir()
	cfgPath := makeSSHFixture(t, dir, []string{"sb-ghost"})
	// Inject a non-km Host inside the markers.
	raw, _ := os.ReadFile(cfgPath)
	mutated := strings.Replace(string(raw),
		"# END km vscode hosts",
		"Host my-laptop\n  User me\n# END km vscode hosts", 1)
	if err := os.WriteFile(cfgPath, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}

	r := checkStaleSSHConfig(context.Background(), cfgPath, filepath.Join(dir, "no-keys"),
		&fakeSandboxLister{}, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	post, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(post), "Host my-laptop") {
		t.Error("non-km alias inside markers must be preserved across cleanup")
	}
	if strings.Contains(string(post), "Host km-sb-ghost") {
		t.Error("km-sb-ghost should have been removed")
	}
}
