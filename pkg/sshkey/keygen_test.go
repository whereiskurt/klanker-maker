package sshkey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// TestGenerateAndWrite_ModePriv asserts the private key file is created with mode 0600.
func TestGenerateAndWrite_ModePriv(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if _, err := GenerateAndWrite(privPath, pubPath, "test-comment"); err != nil {
		t.Fatalf("GenerateAndWrite failed: %v", err)
	}
	info, err := os.Stat(privPath)
	if err != nil {
		t.Fatalf("stat privPath: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("privkey mode = %o; want 0600", got)
	}
}

// TestGenerateAndWrite_ModePub asserts the public key file is created with mode 0644.
func TestGenerateAndWrite_ModePub(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if _, err := GenerateAndWrite(privPath, pubPath, "test-comment"); err != nil {
		t.Fatalf("GenerateAndWrite failed: %v", err)
	}
	info, err := os.Stat(pubPath)
	if err != nil {
		t.Fatalf("stat pubPath: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("pubkey mode = %o; want 0644", got)
	}
}

// TestGenerateAndWrite_PubKeyParses asserts the returned pubLine and .pub file
// contents both parse cleanly via ssh.ParseAuthorizedKey.
func TestGenerateAndWrite_PubKeyParses(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	pubLine, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	if err != nil {
		t.Fatalf("GenerateAndWrite failed: %v", err)
	}
	// Parse returned line (add newline so ParseAuthorizedKey accepts it).
	if _, _, _, _, err := gossh.ParseAuthorizedKey([]byte(pubLine + "\n")); err != nil {
		t.Fatalf("returned pubLine does not parse: %v; line=%q", err, pubLine)
	}
	// Parse file contents.
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatalf("ReadFile pubPath: %v", err)
	}
	if _, _, _, _, err := gossh.ParseAuthorizedKey(raw); err != nil {
		t.Fatalf("pubPath file does not parse: %v; content=%q", err, raw)
	}
}

// TestGenerateAndWrite_Comment asserts the pubLine ends with " <comment>"
// (space-separated; the MarshalAuthorizedKey-loses-comment workaround from
// 73-RESEARCH.md Pitfall 1).
func TestGenerateAndWrite_Comment(t *testing.T) {
	const comment = "km-sb-abc123"
	dir := t.TempDir()
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	pubLine, err := GenerateAndWrite(privPath, pubPath, comment)
	if err != nil {
		t.Fatalf("GenerateAndWrite failed: %v", err)
	}
	if !strings.HasSuffix(pubLine, " "+comment) {
		t.Fatalf("pubLine missing comment suffix: %q", pubLine)
	}
}

// TestGenerateAndWrite_NoTrailingNewline asserts the returned pubContent has no
// trailing "\n" (Pitfall 2 from research).
func TestGenerateAndWrite_NoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "id_ed25519")
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	pubLine, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	if err != nil {
		t.Fatalf("GenerateAndWrite failed: %v", err)
	}
	if strings.HasSuffix(pubLine, "\n") {
		t.Fatalf("pubLine has trailing newline: %q", pubLine)
	}
}

// TestGenerateAndWrite_CreatesParentDir asserts that calling GenerateAndWrite
// against a privPath whose parent dir doesn't exist results in MkdirAll with
// mode 0700 on the parent directory.
func TestGenerateAndWrite_CreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "deep", "nest", "keys")
	privPath := filepath.Join(nested, "sb-test")
	pubPath := privPath + ".pub"

	_, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	if err != nil {
		t.Fatalf("GenerateAndWrite returned error: %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
	if info.Mode()&0o777 != 0o700 {
		t.Fatalf("parent dir mode = %o, want 0o700", info.Mode()&0o777)
	}
}

// TestGenerateAndWrite_Idempotent asserts that calling GenerateAndWrite twice
// with the same paths succeeds without error and produces a DIFFERENT keypair
// on the second call (because each call uses new randomness).
func TestGenerateAndWrite_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	privPath := filepath.Join(tmp, "sb-test")
	pubPath := privPath + ".pub"

	first, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first == second {
		t.Fatal("second call produced the same pubkey — randomness is broken")
	}
}
