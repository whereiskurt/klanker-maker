package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: write a file and return its path.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// sha256("hello\n") — the digest every case below is anchored on.
const helloBody = "hello\n"
const helloSHA = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

func TestVerifySHA256_MatchingDigestPasses(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", helloBody)
	sums := writeTemp(t, dir, "checksums.txt", helloSHA+"  artifact.tar.gz\n")

	if err := verifySHA256(art, sums, "artifact.tar.gz"); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

func TestVerifySHA256_TamperedContentFails(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", "hello\nEXTRA")
	sums := writeTemp(t, dir, "checksums.txt", helloSHA+"  artifact.tar.gz\n")

	err := verifySHA256(art, sums, "artifact.tar.gz")
	if err == nil {
		t.Fatal("tampered content was accepted")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a mismatch error, got: %v", err)
	}
}

// The trap this whole helper exists to close: `sha256sum -c --ignore-missing`
// succeeds VACUOUSLY when no line in the checksums file names the artifact, so
// an upstream that renames its assets would silently stop verifying rather than
// fail the build. A missing entry must be a hard error, never a pass.
func TestVerifySHA256_MissingEntryIsAnErrorNotAVacuousPass(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", helloBody)
	// Checksums file is well-formed but names a DIFFERENT asset.
	sums := writeTemp(t, dir, "checksums.txt", helloSHA+"  some-other-asset.tar.gz\n")

	err := verifySHA256(art, sums, "artifact.tar.gz")
	if err == nil {
		t.Fatal("missing checksum entry passed vacuously — the exact failure this guards")
	}
	if !strings.Contains(err.Error(), "no checksum published") {
		t.Fatalf("want a 'no checksum published' error, got: %v", err)
	}
}

// A suffix match is not a match: an entry for "evil-artifact.tar.gz" must not
// satisfy a request for "artifact.tar.gz".
func TestVerifySHA256_EntryNameMatchIsExact(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", helloBody)
	sums := writeTemp(t, dir, "checksums.txt", helloSHA+"  evil-artifact.tar.gz\n")

	if err := verifySHA256(art, sums, "artifact.tar.gz"); err == nil {
		t.Fatal("suffix match was accepted as the artifact's own entry")
	}
}

func TestVerifySHA256_UppercaseDigestIsAccepted(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", helloBody)
	sums := writeTemp(t, dir, "checksums.txt", strings.ToUpper(helloSHA)+"  artifact.tar.gz\n")

	if err := verifySHA256(art, sums, "artifact.tar.gz"); err != nil {
		t.Fatalf("uppercase digest should verify, got: %v", err)
	}
}

func TestVerifySHA256_UnreadableChecksumsFileFails(t *testing.T) {
	dir := t.TempDir()
	art := writeTemp(t, dir, "artifact.tar.gz", helloBody)

	if err := verifySHA256(art, filepath.Join(dir, "absent.txt"), "artifact.tar.gz"); err == nil {
		t.Fatal("a missing checksums file must fail, not skip verification")
	}
}
