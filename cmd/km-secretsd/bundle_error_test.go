package main

import (
	"strings"
	"testing"
)

// TestLoadBundle_MalformedYAMLErrorOmitsPlaintext pins the Finding-1 fix: a
// malformed decrypted bundle must not leak fragments of its own plaintext
// through the returned error. yaml.Unmarshal errors can quote the offending
// source line verbatim, and that source is decrypted secret content — so
// LoadBundle must never propagate the decoder's error text.
func TestLoadBundle_MalformedYAMLErrorOmitsPlaintext(t *testing.T) {
	const distinctivePlaintext = "sk-live-VERYSECRETTOKEN12345"
	// Invalid YAML (unterminated flow mapping) that still contains a
	// distinctive plaintext token the decoder might otherwise echo back.
	malformed := "API_KEY: " + distinctivePlaintext + "\nBROKEN: [unterminated\n"
	stubDecrypt(t, malformed, nil)

	_, err := LoadBundle(writeCipher(t))
	if err == nil {
		t.Fatal("LoadBundle succeeded on malformed YAML")
	}
	if strings.Contains(err.Error(), distinctivePlaintext) {
		t.Errorf("LoadBundle error leaked plaintext: %v", err)
	}
}
