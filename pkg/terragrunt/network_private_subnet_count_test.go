// Structural regression test over the live network unit's private_subnet_count
// wiring. Like network_module_v110_test.go, this reads terragrunt.hcl as plain
// text and never shells out to terraform — running terraform in a module source
// directory creates the gitignored lock-drift artifacts that bite `km init`.
package terragrunt_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNetworkUnit_PrivateSubnetCountWiring pins the three properties that make
// network.private_subnet_count both effective and dormant-by-default.
func TestNetworkUnit_PrivateSubnetCountWiring(t *testing.T) {
	repoRoot := findRepoRoot(t)
	src := readFileString(t, filepath.Join(repoRoot, "infra", "live", "use1", "network", "terragrunt.hcl"))

	t.Run("private_subnets_cidr is sliced, not a literal list", func(t *testing.T) {
		if !strings.Contains(src, "private_subnets_cidr    = slice(local.all_private_subnets_cidr, 0, local.private_subnet_count)") {
			t.Errorf("private_subnets_cidr is not wired through slice(); the knob cannot take effect:\n%s", src)
		}
	})

	t.Run("reads KM_PRIVATE_SUBNET_COUNT", func(t *testing.T) {
		if !strings.Contains(src, `get_env("KM_PRIVATE_SUBNET_COUNT"`) {
			t.Error("live network unit does not read KM_PRIVATE_SUBNET_COUNT")
		}
	})

	// The dormancy contract, and the reason the default is an expression rather
	// than a literal "4": with the env var unset the slice bound must equal the
	// full list length, so an absent km-config.yaml key is byte-identical to the
	// pre-knob behaviour AND stays correct if a fifth CIDR is ever added.
	t.Run("defaults to the full CIDR list length, not a hardcoded number", func(t *testing.T) {
		if !strings.Contains(src, `tostring(length(local.all_private_subnets_cidr))`) {
			t.Errorf("KM_PRIVATE_SUBNET_COUNT default is not derived from the CIDR list length — "+
				"a hardcoded default silently truncates if the list grows:\n%s", src)
		}
		if strings.Contains(src, `get_env("KM_PRIVATE_SUBNET_COUNT", "4")`) {
			t.Error(`default is hardcoded to "4"; derive it from length(local.all_private_subnets_cidr)`)
		}
	})

	// Public subnets and AZ count are deliberately NOT governed by this knob:
	// narrowing them would change placement for ordinary (public) sandboxes, which
	// is a much larger blast radius than the private-only tradeoff being made here.
	t.Run("public subnets and AZ count are untouched", func(t *testing.T) {
		if !strings.Contains(src, `public_subnets_cidr     = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24"]`) {
			t.Error("public_subnets_cidr changed — private_subnet_count must not affect public placement")
		}
		if !strings.Contains(src, "availability_zone_count = 4") {
			t.Error("availability_zone_count changed — private_subnet_count must not affect AZ count")
		}
	})
}
