package cmd_test

// bootstrap_scp_test.go — the SCP is rendered by pkg/scp from km-config.yaml.
// Trust patterns stay prefix-agnostic (`*-ttl-handler`, not `km-ttl-handler`) so
// installs sharing an application account do not deny each other's control
// plane; patterns a sandbox role could match were removed (see pkg/scp).

import (
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestBuildSCPPolicySize(t *testing.T) {
	for _, prefix := range []string{"km", "kph", "whereiskurt"} {
		t.Run(prefix, func(t *testing.T) {
			got, err := cmd.BuildSCPPolicyJSON(&config.Config{
				ResourcePrefix: prefix, ApplicationAccountID: "123456789012", PrimaryRegion: "us-east-1",
			})
			if err != nil {
				t.Fatal(err)
			}
			// Indented display form; the deployed compact form is smaller.
			if len(got) > 5000 {
				t.Errorf("policy %d bytes for prefix %q", len(got), prefix)
			}
			if strings.Contains(got, ":role/"+prefix+"-ttl-handler") {
				t.Errorf("prefix-bound trust pattern for %q; want *-ttl-handler", prefix)
			}
		})
	}
}

func TestBuildSCPPolicyPrefix(t *testing.T) {
	got, err := cmd.BuildSCPPolicyJSON(&config.Config{
		ResourcePrefix: "km", ApplicationAccountID: "123456789012", PrimaryRegion: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"*-budget-enforcer-*", "*-ttl-handler", "*-create-handler", "bedrock-mantle:*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Removed: dead ECS-era roles, and patterns that matched the sandbox role.
	for _, gone := range []string{"*-ecs-spot-handler", "*-ec2spot-ssm-*", "*-github-token-refresher-*", "*-provisioner-*", "*-lifecycle-*"} {
		if strings.Contains(got, gone) {
			t.Errorf("policy still trusts %q", gone)
		}
	}
}
