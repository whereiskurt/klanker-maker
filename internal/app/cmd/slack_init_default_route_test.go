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

// envRecordingTerragrunt records KM_SLACK_DEFAULT_ROUTE_ALLOW and _PROFILE_ALLOW
// as they stand at each Apply — what terragrunt's get_env would read.
type envRecordingTerragrunt struct {
	fakeTerragrunt
	allowAtApply        map[string]string
	profileAllowAtApply map[string]string
}

func (f *envRecordingTerragrunt) Apply(ctx context.Context, dir string) error {
	if f.allowAtApply == nil {
		f.allowAtApply = map[string]string{}
		f.profileAllowAtApply = map[string]string{}
	}
	f.allowAtApply[filepath.Base(dir)] = os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW")
	f.profileAllowAtApply[filepath.Base(dir)] = os.Getenv("KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW")
	return f.fakeTerragrunt.Apply(ctx, dir)
}

// routeProfileRecorder records pre-staged route profiles (S3ProfileUploader).
type routeProfileRecorder struct{ keys map[string][]byte }

func (r *routeProfileRecorder) PutObject(_ context.Context, _, key string, body []byte) error {
	if r.keys == nil {
		r.keys = map[string][]byte{}
	}
	r.keys[key] = body
	return nil
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
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW", "KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW"} {
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

// km slack init's step-6 bridge apply sees both route lists: ALLOW is the
// km-config.yaml install list, PROFILE_ALLOW the route profile's allowEmails.
// (Before the two-list model the profile list replaced ALLOW.)
func TestSlackInit_BridgeApplyCarriesBothRouteLists(t *testing.T) {
	body := slackInitRouteProfile + "        allowEmails:\n          - Kurt@Example.com\n          - \"*@corp.example\"\n"
	deps, tg := slackInitRouteDeps(t, body, "install@example.com")
	if err := cmd.RunSlackInit(context.Background(), deps, cmd.SlackInitOpts{SharedChannel: "km-notifications"}); err != nil {
		t.Fatalf("RunSlackInit: %v", err)
	}
	if got := tg.profileAllowAtApply["lambda-slack-bridge"]; got != "kurt@example.com,*@corp.example" {
		t.Fatalf("KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW at bridge apply = %q, want the profile list", got)
	}
	if got := tg.allowAtApply["lambda-slack-bridge"]; got != "install@example.com" {
		t.Fatalf("KM_SLACK_DEFAULT_ROUTE_ALLOW at bridge apply = %q, want the install list", got)
	}
}

// km slack init re-stages the route profile with the same step as full km init,
// so a cold-created box's row list equals the list the bridge gates on.
func TestSlackInit_RestagesRouteProfile(t *testing.T) {
	body := slackInitRouteProfile + "        allowEmails:\n          - kurt@example.com\n"
	deps, _ := slackInitRouteDeps(t, body)
	deps.Cfg.ArtifactsBucket = "artifacts"
	up := &routeProfileRecorder{}
	deps.RouteProfileUploader = up
	if err := cmd.RunSlackInit(context.Background(), deps, cmd.SlackInitOpts{SharedChannel: "km-notifications"}); err != nil {
		t.Fatalf("RunSlackInit: %v", err)
	}
	got, ok := up.keys["slack-profiles/ir-bot/.km-profile.yaml"]
	if !ok || !strings.Contains(string(got), "kurt@example.com") {
		t.Fatalf("route profile not re-staged; keys=%v", up.keys)
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
