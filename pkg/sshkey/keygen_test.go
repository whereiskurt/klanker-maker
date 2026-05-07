package sshkey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	// golang.org/x/crypto/ssh is imported for the ParseAuthorizedKey assertion
	// that Wave 1 (Plan 73-01) will activate when t.Skip is removed.
	_ "golang.org/x/crypto/ssh"
)

// TestGenerateAndWrite_ModePriv asserts the private key file is created with mode 0600.
func TestGenerateAndWrite_ModePriv(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// if _, err := GenerateAndWrite(privPath, pubPath, "test-comment"); err != nil {
	// 	t.Fatalf("GenerateAndWrite failed: %v", err)
	// }
	// info, err := os.Stat(privPath)
	// if err != nil {
	// 	t.Fatalf("stat privPath: %v", err)
	// }
	// if got := info.Mode().Perm(); got != 0600 {
	// 	t.Fatalf("privkey mode = %o; want 0600", got)
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestGenerateAndWrite_ModePub asserts the public key file is created with mode 0644.
func TestGenerateAndWrite_ModePub(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// if _, err := GenerateAndWrite(privPath, pubPath, "test-comment"); err != nil {
	// 	t.Fatalf("GenerateAndWrite failed: %v", err)
	// }
	// info, err := os.Stat(pubPath)
	// if err != nil {
	// 	t.Fatalf("stat pubPath: %v", err)
	// }
	// if got := info.Mode().Perm(); got != 0644 {
	// 	t.Fatalf("pubkey mode = %o; want 0644", got)
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestGenerateAndWrite_PubKeyParses asserts the returned pubLine and .pub file
// contents both parse cleanly via ssh.ParseAuthorizedKey.
func TestGenerateAndWrite_PubKeyParses(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// pubLine, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	// if err != nil {
	// 	t.Fatalf("GenerateAndWrite failed: %v", err)
	// }
	// // Parse returned line.
	// if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine)); err != nil {
	// 	t.Fatalf("returned pubLine does not parse: %v; line=%q", err, pubLine)
	// }
	// // Parse file contents.
	// raw, err := os.ReadFile(pubPath)
	// if err != nil {
	// 	t.Fatalf("ReadFile pubPath: %v", err)
	// }
	// if _, _, _, _, err := ssh.ParseAuthorizedKey(raw); err != nil {
	// 	t.Fatalf("pubPath file does not parse: %v; content=%q", err, raw)
	// }
	_ = strings.HasSuffix
}

// TestGenerateAndWrite_Comment asserts the pubLine ends with " <comment>"
// (space-separated; the MarshalAuthorizedKey-loses-comment workaround from
// 73-RESEARCH.md Pitfall 1).
func TestGenerateAndWrite_Comment(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// const comment = "km-sb-abc123"
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// pubLine, err := GenerateAndWrite(privPath, pubPath, comment)
	// if err != nil {
	// 	t.Fatalf("GenerateAndWrite failed: %v", err)
	// }
	// if !strings.HasSuffix(pubLine, " "+comment) {
	// 	t.Fatalf("pubLine missing comment suffix: %q", pubLine)
	// }
	_ = strings.HasSuffix
}

// TestGenerateAndWrite_NoTrailingNewline asserts the returned pubContent has no
// trailing "\n" (Pitfall 2 from research).
func TestGenerateAndWrite_NoTrailingNewline(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// pubLine, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	// if err != nil {
	// 	t.Fatalf("GenerateAndWrite failed: %v", err)
	// }
	// if strings.HasSuffix(pubLine, "\n") {
	// 	t.Fatalf("pubLine has trailing newline: %q", pubLine)
	// }
	_ = strings.HasSuffix
}

// TestGenerateAndWrite_CreatesParentDir asserts that calling GenerateAndWrite
// against a privPath whose parent dir doesn't exist results in MkdirAll with
// mode 0700 on the parent directory.
func TestGenerateAndWrite_CreatesParentDir(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "nested", "subdir", "id_ed25519")
	// pubPath := filepath.Join(dir, "nested", "subdir", "id_ed25519.pub")
	// if _, err := GenerateAndWrite(privPath, pubPath, "km-sb-test"); err != nil {
	// 	t.Fatalf("GenerateAndWrite failed on non-existent parent: %v", err)
	// }
	// info, err := os.Stat(filepath.Join(dir, "nested", "subdir"))
	// if err != nil {
	// 	t.Fatalf("stat parent dir: %v", err)
	// }
	// if got := info.Mode().Perm(); got != 0700 {
	// 	t.Fatalf("parent dir mode = %o; want 0700", got)
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestGenerateAndWrite_Idempotent asserts that calling GenerateAndWrite twice
// with the same paths succeeds without error and produces a DIFFERENT keypair
// on the second call (because each call uses new randomness).
func TestGenerateAndWrite_Idempotent(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-01): implement GenerateAndWrite")
	// dir := t.TempDir()
	// privPath := filepath.Join(dir, "id_ed25519")
	// pubPath := filepath.Join(dir, "id_ed25519.pub")
	// pub1, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	// if err != nil {
	// 	t.Fatalf("first call failed: %v", err)
	// }
	// pub2, err := GenerateAndWrite(privPath, pubPath, "km-sb-test")
	// if err != nil {
	// 	t.Fatalf("second call failed: %v", err)
	// }
	// // A new keypair is generated each time — the public keys must differ.
	// if pub1 == pub2 {
	// 	t.Fatal("expected second call to produce a different keypair")
	// }
	_ = strings.HasSuffix
}
