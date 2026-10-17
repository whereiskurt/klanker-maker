package cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// envRecordingTerragrunt records KM_SLACK_DEFAULT_ROUTE_ALLOW as it stands at
// each Apply — what terragrunt's get_env would read for that module.
type envRecordingTerragrunt struct {
	fakeTerragrunt
	allowAtApply map[string]string
}

func (f *envRecordingTerragrunt) Apply(ctx context.Context, dir string) error {
	if f.allowAtApply == nil {
		f.allowAtApply = map[string]string{}
	}
	f.allowAtApply[filepath.Base(dir)] = os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW")
	return f.fakeTerragrunt.Apply(ctx, dir)
}

const slackInitRouteProfile = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: ir-bot
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      inbound:
        enabled: true
`

func slackInitRouteDeps(t *testing.T, profileBody string, cfgEmails ...string) (*cmd.SlackCmdDeps, *envRecordingTerragrunt) {
	t.Helper()
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	p := filepath.Join(t.TempDir(), "ir-bot.yaml")
	if err := os.WriteFile(p, []byte(profileBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: cfgEmails}
	tg := &envRecordingTerragrunt{}
	deps := buildSlackTestDeps(&fakeSlackInitAPI{createID: "C0123ABC"}, newFakeSSM(nil), nil,
		&fakePrompter{ordered: []string{"xoxb-test-token", "ops@example.com"}}, &fakeBridgePoster{})
	deps.Terragrunt = tg
	deps.Cfg = cfg
	return deps, tg
}

// km slack init's step-6 bridge apply sees the effective route allowlist —
// the route profile's allowEmails over a different km-config.yaml list.
func TestSlackInit_BridgeApplyCarriesProfileRouteList(t *testing.T) {
	body := slackInitRouteProfile + "        allowEmails:\n          - Kurt@Example.com\n          - \"*@corp.example\"\n"
	deps, tg := slackInitRouteDeps(t, body, "stale@example.com")
	if err := cmd.RunSlackInit(context.Background(), deps, cmd.SlackInitOpts{SharedChannel: "km-notifications"}); err != nil {
		t.Fatalf("RunSlackInit: %v", err)
	}
	if got := tg.allowAtApply["lambda-slack-bridge"]; got != "kurt@example.com,*@corp.example" {
		t.Fatalf("KM_SLACK_DEFAULT_ROUTE_ALLOW at bridge apply = %q, want the profile list", got)
	}
}

// A route with no allowlist anywhere stops km slack init before any apply:
// re-applying would deploy the bridge with the route silently OFF.
func TestSlackInit_RouteWithoutAllowlistFailsClosed(t *testing.T) {
	deps, tg := slackInitRouteDeps(t, slackInitRouteProfile)
	err := cmd.RunSlackInit(context.Background(), deps, cmd.SlackInitOpts{SharedChannel: "km-notifications"})
	if err == nil || !strings.Contains(err.Error(), "allowEmails") || !strings.Contains(err.Error(), "allow_emails") {
		t.Fatalf("want a fail-closed error naming both lists, got %v", err)
	}
	if len(tg.applied) != 0 {
		t.Fatalf("no module may be applied, got %v", tg.applied)
	}
}
