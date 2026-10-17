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

	if _, err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: ok, AllowEmails: []string{"x@y.io"}}, nil); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	_, err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: bad, AllowEmails: []string{"x@y.io"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "notification.slack.inbound.enabled") {
		t.Fatalf("want inbound error, got %v", err)
	}
	_, err = ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: filepath.Join(dir, "missing.yaml"), AllowEmails: []string{"x@y.io"}}, nil)
	if err == nil {
		t.Fatal("want error for missing profile")
	}
	// Neither km-config.yaml nor the profile lists anyone ⇒ deny-by-default
	// error naming both places.
	_, err = ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: ok}, nil)
	if err == nil || !strings.Contains(err.Error(), "allow_emails") || !strings.Contains(err.Error(), "allowEmails") {
		t.Fatalf("want an error naming allow_emails and allowEmails, got %v", err)
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
	if _, err := ValidateSlackDefaultRouteProfile(route, []string{dir}); err != nil {
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
	if _, err := ValidateSlackDefaultRouteProfile(route, []string{dir}); err == nil {
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

const routeProfileWithAllow = `apiVersion: klankermaker.ai/v1alpha2
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
        allowEmails:
          - " Kurt@Example.com "
          - "*@Corp.Example"
`

func unsetRouteEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// A malformed profile entry is an error naming the profile.
func TestValidateSlackDefaultRouteProfile_BadProfileEntry(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", strings.Replace(routeProfileWithAllow, `"*@Corp.Example"`, `"k*@corp.example"`, 1))
	_, err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: p}, nil)
	if err == nil || !strings.Contains(err.Error(), "k*@corp.example") || !strings.Contains(err.Error(), p) {
		t.Fatalf("want an error naming the entry and the profile, got %v", err)
	}
}

// slack.react_quiet_seconds: nil ⇒ unset (terragrunt default 180 applies);
// 0 ⇒ "0"; env wins with a WARN, as for react_always.
func TestExportTerragruntEnvVars_ReactQuietSeconds(t *testing.T) {
	t.Setenv("KM_SLACK_REACT_QUIET_SECONDS", "")
	os.Unsetenv("KM_SLACK_REACT_QUIET_SECONDS")
	ExportTerragruntEnvVars(&config.Config{})
	if _, set := os.LookupEnv("KM_SLACK_REACT_QUIET_SECONDS"); set {
		t.Fatalf("nil must leave KM_SLACK_REACT_QUIET_SECONDS unset, got %q", os.Getenv("KM_SLACK_REACT_QUIET_SECONDS"))
	}

	zero := 0
	cfg := &config.Config{}
	cfg.Slack.ReactQuietSeconds = &zero
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_REACT_QUIET_SECONDS"); got != "0" {
		t.Fatalf("0 must export \"0\", got %q", got)
	}

	os.Setenv("KM_SLACK_REACT_QUIET_SECONDS", "600")
	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w
	ExportTerragruntEnvVars(cfg)
	w.Close()
	os.Stderr = orig
	buf := make([]byte, 64<<10)
	n, _ := r.Read(buf)
	if got := os.Getenv("KM_SLACK_REACT_QUIET_SECONDS"); got != "600" {
		t.Fatalf("env must win, got %q", got)
	}
	if out := string(buf[:n]); !strings.Contains(out, "WARN: KM_SLACK_REACT_QUIET_SECONDS=600") {
		t.Fatalf("want drift WARN, got %q", out)
	}
}

// prepareSlackDefaultRoute is the one step every km init path runs: no route ⇒
// no-op; a route ⇒ validated and exported with the effective list.
func TestPrepareSlackDefaultRoute(t *testing.T) {
	unsetRouteEnv(t)
	if err := prepareSlackDefaultRoute(&config.Config{}); err != nil {
		t.Fatalf("no route must be a no-op: %v", err)
	}
	if _, set := os.LookupEnv("KM_SLACK_DEFAULT_ROUTE_ALLOW"); set {
		t.Fatal("no route must export nothing")
	}
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileWithAllow)
	cfg := &config.Config{}
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"x@other.example"}}
	if err := prepareSlackDefaultRoute(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW"); got != "kurt@example.com,*@corp.example" {
		t.Fatalf("ALLOW=%q, want the profile list", got)
	}
	unsetRouteEnv(t)
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{Alias: "ir-bot", Profile: writeProfile(t, dir, "plain.yaml", routeProfileOK)}
	if err := prepareSlackDefaultRoute(cfg); err == nil {
		t.Fatal("neither list must fail")
	}
}

func routeCfg(alias, profilePath string, emails ...string) *config.Config {
	cfg := &config.Config{}
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{Alias: alias, Profile: profilePath, AllowEmails: emails}
	return cfg
}

// The config list, normalised, when the profile has no allowEmails.
func TestEffectiveSlackDefaultRoute_ConfigList(t *testing.T) {
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileOK)
	alias, prof, emails, source, err := effectiveSlackDefaultRoute(routeCfg("ir-bot", p, "Kurt@Example.com", " analyst@example.com "))
	if err != nil {
		t.Fatal(err)
	}
	if alias != "ir-bot" || prof != p || strings.Join(emails, ",") != "kurt@example.com,analyst@example.com" || source != "km-config.yaml" {
		t.Fatalf("got %q %q %v %q", alias, prof, emails, source)
	}
}

// Profile wins over a different config list; source names the resolved path.
func TestEffectiveSlackDefaultRoute_ProfileWins(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileWithAllow)
	// The route names the profile by a path that only resolves via the search
	// path, so the source must show the RESOLVED path, not the configured one.
	cfg := routeCfg("ir-bot", "nowhere/ir-bot.yaml", "someone@else.example")
	cfg.ProfileSearchPaths = []string{dir}
	_, _, emails, source, err := effectiveSlackDefaultRoute(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(emails, ",") != "kurt@example.com,*@corp.example" {
		t.Fatalf("emails = %v, want the profile list", emails)
	}
	if source != "profile "+p {
		t.Fatalf("source = %q, want %q", source, "profile "+p)
	}
}

// A profile-only list is valid: km-config.yaml may leave allow_emails empty.
func TestEffectiveSlackDefaultRoute_ProfileOnly(t *testing.T) {
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileWithAllow)
	_, _, emails, _, err := effectiveSlackDefaultRoute(routeCfg("ir-bot", p))
	if err != nil || len(emails) != 2 {
		t.Fatalf("emails=%v err=%v", emails, err)
	}
}

func TestEffectiveSlackDefaultRoute_NeitherErrors(t *testing.T) {
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileOK)
	_, _, _, _, err := effectiveSlackDefaultRoute(routeCfg("ir-bot", p))
	if err == nil || !strings.Contains(err.Error(), "allow_emails") || !strings.Contains(err.Error(), "allowEmails") {
		t.Fatalf("want an error naming allow_emails and allowEmails, got %v", err)
	}
}

func TestEffectiveSlackDefaultRoute_Absent(t *testing.T) {
	alias, _, emails, _, err := effectiveSlackDefaultRoute(&config.Config{})
	if err != nil || alias != "" || emails != nil {
		t.Fatalf("absent route must be zero/nil, got %q %v %v", alias, emails, err)
	}
}

// ExportTerragruntEnvVars (best-effort) exports the profile list, so every
// terragrunt-driving command agrees with km init.
func TestExportTerragruntEnvVars_ExportsProfileRouteList(t *testing.T) {
	unsetRouteEnv(t)
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileWithAllow)
	ExportTerragruntEnvVars(routeCfg("ir-bot", p, "stale@example.com"))
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW"); got != "kurt@example.com,*@corp.example" {
		t.Fatalf("ALLOW=%q, want the profile list", got)
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALIAS"); got != "ir-bot" {
		t.Errorf("ALIAS=%q", got)
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_PROFILE"); got != p {
		t.Errorf("PROFILE=%q", got)
	}
}

// Neither list ⇒ one WARN, none of the three exported.
func TestExportTerragruntEnvVars_RouteWithoutListWarnsAndExportsNothing(t *testing.T) {
	unsetRouteEnv(t)
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileOK)
	out := captureStderr(t, func() { ExportTerragruntEnvVars(routeCfg("ir-bot", p)) })
	if strings.Count(out, "WARN: slack.default_route not exported") != 1 {
		t.Fatalf("want exactly one route WARN, got %q", out)
	}
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		if _, set := os.LookupEnv(k); set {
			t.Errorf("%s must not be exported when the route has no allowlist", k)
		}
	}
}

// Absent route ⇒ nothing exported, no WARN.
func TestExportTerragruntEnvVars_AbsentRouteExportsNothing(t *testing.T) {
	unsetRouteEnv(t)
	out := captureStderr(t, func() { ExportTerragruntEnvVars(&config.Config{}) })
	if strings.Contains(out, "slack.default_route") {
		t.Fatalf("absent route must be silent, got %q", out)
	}
	for _, k := range []string{"KM_SLACK_DEFAULT_ROUTE_ALIAS", "KM_SLACK_DEFAULT_ROUTE_PROFILE", "KM_SLACK_DEFAULT_ROUTE_ALLOW"} {
		if _, set := os.LookupEnv(k); set {
			t.Errorf("%s must not be exported when the route is absent", k)
		}
	}
}

// prepareSlackDefaultRoute prints the RESOLVED profile path in its [info] line.
func TestPrepareSlackDefaultRoute_InfoNamesResolvedPath(t *testing.T) {
	unsetRouteEnv(t)
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileWithAllow)
	cfg := routeCfg("ir-bot", "nowhere/ir-bot.yaml")
	cfg.ProfileSearchPaths = []string{dir}
	var err error
	out := captureStderr(t, func() { err = prepareSlackDefaultRoute(cfg) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[info] slack.default_route allowlist: from profile "+p+" (2 entries)") {
		t.Fatalf("info line must name the resolved path %s, got %q", p, out)
	}
}

// buildSlackCmdDeps hands RunSlackInit the config, so km slack init can run
// the fail-closed route export before its bridge apply.
func TestBuildSlackCmdDeps_CarriesCfg(t *testing.T) {
	unsetRouteEnv(t)
	p := writeProfile(t, t.TempDir(), "ir-bot.yaml", routeProfileWithAllow)
	cfg := routeCfg("ir-bot", p)
	d, err := buildSlackCmdDeps(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cfg != cfg {
		t.Fatal("buildSlackCmdDeps must set Cfg")
	}
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW"); got != "kurt@example.com,*@corp.example" {
		t.Fatalf("ALLOW=%q after buildSlackCmdDeps, want the profile list", got)
	}
}
