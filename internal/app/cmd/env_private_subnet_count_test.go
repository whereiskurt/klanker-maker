package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestEnvExport_PrivateSubnetCount_EmittedOnlyWhenSet mirrors the
// KM_NAT_GATEWAY_ENABLED contract: `eval $(km env)` is the documented way to drive
// terragrunt directly, so the var must be emitted there — but only when the
// operator explicitly set the key, or an absent block would stop being dormant.
func TestEnvExport_PrivateSubnetCount_EmittedOnlyWhenSet(t *testing.T) {
	one := 1

	tests := []struct {
		name    string
		cfg     *config.Config
		wantSub string // "" means: must NOT appear
	}{
		{
			name: "absent key emits nothing",
			cfg:  &config.Config{Domain: "example.com", PrimaryRegion: "us-east-1"},
		},
		{
			name: "explicit value is emitted",
			cfg: &config.Config{
				Domain:        "example.com",
				PrimaryRegion: "us-east-1",
				Network:       config.NetworkConfig{PrivateSubnetCount: &one},
			},
			wantSub: "export KM_PRIVATE_SUBNET_COUNT=1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := runEnvExport(tc.cfg, &buf, false); err != nil {
				t.Fatalf("runEnvExport: %v", err)
			}
			out := buf.String()

			if tc.wantSub == "" {
				if strings.Contains(out, "KM_PRIVATE_SUBNET_COUNT") {
					t.Errorf("KM_PRIVATE_SUBNET_COUNT emitted for an absent key:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, tc.wantSub) {
				t.Errorf("%s not found in:\n%s", tc.wantSub, out)
			}
		})
	}
}
