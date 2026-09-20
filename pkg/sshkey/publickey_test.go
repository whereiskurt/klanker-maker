package sshkey

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicKeyLine_MatchesGenerateAndWrite(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "k")
	pub := priv + ".pub"
	want, err := GenerateAndWrite(priv, pub, "km-sbx-1")
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PublicKeyLine(pem, "km-sbx-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("PublicKeyLine = %q, want %q", got, want)
	}
}

func TestPublicKeyLine_RejectsGarbage(t *testing.T) {
	if _, err := PublicKeyLine([]byte("not a key"), "c"); err == nil {
		t.Error("expected error for garbage input")
	}
}
