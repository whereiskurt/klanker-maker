package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/getsops/sops/v3/decrypt"
	"gopkg.in/yaml.v3"
)

// decryptFile is the sops entry point, as a variable so tests can stub it.
// Using the Go API rather than shelling to /opt/km/bin/sops is deliberate: the
// plaintext never transits a pipe or a second process's memory, and the buffer
// stays ours to zero.
//
// If getsops/sops/v3/decrypt will not build under CGO_ENABLED=0 (required for
// the sidecar cross-compile), replace this with an exec of /opt/km/bin/sops and
// record it as debt — the zeroing guarantee then covers only our side of the pipe.
var decryptFile = decrypt.File

// reservedKeys are sops' own metadata, never secrets, never served.
var reservedKeys = map[string]bool{"sops": true, "_meta": true}

// Bundle holds decrypted secrets as []byte so they can actually be overwritten.
//
// Go strings are immutable and may be copied freely by the runtime, so a
// map[string]string cannot be zeroed at all and the claim would be decorative.
// Zeroing here covers the decrypted YAML buffer and these per-key values; it
// cannot cover a response already serialised onto a socket, nor the environment
// of the child process.
type Bundle struct {
	vals map[string][]byte
	raw  []byte // the decrypted YAML, retained solely so Zero can overwrite it
}

// LoadBundle decrypts the SOPS bundle at path. The caller MUST call Zero.
func LoadBundle(path string) (*Bundle, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ciphertext unreadable at %s: %w", path, err)
	}

	plain, err := decryptFile(path, "yaml")
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %s: %w", path, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(plain, &doc); err != nil {
		zero(plain)
		return nil, fmt.Errorf("parse decrypted bundle: %w", err)
	}

	b := &Bundle{vals: make(map[string][]byte, len(doc)), raw: plain}
	for k, v := range doc {
		if reservedKeys[k] {
			continue
		}
		switch v.(type) {
		case map[string]any, []any, nil:
			// Only top-level scalars become env vars, matching the Phase 89
			// --output-type dotenv behaviour this replaces.
			continue
		}
		b.vals[k] = []byte(fmt.Sprint(v))
	}
	return b, nil
}

// Keys returns the bundle's key names, sorted. Names only — never values.
func (b *Bundle) Keys() []string {
	out := make([]string, 0, len(b.vals))
	for k := range b.vals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns the value for key, or nil. The slice aliases the bundle's
// storage: it is invalid after Zero.
func (b *Bundle) Get(key string) []byte { return b.vals[key] }

// Zero overwrites every value and the decrypted YAML buffer, then drops them.
func (b *Bundle) Zero() {
	for k, v := range b.vals {
		zero(v)
		delete(b.vals, k)
	}
	zero(b.raw)
	b.raw = nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
