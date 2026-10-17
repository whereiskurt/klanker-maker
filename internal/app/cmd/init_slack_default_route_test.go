package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func writeProfile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const routeProfileOK = `apiVersion: klankermaker.ai/v1alpha2
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

const routeProfileNoInbound = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: ir-bot
spec:
  notification:
    slack:
      enabled: true
`

func TestValidateSlackDefaultRouteProfile(t *testing.T) {
	dir := t.TempDir()
	ok := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	bad := writeProfile(t, dir, "quiet.yaml", routeProfileNoInbound)

	if err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: ok, AllowEmails: []string{"x@y.io"}}, nil); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: bad, AllowEmails: []string{"x@y.io"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "notification.slack.inbound.enabled") {
		t.Fatalf("want inbound error, got %v", err)
	}
	err = ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: filepath.Join(dir, "missing.yaml"), AllowEmails: []string{"x@y.io"}}, nil)
	if err == nil {
		t.Fatal("want error for missing profile")
	}
	// Validate() failures propagate (empty allowlist).
	err = ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: ok}, nil)
	if err == nil || !strings.Contains(err.Error(), "allow_emails") {
		t.Fatalf("want allow_emails error, got %v", err)
	}
}

func TestExportSlackDefaultRouteEnv(t *testing.T) {
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg := &config.Config{}
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{
		Alias: "ir-bot", Profile: "profiles/ir-bot.yaml",
		AllowEmails: []string{"Kurt@Example.com", " analyst@example.com "},
	}
	exportSlackDefaultRouteEnv(cfg)
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALIAS"); got != "ir-bot" {
		t.Errorf("ALIAS=%q", got)
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_PROFILE"); got != "profiles/ir-bot.yaml" {
		t.Errorf("PROFILE=%q", got)
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW"); got != "kurt@example.com,analyst@example.com" {
		t.Errorf("ALLOW=%q", got)
	}
}

func TestExportSlackDefaultRouteEnv_AbsentExportsNothing(t *testing.T) {
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	exportSlackDefaultRouteEnv(&config.Config{})
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		if _, set := os.LookupEnv(k); set {
			t.Errorf("%s must not be exported when the route is absent", k)
		}
	}
}

type recordingUploader struct{ keys map[string][]byte }

func (r *recordingUploader) PutObject(_ context.Context, _, key string, body []byte) error {
	if r.keys == nil {
		r.keys = map[string][]byte{}
	}
	r.keys[key] = body
	return nil
}

func TestPreStageSlackDefaultRouteProfile(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	up := &recordingUploader{}
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"x@y.io"}}
	if err := PreStageSlackDefaultRouteProfile(context.Background(), route, "bucket", nil, up); err != nil {
		t.Fatal(err)
	}
	body, ok := up.keys["slack-profiles/ir-bot/.km-profile.yaml"]
	if !ok {
		t.Fatalf("profile not uploaded; keys=%v", up.keys)
	}
	if string(body) != routeProfileOK {
		t.Errorf("uploaded bytes differ from the file on disk")
	}
	if SlackDefaultRouteProfileSlug("profiles/IR-Bot.YAML") != "ir-bot" {
		t.Errorf("slug normalisation broken")
	}
}

func TestPreStageSlackDefaultRouteProfile_FlattensExtends(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "ir-base.yaml", `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: ir-base
  abstract: true
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      inbound:
        enabled: true
`)
	child := writeProfile(t, dir, "ir-child.yaml", `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: ir-child
extends: [ir-base]
`)
	up := &recordingUploader{}
	route := &config.SlackDefaultRoute{Alias: "ir", Profile: child, AllowEmails: []string{"x@y.io"}}
	if err := PreStageSlackDefaultRouteProfile(context.Background(), route, "bucket", []string{dir}, up); err != nil {
		t.Fatal(err)
	}
	body := up.keys["slack-profiles/ir-child/.km-profile.yaml"]
	p, err := profile.Parse(body)
	if err != nil {
		t.Fatalf("uploaded YAML does not parse: %v\n%s", err, body)
	}
	if p.Extends.IsSet() {
		t.Errorf("extends must be cleared in the uploaded profile:\n%s", body)
	}
	n := p.Spec.Notification
	if n == nil || n.Slack == nil || n.Slack.Inbound == nil || n.Slack.Inbound.Enabled == nil || !*n.Slack.Inbound.Enabled {
		t.Errorf("inbound.enabled not inherited from base:\n%s", body)
	}
}

// F3: a profile reachable only via a search path pre-stages the real bytes,
// exactly as validation finds it — not an empty placeholder from a cwd miss.
func TestPreStageSlackDefaultRouteProfile_FindsProfileViaSearchPath(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: "nowhere/ir-bot.yaml", AllowEmails: []string{"x@y.io"}}
	if err := ValidateSlackDefaultRouteProfile(route, []string{dir}); err != nil {
		t.Fatalf("validate must find the profile via the search path: %v", err)
	}
	up := &recordingUploader{}
	if err := PreStageSlackDefaultRouteProfile(context.Background(), route, "bucket", []string{dir}, up); err != nil {
		t.Fatal(err)
	}
	if got := string(up.keys["slack-profiles/ir-bot/.km-profile.yaml"]); got != routeProfileOK {
		t.Fatalf("uploaded bytes must be the search-path file; got %q", got)
	}
}

// F3: a missing profile is a hard error from both validate and pre-stage, and
// pre-stage uploads nothing.
func TestSlackDefaultRouteProfile_MissingErrorsAndUploadsNothing(t *testing.T) {
	dir := t.TempDir()
	route := &config.SlackDefaultRoute{Alias: "a", Profile: filepath.Join(dir, "missing-route.yaml"), AllowEmails: []string{"x@y.io"}}
	if err := ValidateSlackDefaultRouteProfile(route, []string{dir}); err == nil {
		t.Fatal("validate: want error for a missing profile")
	}
	up := &recordingUploader{}
	if err := PreStageSlackDefaultRouteProfile(context.Background(), route, "bucket", []string{dir}, up); err == nil {
		t.Fatal("pre-stage: want error for a missing profile")
	}
	if len(up.keys) != 0 {
		t.Fatalf("pre-stage must upload nothing on a miss; keys=%v", up.keys)
	}
}
