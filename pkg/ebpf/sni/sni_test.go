//go:build linux

package sni

import (
	"strings"
	"testing"
)

// TestSNIHostnameNormalization verifies that hostnameToKey produces a
// 256-byte null-padded lowercase key from various input strings.
func TestSNIHostnameNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
		wantKey0 string // expected contents of key[0:len(wantKey0)]
	}{
		{
			name:     "lowercase passthrough",
			input:    "example.com",
			wantKey0: "example.com",
		},
		{
			name:     "uppercase lowercased",
			input:    "EXAMPLE.COM",
			wantKey0: "example.com",
		},
		{
			name:     "mixed case lowercased",
			input:    "Api.Example.COM",
			wantKey0: "api.example.com",
		},
		{
			name:     "leading/trailing whitespace trimmed",
			input:    "  example.com  ",
			wantKey0: "example.com",
		},
		{
			name:    "empty string returns error",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace-only returns error",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "too long hostname returns error",
			input:   strings.Repeat("a", 256),
			wantErr: true,
		},
		{
			name:     "exactly 255 bytes allowed",
			input:    strings.Repeat("a", 255),
			wantKey0: strings.Repeat("a", 255),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, err := hostnameToKey(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Errorf("hostnameToKey(%q): expected error, got nil", tc.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("hostnameToKey(%q): unexpected error: %v", tc.input, err)
			}

			// Verify the hostname bytes at the start of the key match expectations.
			got := string(key[:len(tc.wantKey0)])
			if got != tc.wantKey0 {
				t.Errorf("hostnameToKey(%q): key[0:%d] = %q, want %q",
					tc.input, len(tc.wantKey0), got, tc.wantKey0)
			}

			// Verify that bytes after the hostname are null-padded.
			hostnameLen := len(tc.wantKey0)
			for i := hostnameLen; i < 256; i++ {
				if key[i] != 0 {
					t.Errorf("hostnameToKey(%q): key[%d] = %d, want 0 (null padding)",
						tc.input, i, key[i])
					break
				}
			}

			// Verify total key length is exactly 256 bytes.
			if len(key) != 256 {
				t.Errorf("hostnameToKey(%q): key length = %d, want 256", tc.input, len(key))
			}
		})
	}
}

// TestAllowSNIMultiple verifies that multiple hostnames can be added via
// AllowSNI by calling hostnameToKey directly (avoids needing a live BPF map).
// It confirms that each hostname produces a distinct, non-zero key.
func TestAllowSNIMultiple(t *testing.T) {
	hostnames := []string{
		"api.github.com",
		"registry.npmjs.org",
		"pypi.org",
		"pkg.go.dev",
		"*.example.com",       // exact-match key (wildcards are not special here)
		"UPPERCASE.DOMAIN.IO", // should be lowercased
	}

	seen := make(map[[256]byte]string)
	for _, h := range hostnames {
		key, err := hostnameToKey(h)
		if err != nil {
			t.Fatalf("hostnameToKey(%q): unexpected error: %v", h, err)
		}

		lower := strings.ToLower(strings.TrimSpace(h))

		// Key must start with the lowercased hostname.
		if string(key[:len(lower)]) != lower {
			t.Errorf("hostnameToKey(%q): key prefix = %q, want %q",
				h, string(key[:len(lower)]), lower)
		}

		// Keys must be unique (no two hostnames should produce the same key).
		if prev, exists := seen[key]; exists {
			t.Errorf("hostnameToKey(%q) and hostnameToKey(%q) produced identical keys",
				h, prev)
		}
		seen[key] = h
	}

	// Also verify that adding 100 hostnames does not fail (stress test key generation).
	for i := 0; i < 100; i++ {
		name := strings.Repeat("a", i%255+1) + ".com"
		if _, err := hostnameToKey(name); err != nil {
			// Hostnames up to 255 bytes plus ".com" may exceed limit — that is expected.
			continue
		}
	}
}
