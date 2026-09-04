package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var errDecryptStub = errors.New("kms 403")

func osWriteFile(p string) error { return os.WriteFile(p, []byte("sops: {}\n"), 0o400) }

func contains(hay, needle string) bool { return bytes.Contains([]byte(hay), []byte(needle)) }

// stubDecrypt swaps the sops call out so these tests need no KMS and no age key.
func stubDecrypt(t *testing.T, yaml string, err error) {
	t.Helper()
	prev := decryptFile
	decryptFile = func(path, format string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(yaml), nil
	}
	t.Cleanup(func() { decryptFile = prev })
}

func writeCipher(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := os.WriteFile(p, []byte("sops: {}\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBundle_TopLevelKeys(t *testing.T) {
	stubDecrypt(t, "ANTHROPIC_API_KEY: sk-ant-xyz\nOPENAI_API_KEY: sk-oai-abc\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Keys(); !reflect.DeepEqual(got, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}) {
		t.Errorf("Keys() = %v", got)
	}
	if got := b.Get("ANTHROPIC_API_KEY"); !bytes.Equal(got, []byte("sk-ant-xyz")) {
		t.Errorf("Get() = %q", got)
	}
}

func TestLoadBundle_DropsSopsMetadata(t *testing.T) {
	// sops embeds its own metadata; it is not a secret and must never be served.
	stubDecrypt(t, "API_KEY: v\nsops:\n  kms: []\n_meta: whatever\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Keys(); !reflect.DeepEqual(got, []string{"API_KEY"}) {
		t.Errorf("Keys() = %v, want only API_KEY", got)
	}
}

func TestLoadBundle_NonStringScalarsStringified(t *testing.T) {
	stubDecrypt(t, "PORT: 5432\nDEBUG: true\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Get("PORT"); !bytes.Equal(got, []byte("5432")) {
		t.Errorf("PORT = %q, want 5432", got)
	}
	if got := b.Get("DEBUG"); !bytes.Equal(got, []byte("true")) {
		t.Errorf("DEBUG = %q, want true", got)
	}
}

func TestZero_OverwritesValues(t *testing.T) {
	// The whole point: values are []byte precisely so they CAN be overwritten.
	stubDecrypt(t, "API_KEY: supersecret\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	v := b.Get("API_KEY")
	b.Zero()

	if bytes.Contains(v, []byte("supersecret")) {
		t.Error("Zero() left plaintext in the backing array")
	}
	if len(b.Keys()) != 0 {
		t.Error("Zero() left keys behind")
	}
}

func TestLoadBundle_DecryptErrorPropagates(t *testing.T) {
	// Fail closed and loudly; never return a partial or empty bundle as success.
	sentinel := errors.New("kms 403")
	stubDecrypt(t, "", sentinel)
	if _, err := LoadBundle(writeCipher(t)); err == nil {
		t.Fatal("LoadBundle succeeded despite a decrypt failure")
	}
}

func TestLoadBundle_MissingCiphertext(t *testing.T) {
	stubDecrypt(t, "API_KEY: v\n", nil)
	if _, err := LoadBundle(filepath.Join(t.TempDir(), "absent.enc.yaml")); err == nil {
		t.Fatal("LoadBundle succeeded with no ciphertext on disk")
	}
}
