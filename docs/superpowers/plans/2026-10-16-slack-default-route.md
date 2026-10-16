# Slack Default Route Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an allowlisted person @-mention the Slack bot from any channel or DM and have the message dispatched to one configured default sandbox, with replies landing in the originating thread.

**Architecture:** The bridge's `FetchByChannel` miss path gains a ladder (thread continuity → relay → route gate → alias resolve → shared dispatch tail). The sandbox-side poller replies to the envelope's channel instead of its own, and the bridge's outbound authorizer admits posts into threads it routed. Config, `km init`, `km doctor`, Terraform, the Slack manifest, and the create-handler each get one additive change.

**Tech Stack:** Go 1.25, AWS SDK v2 (DynamoDB, SQS, EventBridge, EC2), Terraform/Terragrunt, bash userdata rendered by `pkg/compiler`, Slack Web API.

**Spec:** `docs/superpowers/specs/2026-10-16-slack-default-route-design.md`

## Global Constraints

- No `apiVersion` bump; every change is additive and dormant when `slack.default_route` is absent.
- Profiles without Slack inbound must render byte-identical userdata; the frozen `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` is hand-patched, never re-captured (see memory `project_frozen_byte_identity_golden_capture_trap`).
- `infra/modules/lambda-slack-bridge/v1.0.0` and `infra/modules/km-operator-policy/v1.0.0` are edited in place (additive, empty defaults). No new module version directories.
- Lambda handlers return 200 on every internal error after signature verification (a 5xx triggers a Slack retry storm).
- Allowlist semantics: empty `allow_emails` ⇒ route OFF. Deny on every identity failure. Never cache a deny.
- Commit after every task with a conventional-commit prefix and the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Stage by explicit pathspec only (other agents may share the index).
- Run `go test ./<pkg>/...` for the touched package after each task; run `go test ./...` once at the end of Task 14. `cd <pkg> && go test .` if a worktree guard blocks compound shells.
- Two spec addenda discovered during planning, both now part of the plan: the create-handler role needs `sqs:SendMessage` on `{prefix}-slack-inbound-*.fifo` (Task 11), and `km init` must pre-stage the route's profile to `s3://{artifacts}/slack-profiles/{slug}/.km-profile.yaml` (Task 2), because the create-handler reads the profile from that prefix.

---

### Task 1: Config block `slack.default_route`

**Files:**
- Modify: `internal/app/config/config.go` (SlackConfig struct near line 96–160; merge list near line 1140; load block near line 1330)
- Test: `internal/app/config/config_slack_default_route_test.go` (create)

**Interfaces:**
- Produces: `config.SlackDefaultRoute{Alias, Profile string; AllowEmails []string}`, `cfg.Slack.DefaultRoute *SlackDefaultRoute`, `(*SlackDefaultRoute).Validate() error`, `(*SlackDefaultRoute).NormalizedEmails() []string`.

- [ ] **Step 1: Write the failing tests**

```go
package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func loadFrom(t *testing.T, yaml string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeKMConfig(t, dir, yaml)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}

// Merge-list regression guard: if "slack.default_route" is absent from the
// v2→v merge-list, DefaultRoute stays nil and this test fails loud.
func TestLoadSlackDefaultRoute_Set(t *testing.T) {
	cfg := loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    default_route:
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails:
        - Kurt@Example.com
        - analyst@example.com
`)
	r := cfg.Slack.DefaultRoute
	if r == nil {
		t.Fatal("Slack.DefaultRoute is nil — add \"slack.default_route\" to the v2→v merge-list (project_config_key_merge_list footgun)")
	}
	if r.Alias != "ir-bot" || r.Profile != "profiles/ir-bot.yaml" {
		t.Errorf("alias/profile: got %q/%q", r.Alias, r.Profile)
	}
	got := r.NormalizedEmails()
	if len(got) != 2 || got[0] != "kurt@example.com" || got[1] != "analyst@example.com" {
		t.Errorf("NormalizedEmails: got %v", got)
	}
}

func TestLoadSlackDefaultRoute_Absent(t *testing.T) {
	cfg := loadFrom(t, "domain: example.com\nregion: us-east-1\n")
	if cfg.Slack.DefaultRoute != nil {
		t.Fatalf("expected nil DefaultRoute when absent, got %+v", cfg.Slack.DefaultRoute)
	}
}

func TestSlackDefaultRoute_Validate(t *testing.T) {
	cases := []struct {
		name    string
		route   config.SlackDefaultRoute
		wantErr string
	}{
		{"ok", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"x@y.z"}}, ""},
		{"no alias", config.SlackDefaultRoute{Profile: "p.yaml", AllowEmails: []string{"x@y.z"}}, "alias"},
		{"no profile", config.SlackDefaultRoute{Alias: "a", AllowEmails: []string{"x@y.z"}}, "profile"},
		{"empty allow", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml"}, "allow_emails"},
		{"not an email", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"nope"}}, "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.route.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd internal/app/config && go test -run 'SlackDefaultRoute' .`
Expected: FAIL to compile (`config.SlackDefaultRoute` undefined).

- [ ] **Step 3: Add the struct, merge-list entry, load, and validation**

In `SlackConfig`, after the `Allow` field:

```go
	// DefaultRoute (2026-10-16): when set on the FRONT-DOOR install, an @-mention
	// of the bot in any channel the bot is a member of (or a DM) that no sandbox
	// owns is dispatched to the sandbox at Alias — warm enqueue, resume-if-stopped,
	// or cold-create from Profile. Gated on AllowEmails (sender's Slack profile
	// email, resolved at event time). Empty AllowEmails ⇒ route OFF. Absent ⇒
	// byte-identical to Phase 96/118 behaviour.
	//
	// Maps to km-config.yaml key slack.default_route. Exported as
	// KM_SLACK_DEFAULT_ROUTE_{ALIAS,PROFILE,ALLOW} by ExportTerragruntEnvVars.
	// CRITICAL: "slack.default_route" must be in the v2→v merge-list.
	DefaultRoute *SlackDefaultRoute `mapstructure:"default_route" yaml:"default_route,omitempty"`
}

// SlackDefaultRoute is the slack.default_route block. See SlackConfig.DefaultRoute.
type SlackDefaultRoute struct {
	Alias       string   `mapstructure:"alias"        yaml:"alias"`
	Profile     string   `mapstructure:"profile"      yaml:"profile"`
	AllowEmails []string `mapstructure:"allow_emails" yaml:"allow_emails,omitempty"`
}

// NormalizedEmails returns AllowEmails lowercased, trimmed, empties dropped.
func (r *SlackDefaultRoute) NormalizedEmails() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.AllowEmails))
	for _, e := range r.AllowEmails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Validate enforces the deny-by-default contract: alias and profile set, at
// least one syntactically plausible email. It does NOT touch disk or Slack.
func (r *SlackDefaultRoute) Validate() error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.Alias) == "" {
		return fmt.Errorf("slack.default_route.alias is required")
	}
	if strings.TrimSpace(r.Profile) == "" {
		return fmt.Errorf("slack.default_route.profile is required")
	}
	emails := r.NormalizedEmails()
	if len(emails) == 0 {
		return fmt.Errorf("slack.default_route.allow_emails is empty: the default route is deny-by-default and stays OFF until at least one email is listed")
	}
	for _, e := range emails {
		if !strings.Contains(e, "@") {
			return fmt.Errorf("slack.default_route.allow_emails entry %q is not an email address", e)
		}
	}
	return nil
}
```

In the merge-list (next to `"slack.allow"`):

```go
			// 2026-10-16: default route block. One entry covers the nested keys
			// because the load below uses UnmarshalKey on the parent (same shape
			// as "github"). CRITICAL: without it the block is silently dropped.
			"slack.default_route",
```

In the load block, after the `slack.allow` block:

```go
	// 2026-10-16: slack.default_route is a structured block → UnmarshalKey. Only
	// populated when present; absent ⇒ nil ⇒ route OFF everywhere downstream.
	if v.IsSet("slack.default_route") {
		var dr SlackDefaultRoute
		if err := v.UnmarshalKey("slack.default_route", &dr); err != nil {
			return nil, fmt.Errorf("unmarshal slack.default_route: %w", err)
		}
		cfg.Slack.DefaultRoute = &dr
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd internal/app/config && go test .`
Expected: PASS (all tests in the package, not only the new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/app/config/config.go internal/app/config/config_slack_default_route_test.go
git commit -m "feat(config): slack.default_route block with deny-by-default validation" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: `km init` export, profile validation, and profile pre-stage

**Files:**
- Modify: `internal/app/cmd/init.go` (export block after the `KM_SLACK_ALLOW` block near line 2005; pre-stage call site after the webhook pre-stage near line 1487; new functions next to `PreStageGitHubProfiles` near line 870)
- Test: `internal/app/cmd/init_slack_default_route_test.go` (create)

**Interfaces:**
- Consumes: `config.SlackDefaultRoute` (Task 1), `S3ProfileUploader`, `githubProfileSlug`, `readFileOrEmpty` (existing in init.go), `profile.Resolve(name string, searchPaths []string)`.
- Produces: env `KM_SLACK_DEFAULT_ROUTE_ALIAS`, `KM_SLACK_DEFAULT_ROUTE_PROFILE`, `KM_SLACK_DEFAULT_ROUTE_ALLOW`; `ValidateSlackDefaultRouteProfile(route *config.SlackDefaultRoute, searchPaths []string) error`; `PreStageSlackDefaultRouteProfile(ctx, route *config.SlackDefaultRoute, bucket string, up S3ProfileUploader) error`; `SlackDefaultRouteProfileSlug(profile string) string`.

- [ ] **Step 1: Write the failing tests**

```go
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
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

	if err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: ok, AllowEmails: []string{"x@y.z"}}, nil); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	err := ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: bad, AllowEmails: []string{"x@y.z"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "notification.slack.inbound.enabled") {
		t.Fatalf("want inbound error, got %v", err)
	}
	err = ValidateSlackDefaultRouteProfile(&config.SlackDefaultRoute{Alias: "a", Profile: filepath.Join(dir, "missing.yaml"), AllowEmails: []string{"x@y.z"}}, nil)
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
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"x@y.z"}}
	if err := PreStageSlackDefaultRouteProfile(context.Background(), route, "bucket", up); err != nil {
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd internal/app/cmd && go test -run 'SlackDefaultRoute' .`
Expected: FAIL to compile (`ValidateSlackDefaultRouteProfile` undefined).

- [ ] **Step 3: Implement export, validation, pre-stage**

Add next to `PreStageGitHubProfiles`:

```go
// SlackDefaultRouteProfileSlug is the S3 prefix key for the default-route
// profile: s3://{artifacts}/slack-profiles/{slug}/.km-profile.yaml. Same
// normalisation as the GitHub bridge (base name, extension stripped, lowercase)
// so pkg/slack/bridge.EventBridgeAdapter and this uploader agree by construction.
func SlackDefaultRouteProfileSlug(profile string) string { return githubProfileSlug(profile) }

// ValidateSlackDefaultRouteProfile runs route.Validate() and then resolves the
// profile (extends included) to prove it has Slack inbound enabled — a
// cold-created box without an inbound queue can never answer.
func ValidateSlackDefaultRouteProfile(route *config.SlackDefaultRoute, searchPaths []string) error {
	if route == nil {
		return nil
	}
	if err := route.Validate(); err != nil {
		return err
	}
	paths := append([]string{filepath.Dir(route.Profile)}, searchPaths...)
	p, err := profile.Resolve(SlackDefaultRouteProfileSlug(route.Profile), paths)
	if err != nil {
		return fmt.Errorf("slack.default_route.profile %q: %w", route.Profile, err)
	}
	n := p.Spec.Notification
	slackOn := n != nil && n.Slack != nil && n.Slack.Enabled != nil && *n.Slack.Enabled
	inboundOn := slackOn && n.Slack.Inbound != nil && n.Slack.Inbound.Enabled != nil && *n.Slack.Inbound.Enabled
	if !slackOn || !inboundOn {
		return fmt.Errorf("slack.default_route.profile %q must set notification.slack.enabled and notification.slack.inbound.enabled: a default-route sandbox replies through its inbound poller and has no other way to answer", route.Profile)
	}
	return nil
}

// PreStageSlackDefaultRouteProfile uploads the route's profile YAML to
// s3://{bucket}/slack-profiles/{slug}/.km-profile.yaml so a cold-create
// published by the Slack bridge can be provisioned. Mirrors PreStageGitHubProfiles;
// reads the file at route.Profile verbatim.
func PreStageSlackDefaultRouteProfile(ctx context.Context, route *config.SlackDefaultRoute, bucket string, up S3ProfileUploader) error {
	if route == nil {
		return nil
	}
	key := "slack-profiles/" + SlackDefaultRouteProfileSlug(route.Profile) + "/.km-profile.yaml"
	if err := up.PutObject(ctx, bucket, key, readFileOrEmpty(route.Profile)); err != nil {
		return fmt.Errorf("km init: pre-stage slack default-route profile: %w", err)
	}
	return nil
}

// exportSlackDefaultRouteEnv exports the three KM_SLACK_DEFAULT_ROUTE_* vars
// consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl. All three or
// none: a partial export would turn the route on with no allowlist. env-wins
// drift WARN mirrors KM_SLACK_ALLOW.
func exportSlackDefaultRouteEnv(cfg *config.Config) {
	r := cfg.Slack.DefaultRoute
	if r == nil {
		return
	}
	emails := r.NormalizedEmails()
	if r.Alias == "" || r.Profile == "" || len(emails) == 0 {
		return // Validate() reports this; never export a half-configured route.
	}
	for k, want := range map[string]string{
		"KM_SLACK_DEFAULT_ROUTE_ALIAS":   r.Alias,
		"KM_SLACK_DEFAULT_ROUTE_PROFILE": r.Profile,
		"KM_SLACK_DEFAULT_ROUTE_ALLOW":   strings.Join(emails, ","),
	} {
		if envVal := os.Getenv(k); envVal != "" && envVal != want {
			fmt.Fprintf(os.Stderr, "WARN: %s=%s (env) overrides km-config.yaml slack.default_route value %s\n", k, envVal, want)
		} else if envVal == "" {
			os.Setenv(k, want) //nolint:errcheck
		}
	}
}
```

Call `exportSlackDefaultRouteEnv(cfg)` inside `ExportTerragruntEnvVars` immediately after the `KM_SLACK_ALLOW` block. Add `"github.com/whereiskurt/klanker-maker/pkg/profile"` to init.go's imports if absent.

In `runInit`, immediately after the webhook pre-stage block (before `repoRoot := findRepoRoot()`):

```go
	// 2026-10-16: Slack default route. Validate (deny-by-default + the profile
	// must carry Slack inbound) and pre-stage the profile for cold-create.
	if cfg.Slack.DefaultRoute != nil {
		if err := ValidateSlackDefaultRouteProfile(cfg.Slack.DefaultRoute, cfg.ProfileSearchPaths); err != nil {
			return fmt.Errorf("km init: %w", err)
		}
		if cfg.ArtifactsBucket != "" {
			uploader := &s3ProfileUploaderClient{inner: s3.NewFromConfig(awsCfg)}
			if err := PreStageSlackDefaultRouteProfile(ctx, cfg.Slack.DefaultRoute, cfg.ArtifactsBucket, uploader); err != nil {
				return err
			}
		}
	}
```

Add the same `ValidateSlackDefaultRouteProfile` call in `runInitScoped` right after `ExportTerragruntEnvVars(cfg)` (no pre-stage there; scoped applies never cold-create a new profile).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd internal/app/cmd && go test -run 'SlackDefaultRoute|ExportTerragrunt|TestRunInit' .`
Expected: PASS. (Known pre-existing failures in this package: `TestBootstrap*`, `TestCluster*`, `TestConfigure*` — see memory `project_cmd_suite_pre_existing_failures`; they are unrelated.)

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/init_slack_default_route_test.go
git commit -m "feat(init): export, validate, and pre-stage the Slack default route" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: `pkg/slack` client `UserInfo`

**Files:**
- Modify: `pkg/slack/client.go` (`SlackUserField` near line 70; new method after `LookupUserByEmail` near line 735)
- Test: `pkg/slack/client_userinfo_test.go` (create)

**Interfaces:**
- Produces: `type SlackUserInfo struct{ ID, Email string; Deleted, IsBot bool }`, `(*Client).UserInfo(ctx, userID string) (*SlackUserInfo, error)`.

- [ ] **Step 1: Write the failing test**

```go
package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserInfo_ParsesEmailDeletedBot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.info" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("user") != "U0KURT" {
			t.Errorf("form user=%q err=%v", r.Form.Get("user"), err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"user":{"id":"U0KURT","deleted":true,"is_bot":false,"profile":{"email":"Kurt@Example.com"}}}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.SetBaseURL(srv.URL)
	u, err := c.UserInfo(context.Background(), "U0KURT")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "U0KURT" || u.Email != "kurt@example.com" || !u.Deleted || u.IsBot {
		t.Fatalf("got %+v", u)
	}
}

func TestUserInfo_SlackErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.SetBaseURL(srv.URL)
	if _, err := c.UserInfo(context.Background(), "UNOPE"); err == nil {
		t.Fatal("want error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd pkg/slack && go test -run UserInfo .`
Expected: FAIL to compile (`UserInfo` undefined).

- [ ] **Step 3: Implement**

Extend `SlackUserField` and its `UnmarshalJSON` so the object shape also carries the three fields:

```go
type SlackUserField struct {
	ID      string
	Deleted bool
	IsBot   bool
	Email   string // profile.email; empty when the token lacks users:read.email
}

func (u *SlackUserField) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		return nil
	}
	var obj struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
		IsBot   bool   `json:"is_bot"`
		Profile struct {
			Email string `json:"email"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	u.ID, u.Deleted, u.IsBot, u.Email = obj.ID, obj.Deleted, obj.IsBot, obj.Profile.Email
	return nil
}
```

Add after `LookupUserByEmail`:

```go
// SlackUserInfo is the subset of users.info the bridge's default-route gate needs.
type SlackUserInfo struct {
	ID      string
	Email   string // lowercased; empty when absent
	Deleted bool
	IsBot   bool
}

// UserInfo wraps users.info (form-encoded, same legacy-method class as
// users.lookupByEmail). Requires users:read; profile.email additionally requires
// users:read.email. Any Slack error is returned as *SlackAPIError.
func (c *Client) UserInfo(ctx context.Context, userID string) (*SlackUserInfo, error) {
	resp, err := c.callForm(ctx, "users.info", url.Values{"user": {userID}})
	if err != nil {
		return nil, err
	}
	return &SlackUserInfo{
		ID:      resp.User.ID,
		Email:   strings.ToLower(strings.TrimSpace(resp.User.Email)),
		Deleted: resp.User.Deleted,
		IsBot:   resp.User.IsBot,
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd pkg/slack && go test .`
Expected: PASS (existing `LookupUserByEmail` tests still pass — the extra fields are additive).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/client.go pkg/slack/client_userinfo_test.go
git commit -m "feat(slack): users.info client method with email/deleted/is_bot" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Bridge interfaces, event `channel_type`, and the email allowlist gate

**Files:**
- Modify: `pkg/slack/bridge/events_types.go` (`slackMessageEvent` near line 15)
- Modify: `pkg/slack/bridge/events_interfaces.go` (append)
- Create: `pkg/slack/bridge/default_route.go`
- Test: `pkg/slack/bridge/default_route_test.go` (create)

**Interfaces:**
- Produces (all in package `bridge`):
  - `slackMessageEvent.ChannelType string \`json:"channel_type,omitempty"\``
  - `type SandboxByIDFetcher interface{ FetchBySandboxID(ctx, sandboxID string) (SandboxRoutingInfo, error) }`
  - `type SlackAliasResolver interface{ ResolveAlias(ctx, alias string) (info SandboxRoutingInfo, found bool, err error) }`
  - `type SandboxCreatePublisher interface{ PutSandboxCreate(ctx, alias, profile, slackEnvelopeJSON string) (sandboxID string, err error) }`
  - `type SenderEmailFetcher interface{ SenderEmail(ctx, userID string) (email string, err error) }` — returns `ErrSenderIneligible` for deleted/bot/no-email.
  - `var ErrSenderIneligible = errors.New(...)`
  - `type DefaultRoute struct{ Alias, Profile string; allow map[string]struct{} }`, `NewDefaultRoute(alias, profile string, emails []string) *DefaultRoute`, `(*DefaultRoute).Allows(email string) bool`
  - `type CachedSenderEmailFetcher struct{ Inner SenderEmailFetcher; TTL time.Duration; Now func() time.Time }` implementing `SenderEmailFetcher`, caching successes only.
  - `requiresMention(channelType string) bool` — false only for `"im"`.

- [ ] **Step 1: Write the failing tests**

```go
package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

type scriptedEmails struct {
	calls int
	email string
	err   error
}

func (s *scriptedEmails) SenderEmail(_ context.Context, _ string) (string, error) {
	s.calls++
	return s.email, s.err
}

func TestDefaultRoute_AllowsIsCaseInsensitiveAndTrimmed(t *testing.T) {
	r := NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{" Kurt@Example.com "})
	if !r.Allows("kurt@example.com") || !r.Allows("KURT@EXAMPLE.COM") {
		t.Fatal("expected allow")
	}
	if r.Allows("other@example.com") || r.Allows("") {
		t.Fatal("expected deny")
	}
}

func TestNewDefaultRoute_EmptyEmailsIsNil(t *testing.T) {
	if NewDefaultRoute("a", "p", nil) != nil || NewDefaultRoute("a", "p", []string{" "}) != nil {
		t.Fatal("empty allowlist must yield a nil route (route OFF)")
	}
}

func TestRequiresMention(t *testing.T) {
	for ct, want := range map[string]bool{"im": false, "mpim": true, "channel": true, "group": true, "": true} {
		if got := requiresMention(ct); got != want {
			t.Errorf("requiresMention(%q)=%v want %v", ct, got, want)
		}
	}
}

func TestCachedSenderEmailFetcher_CachesSuccessOnly(t *testing.T) {
	now := time.Unix(1000, 0)
	inner := &scriptedEmails{email: "a@b.c"}
	c := &CachedSenderEmailFetcher{Inner: inner, TTL: 15 * time.Minute, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if e, err := c.SenderEmail(context.Background(), "U1"); err != nil || e != "a@b.c" {
			t.Fatalf("iter %d: %q %v", i, e, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("want 1 inner call, got %d", inner.calls)
	}
	now = now.Add(16 * time.Minute)
	c.SenderEmail(context.Background(), "U1")
	if inner.calls != 2 {
		t.Fatalf("want refetch after TTL, got %d calls", inner.calls)
	}

	bad := &scriptedEmails{err: errors.New("boom")}
	c2 := &CachedSenderEmailFetcher{Inner: bad, TTL: time.Hour, Now: func() time.Time { return now }}
	c2.SenderEmail(context.Background(), "U2")
	c2.SenderEmail(context.Background(), "U2")
	if bad.calls != 2 {
		t.Fatalf("a failure must never be cached; got %d calls", bad.calls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd pkg/slack/bridge && go test -run 'DefaultRoute|RequiresMention|CachedSenderEmail' .`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

`events_types.go`: add to `slackMessageEvent`:

```go
	// ChannelType is Slack's conversation kind: "channel", "group", "im", "mpim".
	// The default route requires an @-mention everywhere except "im".
	ChannelType string `json:"channel_type,omitempty"`
```

`events_interfaces.go`: append:

```go
// ---- 2026-10-16: default route ---------------------------------------------

// SandboxByIDFetcher reads routing info for a known sandbox id (GetItem on
// km-sandboxes). Used for thread continuity in channels no sandbox owns.
type SandboxByIDFetcher interface {
	FetchBySandboxID(ctx context.Context, sandboxID string) (SandboxRoutingInfo, error)
}

// SlackAliasResolver resolves the default route's alias to routing info.
// found=false with err=nil means "no row": cold-create.
type SlackAliasResolver interface {
	ResolveAlias(ctx context.Context, alias string) (info SandboxRoutingInfo, found bool, err error)
}

// SandboxCreatePublisher publishes a SandboxCreate event carrying the Slack
// envelope and returns the sandbox id it minted, so the thread row can be
// written before the box exists.
type SandboxCreatePublisher interface {
	PutSandboxCreate(ctx context.Context, alias, profile, slackEnvelopeJSON string) (sandboxID string, err error)
}

// SenderEmailFetcher resolves a Slack user id to a lowercased email. It returns
// ErrSenderIneligible for deleted users, bots, and profiles with no email.
type SenderEmailFetcher interface {
	SenderEmail(ctx context.Context, userID string) (email string, err error)
}
```

`default_route.go`:

```go
package bridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrSenderIneligible marks a sender who can never be on the allowlist:
// deleted, a bot, or a profile with no email.
var ErrSenderIneligible = errors.New("slack-bridge: sender is not an eligible human user")

// DefaultRoute is the resolved slack.default_route block. A nil *DefaultRoute
// means the route is OFF; NewDefaultRoute returns nil for an empty allowlist so
// the deny-by-default contract is structural, not a runtime check.
type DefaultRoute struct {
	Alias   string
	Profile string
	allow   map[string]struct{}
}

func NewDefaultRoute(alias, profile string, emails []string) *DefaultRoute {
	allow := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			allow[e] = struct{}{}
		}
	}
	if len(allow) == 0 || alias == "" || profile == "" {
		return nil
	}
	return &DefaultRoute{Alias: alias, Profile: profile, allow: allow}
}

// Allows reports whether email is on the list. Empty email is always denied.
func (r *DefaultRoute) Allows(email string) bool {
	if r == nil {
		return false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	_, ok := r.allow[email]
	return ok
}

// requiresMention: a DM to the bot is addressed by construction; every other
// conversation kind needs an explicit <@bot> token.
func requiresMention(channelType string) bool { return channelType != "im" }

// CachedSenderEmailFetcher memoises successful lookups for TTL. Failures and
// ErrSenderIneligible are never cached: a transient Slack error costs one
// event, and a re-activated user is seen on their next message.
type CachedSenderEmailFetcher struct {
	Inner SenderEmailFetcher
	TTL   time.Duration
	Now   func() time.Time

	mu    sync.Mutex
	cache map[string]cachedEmail
}

type cachedEmail struct {
	email   string
	expires time.Time
}

func (c *CachedSenderEmailFetcher) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CachedSenderEmailFetcher) SenderEmail(ctx context.Context, userID string) (string, error) {
	c.mu.Lock()
	if hit, ok := c.cache[userID]; ok && c.now().Before(hit.expires) {
		c.mu.Unlock()
		return hit.email, nil
	}
	c.mu.Unlock()

	email, err := c.Inner.SenderEmail(ctx, userID)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cachedEmail{}
	}
	c.cache[userID] = cachedEmail{email: email, expires: c.now().Add(c.TTL)}
	c.mu.Unlock()
	return email, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd pkg/slack/bridge && go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/events_types.go pkg/slack/bridge/events_interfaces.go pkg/slack/bridge/default_route.go pkg/slack/bridge/default_route_test.go
git commit -m "feat(slack-bridge): default-route types, email allowlist, channel_type" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: Events handler ladder

**Files:**
- Modify: `pkg/slack/bridge/events_handler.go` (struct near line 23; `Handle` step 5 miss branch lines ~251–335; steps 5a/5b lines ~336–390; step 7/8 lines ~430–520)
- Modify: `pkg/slack/bridge/events_interfaces.go` (`SlackThreadStore` gains `Rebind`)
- Modify: `pkg/slack/bridge/events_handler_test.go` (`fakeThreads` gains `Rebind`; add fakes for the four new deps)
- Modify: `pkg/slack/bridge/lookup_thread_handler_test.go` (`fakeThreadStore` gains `Rebind`)
- Modify: `pkg/slack/bridge/aws_adapters.go` (`DDBThreadStore.Rebind` next to `Upsert` near line 996)
- Test: `pkg/slack/bridge/events_handler_default_route_test.go` (create)

**Interfaces:**
- Consumes: Task 4 types.
- Produces: `EventsHandler` fields `DefaultRoute *DefaultRoute`, `SandboxByID SandboxByIDFetcher`, `AliasResolver SlackAliasResolver`, `CreatePublisher SandboxCreatePublisher`, `SenderEmails SenderEmailFetcher`; `SlackThreadStore.Rebind(ctx, channelID, threadTS, sandboxID string) error` (unconditional PutItem that resets the session id).

- [ ] **Step 1: Add `Rebind` to the thread store interface, both fakes, and the DDB implementation**

`events_interfaces.go`, inside `SlackThreadStore`:

```go
	// Rebind overwrites the row for (channel, thread_ts) with a new sandbox_id
	// and clears claude_session_id — used when the alias behind a routed
	// thread now resolves to a different (recreated) sandbox.
	Rebind(ctx context.Context, channelID, threadTS, sandboxID string) error
```

`events_handler_test.go`, on `fakeThreads`:

```go
func (f *fakeThreads) Rebind(ctx context.Context, ch, ts, sb string) error {
	if f.sandboxByThread == nil {
		f.sandboxByThread = map[string]string{}
	}
	f.sandboxByThread[ch+"|"+ts] = sb
	f.rebinds = append(f.rebinds, struct{ chan_, ts, sb string }{ch, ts, sb})
	return f.err
}
```

and add `rebinds []struct{ chan_, ts, sb string }` to the struct. `lookup_thread_handler_test.go` `fakeThreadStore`: `func (f *fakeThreadStore) Rebind(_ context.Context, _, _, _ string) error { return nil }`.

`aws_adapters.go`, after `Upsert`:

```go
// Rebind unconditionally rewrites (channel, thread_ts) → sandbox_id with no
// session id, so the next turn starts a fresh agent session on the new box.
func (s *DDBThreadStore) Rebind(ctx context.Context, channelID, threadTS, sandboxID string) error {
	now := time.Now()
	nowISO := now.UTC().Format(time.RFC3339)
	ttlExpiry := strconv.FormatInt(now.Add(30*24*time.Hour).Unix(), 10)
	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"channel_id":   &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":    &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
			"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: nowISO},
			"last_turn_ts": &dynamodbtypes.AttributeValueMemberS{Value: nowISO},
			"turn_count":   &dynamodbtypes.AttributeValueMemberN{Value: "0"},
			"ttl_expiry":   &dynamodbtypes.AttributeValueMemberN{Value: ttlExpiry},
		},
	})
	if err != nil {
		return fmt.Errorf("threads rebind: %w", err)
	}
	return nil
}
```

Run `cd pkg/slack/bridge && go build ./... && go vet .` — must compile before the next step.

- [ ] **Step 2: Write the failing handler tests**

```go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---- fakes for the four default-route deps ----

type fakeByID struct {
	infos map[string]SandboxRoutingInfo
	err   error
}

func (f *fakeByID) FetchBySandboxID(_ context.Context, id string) (SandboxRoutingInfo, error) {
	return f.infos[id], f.err
}

type fakeAlias struct {
	info  SandboxRoutingInfo
	found bool
	err   error
	calls int
}

func (f *fakeAlias) ResolveAlias(_ context.Context, _ string) (SandboxRoutingInfo, bool, error) {
	f.calls++
	return f.info, f.found, f.err
}

type fakePublisher struct {
	calls     []struct{ alias, profile, envelope string }
	sandboxID string
	err       error
}

func (f *fakePublisher) PutSandboxCreate(_ context.Context, alias, profile, env string) (string, error) {
	f.calls = append(f.calls, struct{ alias, profile, envelope string }{alias, profile, env})
	return f.sandboxID, f.err
}

type fakeRelayer struct {
	calls   int
	results []PeerClaimResult
}

func (f *fakeRelayer) Broadcast(_ context.Context, _ string, _ map[string]string) ([]PeerClaimResult, error) {
	f.calls++
	return f.results, nil
}

// routedHandler: channel CFOREIGN is owned by nobody; alias "ir-bot" resolves to
// sb-irbot running; sender U0KURT maps to kurt@example.com (allowed), U0OTHER to
// other@example.com (denied).
func routedHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAlias, *fakePublisher, *fakeReactor, *scriptedEmailsByUser) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, reactor := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{} // every channel is a miss
	alias := &fakeAlias{info: SandboxRoutingInfo{SandboxID: "sb-irbot", QueueURL: "https://sqs.example/irbot.fifo"}, found: true}
	pub := &fakePublisher{sandboxID: "sl-cold1"}
	emails := &scriptedEmailsByUser{byUser: map[string]string{"U0KURT": "kurt@example.com", "U0OTHER": "other@example.com"}}
	h.DefaultRoute = NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"kurt@example.com"})
	h.AliasResolver = alias
	h.CreatePublisher = pub
	h.SenderEmails = emails
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	return h, sqs, threads, alias, pub, reactor, emails
}

type scriptedEmailsByUser struct {
	byUser map[string]string
	err    error
	calls  int
}

func (s *scriptedEmailsByUser) SenderEmail(_ context.Context, u string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	e, ok := s.byUser[u]
	if !ok {
		return "", ErrSenderIneligible
	}
	return e, nil
}

func routedRequest(t *testing.T, now time.Time, channel, channelType, user, text, ts, threadTS, eventID string) EventsRequest {
	t.Helper()
	ev := map[string]any{"type": "message", "channel": channel, "user": user, "text": text, "ts": ts}
	if channelType != "" {
		ev["channel_type"] = channelType
	}
	if threadTS != "" {
		ev["thread_ts"] = threadTS
	}
	evb, _ := json.Marshal(ev)
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":%s}`, eventID, evb)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	return EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body}
}

func TestDefaultRoute_MentionFromListedSender_Dispatches(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, reactor, _ := routedHandler(t, now)
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> do the thing", "1.0", "", "DR1"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("want 1 send to irbot queue, got %+v", sqs.sends)
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.Channel != "CFOREIGN" || body.ThreadTS != "1.0" || body.User != "U0KURT" {
		t.Fatalf("envelope %+v", body)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].sb != "sb-irbot" || threads.upserts[0].chan_ != "CFOREIGN" {
		t.Fatalf("thread row %+v", threads.upserts)
	}
	if len(reactor.snapshot()) != 1 {
		t.Fatalf("want 👀, got %d reactions", len(reactor.snapshot()))
	}
}

func TestDefaultRoute_UnlistedSender_SilentDrop(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, pub, reactor, _ := routedHandler(t, now)
	h.Slack = &fakeSlackPoster{}
	h.DefaultRouter = true // even with the Phase 96 router on, no orphan reply
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0OTHER", "<@UBOT123> hi", "1.0", "", "DR2"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 || len(threads.upserts) != 0 || len(pub.calls) != 0 || len(reactor.snapshot()) != 0 {
		t.Fatal("unlisted sender must produce nothing")
	}
	if len(h.Slack.(*fakeSlackPoster).msgs) != 0 {
		t.Fatal("no orphan reply when a route is configured")
	}
}

func TestDefaultRoute_NoMention_Dropped_ButDMNeedsNone(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := routedHandler(t, now)
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "no mention here", "1.0", "", "DR3a"))
	if len(sqs.sends) != 0 {
		t.Fatal("channel message without mention must be dropped")
	}
	h.Handle(context.Background(), routedRequest(t, now, "DKURT", "im", "U0KURT", "no mention here", "2.0", "", "DR3b"))
	if len(sqs.sends) != 1 {
		t.Fatal("DM without mention must dispatch")
	}
	h.Handle(context.Background(), routedRequest(t, now, "GMPIM", "mpim", "U0KURT", "no mention here", "3.0", "", "DR3c"))
	if len(sqs.sends) != 1 {
		t.Fatal("group DM without mention must be dropped")
	}
}

func TestDefaultRoute_ThreadContinuity_NoMention_NoRelay(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := routedHandler(t, now)
	relay := &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}} // would stop everything if consulted
	h.Relayer = relay
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "follow-up, no mention", "1.5", "1.0", "DR4"))
	if len(sqs.sends) != 1 {
		t.Fatalf("continuity reply must dispatch; sends=%d", len(sqs.sends))
	}
	if relay.calls != 0 {
		t.Fatal("continuity must be decided before the relay")
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.ThreadTS != "1.0" {
		t.Fatalf("thread_ts %q", body.ThreadTS)
	}
}

func TestDefaultRoute_ThreadContinuity_StillGatedOnEmail(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := routedHandler(t, now)
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0OTHER", "me too", "1.5", "1.0", "DR5"))
	if len(sqs.sends) != 0 {
		t.Fatal("unlisted sender in an established thread must be dropped")
	}
}

func TestDefaultRoute_ThreadContinuity_RebindsWhenAliasMoved(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, _, _, _ := routedHandler(t, now)
	alias.info = SandboxRoutingInfo{SandboxID: "sb-irbot2", QueueURL: "https://sqs.example/irbot2.fifo"}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "hello again", "1.5", "1.0", "DR6"))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/irbot2.fifo" {
		t.Fatalf("must enqueue to the recreated box; sends=%+v", sqs.sends)
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sb-irbot2" {
		t.Fatalf("thread row must be rebound; rebinds=%+v", threads.rebinds)
	}
}

func TestDefaultRoute_NotConfigured_Phase96Unchanged(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{}
	poster := &fakeSlackPoster{}
	h.Slack = poster
	h.DefaultRouter = true
	h.Relayer = &fakeRelayer{}
	h.RouterCooldown = &fakeCooldownStore{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR7"))
	if len(sqs.sends) != 0 {
		t.Fatal("no dispatch without a route")
	}
	if len(poster.msgs) != 1 {
		t.Fatalf("Phase 96 orphan reply must still fire; got %d", len(poster.msgs))
	}
}

func TestDefaultRoute_PeerClaim_RouteNotEvaluated(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, emails := routedHandler(t, now)
	h.Relayer = &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR8"))
	if len(sqs.sends) != 0 || alias.calls != 0 || emails.calls != 0 {
		t.Fatal("a peer claim must end processing before the route")
	}
}

func TestDefaultRoute_AliasAbsent_ColdCreateCarriesEnvelope(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, pub, reactor, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR9"))
	if len(sqs.sends) != 0 {
		t.Fatal("cold-create must not enqueue (no queue exists yet)")
	}
	if len(pub.calls) != 1 || pub.calls[0].alias != "ir-bot" || pub.calls[0].profile != "profiles/ir-bot.yaml" {
		t.Fatalf("publisher calls %+v", pub.calls)
	}
	var body InboundQueueBody
	if err := json.Unmarshal([]byte(pub.calls[0].envelope), &body); err != nil || body.Channel != "CFOREIGN" || body.Text == "" {
		t.Fatalf("envelope must be an InboundQueueBody: %s err=%v", pub.calls[0].envelope, err)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].sb != "sl-cold1" {
		t.Fatalf("thread row must name the minted sandbox id; %+v", threads.upserts)
	}
	if len(reactor.snapshot()) != 1 {
		t.Fatal("👀 on cold-create too")
	}
}

func TestDefaultRoute_AliasStopped_ResumesThenEnqueues(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	alias.info.Paused = true
	resumer := &mockSandboxResumer{}
	h.Resumer = resumer
	h.StatusWriter = &mockSandboxStatusWriter{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> wake", "1.0", "", "DR10"))
	if len(sqs.sends) != 1 {
		t.Fatal("stopped alias must still enqueue")
	}
	if calls, id := resumer.snapshot(); calls != 1 || id != "sb-irbot" {
		t.Fatalf("resumer calls=%d id=%s", calls, id)
	}
}

func TestDefaultRoute_AliasFrozen_NoticeNoDispatch(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	alias.info.ActionFrozen = true
	alias.info.FrozenReason = "quota"
	poster := &fakeSlackPoster{}
	h.Slack = poster
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR11"))
	if len(sqs.sends) != 0 {
		t.Fatal("frozen must not dispatch")
	}
	if len(poster.msgs) != 1 || !strings.Contains(poster.msgs[0].body, "frozen") || poster.msgs[0].channel != "CFOREIGN" {
		t.Fatalf("frozen notice %+v", poster.msgs)
	}
}

func TestDefaultRoute_EmailLookupFailureIsDeny(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, emails := routedHandler(t, now)
	emails.err = errors.New("slack: ratelimited")
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR12"))
	if len(sqs.sends) != 0 {
		t.Fatal("lookup error must deny")
	}
}

func TestDefaultRoute_RelayedRequest_StaysTerminal(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	req := routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR13")
	req.Headers["x-km-relayed"] = "1"
	resp := h.Handle(context.Background(), req)
	if len(sqs.sends) != 0 || alias.calls != 0 {
		t.Fatal("a relayed miss must never route; peers are not front doors")
	}
	if !strings.Contains(resp.Body, `"claimed":false`) {
		t.Fatalf("body %s", resp.Body)
	}
}
```

Fake names verified against the package: `fakeSlackPoster` records `msgs []fakeSlackMsg` with `channel, subject, body, threadTS` fields (`events_handler_test.go:22-37`); the Phase 96 cooldown fake is `fakeCooldownStore` (`events_handler_test.go:1897`).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd pkg/slack/bridge && go test -run 'TestDefaultRoute_' .`
Expected: FAIL (fields `DefaultRoute`, `AliasResolver`, … undefined).

- [ ] **Step 4: Implement the ladder in `Handle`**

Add fields to `EventsHandler` after `OrphanHinter`:

```go
	// 2026-10-16: default route (nil => OFF; Phase 96/118 byte-identical).
	DefaultRoute    *DefaultRoute
	SandboxByID     SandboxByIDFetcher
	AliasResolver   SlackAliasResolver
	CreatePublisher SandboxCreatePublisher
	SenderEmails    SenderEmailFetcher
```

Replace the body of the `if info.SandboxID == "" || info.QueueURL == ""` miss block so it reads (keeping the existing relayed-terminal branch verbatim):

```go
	routed := false     // info came from the default route, not channel ownership
	coldCreate := false // alias absent: publish instead of enqueue
	if info.SandboxID == "" || info.QueueURL == "" {
		// Ladder step 1: thread continuity. A thread the front door routed earlier
		// carries its sandbox in km-slack-threads. Decide this BEFORE the relay so
		// a follow-up never fans out to peers.
		continuity := false
		if h.DefaultRoute != nil && msg.ThreadTS != "" && req.Headers["x-km-relayed"] == "" {
			if sb, lookupErr := h.Threads.LookupSandbox(ctx, msg.Channel, msg.ThreadTS); lookupErr != nil {
				h.log().Warn("events: route: thread lookup failed", "err", lookupErr, "channel", msg.Channel)
			} else if sb != "" {
				continuity = true
			}
		}
		if !continuity {
			// Ladder step 2: relay (unchanged Phase 95/96 decision table).
			if req.Headers["x-km-relayed"] != "" {
				/* existing terminal {claimed:false, channels:[...]} block, verbatim */
			}
			anyClaimed := false
			var claimResults []PeerClaimResult
			if h.Relayer != nil {
				var broadcastErr error
				claimResults, broadcastErr = h.Relayer.Broadcast(ctx, req.Body, req.Headers)
				if broadcastErr != nil {
					h.log().Warn("events: relay broadcast partial failure", "err", broadcastErr, "channel", msg.Channel)
				}
				for _, r := range claimResults {
					if r.Claimed {
						anyClaimed = true
						break
					}
				}
			}
			if anyClaimed {
				return EventsResponse{StatusCode: 200, Body: "ok"}
			}
			// Ladder step 3: route selection.
			if h.DefaultRoute == nil {
				if h.Relayer != nil {
					h.maybePostOrphanReply(ctx, msg, claimResults)
				} else {
					h.log().Warn("events: unknown channel or inbound disabled", "channel", msg.Channel)
				}
				return EventsResponse{StatusCode: 200, Body: "ok"}
			}
		}
		// Ladder step 4+5: gate and resolve.
		var ok bool
		info, coldCreate, ok = h.resolveDefaultRoute(ctx, msg, continuity)
		if !ok {
			return EventsResponse{StatusCode: 200, Body: "ok"}
		}
		routed = true
	}
```

Add the resolver method:

```go
// resolveDefaultRoute runs ladder steps 4 (mention + email gate) and 5 (alias
// resolve, continuity rebind). ok=false means "drop silently, 200". coldCreate
// is true when the alias has no row and the caller must publish, not enqueue.
func (h *EventsHandler) resolveDefaultRoute(ctx context.Context, msg slackMessageEvent, continuity bool) (info SandboxRoutingInfo, coldCreate bool, ok bool) {
	if !continuity && requiresMention(msg.ChannelType) {
		uid, err := h.BotUserID.Fetch(ctx)
		if err != nil {
			h.log().Warn("events: route: bot_user_id fetch failed; dropping", "err", err)
			return info, false, false
		}
		if uid == "" || !strings.Contains(msg.Text, "<@"+uid+">") {
			h.log().Debug("events: route: no mention", "channel", msg.Channel, "channel_type", msg.ChannelType)
			return info, false, false
		}
	}
	if h.SenderEmails == nil {
		h.log().Warn("events: route: no SenderEmails fetcher wired; dropping")
		return info, false, false
	}
	email, err := h.SenderEmails.SenderEmail(ctx, msg.User)
	if err != nil {
		h.log().Debug("events: route: sender email unavailable (deny)", "user", msg.User, "err", err)
		return info, false, false
	}
	if !h.DefaultRoute.Allows(email) {
		h.log().Debug("events: route: sender not allowlisted (silent drop)", "user", msg.User, "channel", msg.Channel)
		return info, false, false
	}

	current, found, err := h.AliasResolver.ResolveAlias(ctx, h.DefaultRoute.Alias)
	if err != nil {
		h.log().Error("events: route: alias resolve failed", "alias", h.DefaultRoute.Alias, "err", err)
		return info, false, false
	}
	if continuity {
		rowSB, _ := h.Threads.LookupSandbox(ctx, msg.Channel, msg.ThreadTS)
		if found && current.SandboxID != rowSB {
			h.log().Info("events: route: alias moved; rebinding thread", "from", rowSB, "to", current.SandboxID)
			if rerr := h.Threads.Rebind(ctx, msg.Channel, msg.ThreadTS, current.SandboxID); rerr != nil {
				h.log().Warn("events: route: rebind failed (continuing)", "err", rerr)
			}
			return current, false, true
		}
		if !found {
			return info, true, true // old box gone and alias absent: cold-create
		}
		byID, ferr := h.SandboxByID.FetchBySandboxID(ctx, rowSB)
		if ferr != nil || byID.SandboxID == "" {
			return current, false, true
		}
		return byID, false, true
	}
	if !found {
		return info, true, true
	}
	return current, false, true
}
```

Then, in the shared tail:

- Step 5a (Phase 118 allow) and step 5b (mention-only): wrap both in `if !routed { … }`. The route gate already ran.
- Step 5c (frozen): unchanged; it runs for routed boxes too (`info.ActionFrozen`).
- Step 7 (thread upsert): for `coldCreate` the sandbox id is unknown until publish; move the upsert after delivery (see below).
- Step 8: factor both branches so they build `body InboundQueueBody` and call one helper:

```go
// deliver enqueues a routed/owned turn, or publishes a cold-create carrying the
// envelope. Returns the sandbox id the thread row must name.
func (h *EventsHandler) deliver(ctx context.Context, info SandboxRoutingInfo, coldCreate bool, body InboundQueueBody, threadTS, dedupID string) (string, error) {
	bodyBytes, _ := json.Marshal(body)
	if coldCreate {
		id, err := h.CreatePublisher.PutSandboxCreate(ctx, h.DefaultRoute.Alias, h.DefaultRoute.Profile, string(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("cold-create publish: %w", err)
		}
		h.log().Info("events: route: cold-create published", "alias", h.DefaultRoute.Alias, "sandbox", id, "channel", body.Channel)
		return id, nil
	}
	if err := h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), threadTS, dedupID); err != nil {
		return "", err
	}
	return info.SandboxID, nil
}
```

Both the files-sync branch and the plain branch call `deliver` in place of their `h.SQS.Send`, log on error and return 200 as today, and on success write the thread row with the returned id (replacing the step-7 upsert, which moves to after delivery for every path; the fake-threads assertions in existing tests still hold because the write still happens exactly once on success and still before the reaction). The write is:

```go
	if routed && coldCreate && msg.ThreadTS != "" {
		// Orphaned thread whose old box is gone and whose alias is absent: the
		// existing row still names the dead sandbox and a conditional Upsert
		// would leave it there. Rebind it to the box we just minted.
		if rerr := h.Threads.Rebind(ctx, msg.Channel, threadTS, deliveredSandboxID); rerr != nil {
			h.log().Warn("events: threads rebind after cold-create", "err", rerr)
		}
	} else if err := h.Threads.Upsert(ctx, msg.Channel, threadTS, deliveredSandboxID); err != nil {
		h.log().Warn("events: threads upsert", "err", err, "channel", msg.Channel, "thread_ts", threadTS)
	}
```

Add one test for that path to `events_handler_default_route_test.go`:

```go
func TestDefaultRoute_OrphanThread_AliasAbsent_ColdCreateRebinds(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-dead"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "still there?", "1.5", "1.0", "DR14"))
	if len(pub.calls) != 1 {
		t.Fatalf("want cold-create, got %d publishes", len(pub.calls))
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sl-cold1" {
		t.Fatalf("orphaned thread row must be rebound to the minted box; rebinds=%+v upserts=%+v", threads.rebinds, threads.upserts)
	}
}
``` Existing `TestEventsHandler_DDBUpsertFailure_Returns200` and `TestEventsHandler_SQSWriteFailure_Returns200` must keep passing: an upsert failure after a successful send is logged and ignored exactly as before.

- Step 9 (resume): unchanged; `info.Paused` on a routed box resumes it.
- Step 10 (reaction): unchanged.

- [ ] **Step 5: Run the whole package**

Run: `cd pkg/slack/bridge && go test .`
Expected: PASS, including every pre-existing `TestEventsHandler_*` test (the orphan-reply, relay, allowlist, mention-only, paused, reactor, and files tests all exercise the owned path or the route-OFF miss path, which must be unchanged).

- [ ] **Step 6: Commit**

```bash
git add pkg/slack/bridge/events_handler.go pkg/slack/bridge/events_interfaces.go pkg/slack/bridge/aws_adapters.go pkg/slack/bridge/events_handler_test.go pkg/slack/bridge/lookup_thread_handler_test.go pkg/slack/bridge/events_handler_default_route_test.go
git commit -m "feat(slack-bridge): default-route ladder on the channel-miss path" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Bridge AWS/Slack adapters for the route

**Files:**
- Modify: `pkg/slack/bridge/aws_adapters.go` (refactor `FetchByChannel` item parse into `routingInfoFromItem`; add `DDBSandboxByID`, `DDBAliasResolver`, `EventBridgeAdapter`, `SlackSenderEmailAdapter`)
- Test: `pkg/slack/bridge/aws_adapters_default_route_test.go` (create)

**Interfaces:**
- Consumes: `kmaws.SandboxCreateDetail` with the new `SlackEnvelope` field (Task 9 adds it; to keep this task compilable on its own, Task 9 is ordered BEFORE this one in execution — see the note in Step 3).
- Produces: `DDBSandboxByID{Client DDBQueryGetPutAPI; TableName string}`, `DDBAliasResolver{Client DDBQueryGetPutAPI; TableName string}`, `EventBridgeAdapter{Client kmaws.EventBridgeAPI; ArtifactBucket string}`, `SlackSenderEmailAdapter{Tokens BotTokenFetcher; HTTPClient *http.Client; BaseURL string}`.

> Execution note: do Task 9 (pkg/aws `SlackEnvelope` field + create-handler drain) before this task. The numbering here follows the spec's reading order, not the build order.

- [ ] **Step 1: Write the failing tests**

```go
package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func irbotItem(id string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":              &dynamodbtypes.AttributeValueMemberS{Value: id},
		"alias":                   &dynamodbtypes.AttributeValueMemberS{Value: "ir-bot"},
		"slack_inbound_queue_url": &dynamodbtypes.AttributeValueMemberS{Value: "https://sqs/" + id + ".fifo"},
		"status":                  &dynamodbtypes.AttributeValueMemberS{Value: "stopped"},
		"action_frozen":           &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
	}
}

func TestDDBSandboxByID_ParsesSameFieldsAsByChannel(t *testing.T) {
	m := &mockDDBQueryGetPut{getItem: func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
		if v := in.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "sb-1" {
			t.Errorf("key %q", v)
		}
		return &dynamodb.GetItemOutput{Item: irbotItem("sb-1")}, nil
	}}
	info, err := (&DDBSandboxByID{Client: m, TableName: "km-sandboxes"}).FetchBySandboxID(context.Background(), "sb-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.SandboxID != "sb-1" || info.QueueURL != "https://sqs/sb-1.fifo" || !info.Paused || !info.ActionFrozen {
		t.Fatalf("info %+v", info)
	}
}

func TestDDBAliasResolver_FoundAbsentAmbiguous(t *testing.T) {
	items := []map[string]dynamodbtypes.AttributeValue{}
	m := &mockDDBQueryGetPut{
		query: func(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			if awssdk.ToString(in.IndexName) != "alias-index" {
				t.Errorf("index %q", awssdk.ToString(in.IndexName))
			}
			return &dynamodb.QueryOutput{Items: items}, nil
		},
		getItem: func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: irbotItem("sb-1")}, nil
		},
	}
	r := &DDBAliasResolver{Client: m, TableName: "km-sandboxes"}

	_, found, err := r.ResolveAlias(context.Background(), "ir-bot")
	if err != nil || found {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
	items = []map[string]dynamodbtypes.AttributeValue{{"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-1"}}}
	info, found, err := r.ResolveAlias(context.Background(), "ir-bot")
	if err != nil || !found || info.QueueURL != "https://sqs/sb-1.fifo" {
		t.Fatalf("found: %+v %v %v", info, found, err)
	}
	items = append(items, items[0])
	if _, _, err := r.ResolveAlias(context.Background(), "ir-bot"); err == nil {
		t.Fatal("ambiguous alias must error")
	}
}

type mockEventBridge struct{ entries []string }

func (m *mockEventBridge) PutEvents(_ context.Context, in *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	for _, e := range in.Entries {
		m.entries = append(m.entries, awssdk.ToString(e.Detail))
	}
	return &eventbridge.PutEventsOutput{}, nil
}

func TestEventBridgeAdapter_PutSandboxCreate(t *testing.T) {
	eb := &mockEventBridge{}
	a := &EventBridgeAdapter{Client: eb, ArtifactBucket: "km-artifacts"}
	id, err := a.PutSandboxCreate(context.Background(), "ir-bot", "profiles/IR-Bot.yaml", `{"channel":"C1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "sl-") {
		t.Fatalf("id %q", id)
	}
	var d kmaws.SandboxCreateDetail
	json.Unmarshal([]byte(eb.entries[0]), &d)
	if d.SandboxID != id || d.Alias != "ir-bot" || d.ArtifactPrefix != "slack-profiles/ir-bot" || d.SlackEnvelope != `{"channel":"C1"}` || d.CreatedBy != "slack" {
		t.Fatalf("detail %+v", d)
	}
}

type staticTokens struct{}

func (staticTokens) Fetch(_ context.Context) (string, error) { return "xoxb-test", nil }

func TestSlackSenderEmailAdapter(t *testing.T) {
	var reply string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	a := &SlackSenderEmailAdapter{Tokens: staticTokens{}, HTTPClient: srv.Client(), BaseURL: srv.URL}

	reply = `{"ok":true,"user":{"id":"U1","profile":{"email":"A@B.C"}}}`
	if e, err := a.SenderEmail(context.Background(), "U1"); err != nil || e != "a@b.c" {
		t.Fatalf("%q %v", e, err)
	}
	for _, bad := range []string{
		`{"ok":true,"user":{"id":"U1","deleted":true,"profile":{"email":"a@b.c"}}}`,
		`{"ok":true,"user":{"id":"U1","is_bot":true,"profile":{"email":"a@b.c"}}}`,
		`{"ok":true,"user":{"id":"U1","profile":{}}}`,
	} {
		reply = bad
		if _, err := a.SenderEmail(context.Background(), "U1"); err == nil {
			t.Fatalf("want ErrSenderIneligible for %s", bad)
		}
	}
}
```

Confirm `mockDDBQueryGetPut` lives in `aws_adapters_test.go` (same package); if its `query`/`getItem` field names differ, use the real ones. If no `EventBridgeAPI` import path exists in `pkg/aws`, it is defined in `pkg/aws/idle_event.go` as `EventBridgeAPI` with a `PutEvents` method.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd pkg/slack/bridge && go test -run 'DDBSandboxByID|DDBAliasResolver|EventBridgeAdapter|SlackSenderEmailAdapter' .`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

In `aws_adapters.go`, extract the parsing body of `FetchByChannel` (everything after `item := out.Items[0]`) into:

```go
// routingInfoFromItem decodes a km-sandboxes row into SandboxRoutingInfo. Shared by
// FetchByChannel (GSI), FetchBySandboxID (GetItem) and ResolveAlias (GSI → GetItem)
// so the three can never disagree about an attribute name.
func routingInfoFromItem(item map[string]dynamodbtypes.AttributeValue) SandboxRoutingInfo {
	info := SandboxRoutingInfo{}
	/* existing field-by-field parse, verbatim */
	return info
}
```

and have `FetchByChannel` return `routingInfoFromItem(out.Items[0]), nil`.

Append:

```go
// DDBSandboxByID implements SandboxByIDFetcher (GetItem on km-sandboxes).
type DDBSandboxByID struct {
	Client    DDBQueryGetPutAPI
	TableName string
}

func (f *DDBSandboxByID) FetchBySandboxID(ctx context.Context, sandboxID string) (SandboxRoutingInfo, error) {
	out, err := f.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(f.TableName),
		Key:       map[string]dynamodbtypes.AttributeValue{"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID}},
	})
	if err != nil {
		return SandboxRoutingInfo{}, fmt.Errorf("sandbox-by-id get %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return SandboxRoutingInfo{}, nil
	}
	return routingInfoFromItem(out.Item), nil
}

// DDBAliasResolver implements SlackAliasResolver: alias-index GSI → sandbox_id →
// GetItem (the GSI projection does not carry every routing attribute).
type DDBAliasResolver struct {
	Client    DDBQueryGetPutAPI
	TableName string
}

func (r *DDBAliasResolver) ResolveAlias(ctx context.Context, alias string) (SandboxRoutingInfo, bool, error) {
	out, err := r.Client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(r.TableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(2),
	})
	if err != nil {
		return SandboxRoutingInfo{}, false, fmt.Errorf("slack-bridge: resolve alias %q: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return SandboxRoutingInfo{}, false, nil
	}
	if len(out.Items) > 1 {
		return SandboxRoutingInfo{}, false, fmt.Errorf("slack-bridge: alias %q is ambiguous (matched multiple sandboxes)", alias)
	}
	sv, ok := out.Items[0]["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sv.Value == "" {
		return SandboxRoutingInfo{}, false, fmt.Errorf("slack-bridge: alias %q: GSI item missing sandbox_id", alias)
	}
	info, err := (&DDBSandboxByID{Client: r.Client, TableName: r.TableName}).FetchBySandboxID(ctx, sv.Value)
	if err != nil {
		return SandboxRoutingInfo{}, false, err
	}
	if info.SandboxID == "" {
		return SandboxRoutingInfo{}, false, nil // GSI lag: row gone
	}
	return info, true, nil
}

// EventBridgeAdapter implements SandboxCreatePublisher. ArtifactPrefix follows the
// slack-profiles/{slug} convention that internal/app/cmd.PreStageSlackDefaultRouteProfile
// uploads to; the slug rule must match SlackDefaultRouteProfileSlug.
type EventBridgeAdapter struct {
	Client         kmaws.EventBridgeAPI
	ArtifactBucket string
}

func slackProfileSlug(profile string) string {
	base := filepath.Base(profile)
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(strings.ToLower(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ToLower(base)
}

func generateSlackSandboxID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("slack-bridge: generateSlackSandboxID: %v", err))
	}
	return "sl-" + hex.EncodeToString(b)
}

func (a *EventBridgeAdapter) PutSandboxCreate(ctx context.Context, alias, profile, slackEnvelopeJSON string) (string, error) {
	id := generateSlackSandboxID()
	detail := kmaws.SandboxCreateDetail{
		SandboxID:      id,
		ArtifactBucket: a.ArtifactBucket,
		ArtifactPrefix: "slack-profiles/" + slackProfileSlug(profile),
		Alias:          alias,
		CreatedBy:      "slack",
		SlackEnvelope:  slackEnvelopeJSON,
	}
	if err := kmaws.PutSandboxCreateEvent(ctx, a.Client, detail); err != nil {
		return "", fmt.Errorf("slack-bridge: cold-create: %w", err)
	}
	return id, nil
}

// SlackSenderEmailAdapter implements SenderEmailFetcher over users.info.
type SlackSenderEmailAdapter struct {
	Tokens     BotTokenFetcher
	HTTPClient *http.Client
	BaseURL    string // tests only; empty = Slack default
}

func (a *SlackSenderEmailAdapter) SenderEmail(ctx context.Context, userID string) (string, error) {
	tok, err := a.Tokens.Fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("slack-bridge: bot token: %w", err)
	}
	c := pkgslack.NewClient(tok, a.HTTPClient)
	if a.BaseURL != "" {
		c.SetBaseURL(a.BaseURL)
	}
	u, err := c.UserInfo(ctx, userID)
	if err != nil {
		return "", err
	}
	if u.Deleted || u.IsBot || u.Email == "" {
		return "", ErrSenderIneligible
	}
	return u.Email, nil
}
```

Add imports as needed: `crypto/rand`, `encoding/hex`, `net/http`, `path/filepath`, `kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"`, `pkgslack "github.com/whereiskurt/klanker-maker/pkg/slack"` (check the file's existing alias for pkg/slack and reuse it).

- [ ] **Step 4: Run the tests**

Run: `cd pkg/slack/bridge && go test .`
Expected: PASS, including every existing `FetchByChannel` test (the refactor must be behaviour-preserving).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/aws_adapters.go pkg/slack/bridge/aws_adapters_default_route_test.go
git commit -m "feat(slack-bridge): by-id fetcher, alias resolver, cold-create publisher, sender email adapter" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: Bridge Lambda wiring from env

**Files:**
- Modify: `cmd/km-slack-bridge/main.go` (globals near line 63; `init()` near line 87; wiring block after the `KM_SLACK_DEFAULT_ROUTER` block near line 340)
- Test: `cmd/km-slack-bridge/default_route_wiring_test.go` (create)

**Interfaces:**
- Consumes: Task 4–6 types.
- Produces: `wireDefaultRoute(h *bridge.EventsHandler, getenv func(string) string, deps defaultRouteDeps)` (testable pure function).

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestWireDefaultRoute_AllThreeOrNothing(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	deps := defaultRouteDeps{} // nil clients are fine: wiring only, no calls

	h := &bridge.EventsHandler{}
	wireDefaultRoute(h, get, deps)
	if h.DefaultRoute != nil {
		t.Fatal("absent env must leave the route OFF")
	}

	env["KM_SLACK_DEFAULT_ROUTE_ALIAS"] = "ir-bot"
	env["KM_SLACK_DEFAULT_ROUTE_PROFILE"] = "profiles/ir-bot.yaml"
	h = &bridge.EventsHandler{}
	wireDefaultRoute(h, get, deps)
	if h.DefaultRoute != nil {
		t.Fatal("missing ALLOW must leave the route OFF")
	}

	env["KM_SLACK_DEFAULT_ROUTE_ALLOW"] = "kurt@example.com, Other@Example.com"
	h = &bridge.EventsHandler{}
	wireDefaultRoute(h, get, deps)
	if h.DefaultRoute == nil || !h.DefaultRoute.Allows("other@example.com") {
		t.Fatal("route must be ON with both emails")
	}
	if h.AliasResolver == nil || h.CreatePublisher == nil || h.SenderEmails == nil || h.SandboxByID == nil {
		t.Fatal("all four deps must be wired when the route is ON")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cmd/km-slack-bridge && go test -run WireDefaultRoute .`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

Add to the globals: `initEventBridge *eventbridge.Client`; in `init()`: `initEventBridge = eventbridge.NewFromConfig(cfg)` (import `github.com/aws/aws-sdk-go-v2/service/eventbridge`).

Add:

```go
// defaultRouteDeps carries the clients wireDefaultRoute needs; a struct so the
// wiring is testable without AWS.
type defaultRouteDeps struct {
	ddb            bridge.DDBQueryGetPutAPI
	sandboxesTable string
	eventBridge    kmaws.EventBridgeAPI
	artifactBucket string
	tokens         bridge.BotTokenFetcher
	httpClient     *http.Client
}

// wireDefaultRoute reads KM_SLACK_DEFAULT_ROUTE_{ALIAS,PROFILE,ALLOW}. All three
// or nothing: bridge.NewDefaultRoute returns nil for an empty allowlist, so a
// half-set env leaves the route OFF rather than open.
func wireDefaultRoute(h *bridge.EventsHandler, getenv func(string) string, d defaultRouteDeps) {
	alias := strings.TrimSpace(getenv("KM_SLACK_DEFAULT_ROUTE_ALIAS"))
	profile := strings.TrimSpace(getenv("KM_SLACK_DEFAULT_ROUTE_PROFILE"))
	var emails []string
	for _, e := range strings.Split(getenv("KM_SLACK_DEFAULT_ROUTE_ALLOW"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			emails = append(emails, e)
		}
	}
	route := bridge.NewDefaultRoute(alias, profile, emails)
	if route == nil {
		if alias != "" || profile != "" || len(emails) > 0 {
			slog.Warn("km-slack-bridge: default route partially configured; staying OFF",
				"alias_set", alias != "", "profile_set", profile != "", "emails", len(emails))
		}
		return
	}
	h.DefaultRoute = route
	h.SandboxByID = &bridge.DDBSandboxByID{Client: d.ddb, TableName: d.sandboxesTable}
	h.AliasResolver = &bridge.DDBAliasResolver{Client: d.ddb, TableName: d.sandboxesTable}
	h.CreatePublisher = &bridge.EventBridgeAdapter{Client: d.eventBridge, ArtifactBucket: d.artifactBucket}
	h.SenderEmails = &bridge.CachedSenderEmailFetcher{
		Inner: &bridge.SlackSenderEmailAdapter{Tokens: d.tokens, HTTPClient: d.httpClient},
		TTL:   15 * time.Minute,
	}
	slog.Info("km-slack-bridge: default route enabled", "alias", alias, "profile", profile, "allow_count", len(emails))
}
```

Call it right after the Phase 96 router block in `init()`:

```go
	wireDefaultRoute(eventsHandler, os.Getenv, defaultRouteDeps{
		ddb: initDDB, sandboxesTable: sandboxesTable,
		eventBridge: initEventBridge, artifactBucket: artifactsBucket,
		tokens: initToken, httpClient: initHTTPClient,
	})
```

(`artifactsBucket` is already read from `KM_ARTIFACTS_BUCKET` near line 149 of this file; if the variable is scoped elsewhere, read `os.Getenv("KM_ARTIFACTS_BUCKET")` inline.)

- [ ] **Step 4: Run the tests and build**

Run: `cd cmd/km-slack-bridge && go test . && GOOS=linux GOARCH=arm64 go build -o /dev/null .`
Expected: PASS and a clean cross-build.

- [ ] **Step 5: Commit**

```bash
git add cmd/km-slack-bridge/main.go cmd/km-slack-bridge/default_route_wiring_test.go
git commit -m "feat(slack-bridge): wire the default route from KM_SLACK_DEFAULT_ROUTE_* env" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: Outbound authorizer admits thread-scoped posts

**Files:**
- Modify: `pkg/slack/bridge/handler.go` (step 6, lines ~255–283)
- Test: `pkg/slack/bridge/handler_thread_authz_test.go` (create, package `bridge_test`)

**Interfaces:**
- Consumes: `Handler.Threads SlackThreadStore` (already a field, may be nil), `slack.SlackEnvelope.ThreadTS`.

- [ ] **Step 1: Write the failing tests**

```go
package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

type routedThreads struct {
	rows map[string]string // "chan|ts" → sandbox_id
}

func (r *routedThreads) Get(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *routedThreads) Upsert(_ context.Context, _, _, _ string) error     { return nil }
func (r *routedThreads) Rebind(_ context.Context, _, _, _ string) error     { return nil }
func (r *routedThreads) LookupSandbox(_ context.Context, ch, ts string) (string, error) {
	return r.rows[ch+"|"+ts], nil
}
func (r *routedThreads) LookupBySession(_ context.Context, _, _ string) (string, string, string, error) {
	return "", "", "", nil
}

func TestHandler_ForeignChannel_RoutedThread_Allowed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_ForeignChannel_WrongSandboxRow_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-someone-else"}}
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "channel_mismatch" {
		t.Fatalf("want 403 channel_mismatch, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_ForeignChannel_NoThread_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	for _, action := range []string{slack.ActionPost, slack.ActionPermalink, slack.ActionUpdate} {
		env := makeEnv(action, "sb-abc123", "CFOREIGN")
		env.MessageTS = "1.0" // permalink/update carry a message ts, never a thread
		resp := h.Handle(context.Background(), signRequest(t, env, priv))
		if resp.StatusCode != 403 {
			t.Fatalf("%s without thread_ts in a foreign channel must be 403, got %d", action, resp.StatusCode)
		}
	}
}

func TestHandler_ForeignChannel_NilThreads_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub) // Threads nil
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	if resp := h.Handle(context.Background(), signRequest(t, env, priv)); resp.StatusCode != 403 {
		t.Fatalf("nil thread store must fail closed, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd pkg/slack/bridge && go test -run 'ForeignChannel' .`
Expected: `TestHandler_ForeignChannel_RoutedThread_Allowed` FAILS (403); the three 403 tests already pass.

- [ ] **Step 3: Implement**

In `handler.go` step 6, replace `if owned == "" || owned != env.Channel {` with:

```go
			if owned == "" || owned != env.Channel {
				// 2026-10-16: default route. A sandbox may post into a thread the
				// events handler routed to it (km-slack-threads row names it). The
				// bridge does the lookup — the sandbox never reads DDB — and only a
				// thread-anchored envelope qualifies: permalink/update/top-level posts
				// in a foreign channel stay refused.
				if !h.threadRoutedToSender(ctx, env) {
					logger.WarnContext(ctx, "bridge: channel_mismatch",
						"step", "authz", "sender_id", env.SenderID, "channel", env.Channel,
						"owned_channel", owned, "thread_ts", env.ThreadTS, "status", 403)
					return errResp(403, "channel_mismatch")
				}
				logger.InfoContext(ctx, "bridge: authz_routed_thread",
					"sender_id", env.SenderID, "channel", env.Channel, "thread_ts", env.ThreadTS)
			}
```

and add:

```go
// threadRoutedToSender reports whether km-slack-threads binds (channel, thread_ts)
// to the sender. Fails closed on a nil store, empty thread, or lookup error.
func (h *Handler) threadRoutedToSender(ctx context.Context, env slack.SlackEnvelope) bool {
	if h.Threads == nil || env.ThreadTS == "" {
		return false
	}
	sb, err := h.Threads.LookupSandbox(ctx, env.Channel, env.ThreadTS)
	if err != nil {
		logger.WarnContext(ctx, "bridge: routed_thread_lookup_failed", "err", err.Error(), "channel", env.Channel)
		return false
	}
	return sb != "" && sb == env.SenderID
}
```

- [ ] **Step 4: Run the package tests**

Run: `cd pkg/slack/bridge && go test .`
Expected: PASS (existing `TestHandler_ChannelMismatch_403` and `TestHandler_SandboxPost_NoOwnedChannel_403` still pass: their envelopes carry no `ThreadTS`).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/handler.go pkg/slack/bridge/handler_thread_authz_test.go
git commit -m "feat(slack-bridge): authorize sandbox posts into threads the bridge routed to it" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 9: `SlackEnvelope` on `SandboxCreateDetail` and the create-handler drain

> Execute this task BEFORE Task 6 (Task 6 compiles against the new field).

**Files:**
- Modify: `pkg/aws/eventbridge.go` (`SandboxCreateDetail` near line 21)
- Modify: `cmd/create-handler/main.go` (`CreateEvent` near line 85; drain site after the H1 block near line 482)
- Test: `cmd/create-handler/main_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `cmd/create-handler/main_test.go`:

```go
// TestCreateHandler_SlackEnvelope_EnqueuesToSlackInboundQueue: a non-empty
// slack_envelope is sent verbatim to the sandbox's slack-inbound FIFO.
func TestCreateHandler_SlackEnvelope_EnqueuesToSlackInboundQueue(t *testing.T) {
	mockSQS := &mockSQSSendAPI{}
	h := newDrainTestHandler(mockSQS)
	envelope := `{"channel":"CFOREIGN","thread_ts":"1.0","text":"<@UBOT> hi","user":"U0KURT","event_ts":"1.0"}`
	event := CreateEvent{
		SandboxID:      "sl-cold1",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "slack-profiles/ir-bot",
		SlackEnvelope:  envelope,
	}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if mockSQS.sendMessageCalled != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", mockSQS.sendMessageCalled)
	}
	got := mockSQS.sends[0]
	wantQueue := awspkg.SlackInboundQueueName(resourcePrefix(), "sl-cold1")
	if !strings.HasSuffix(got.queueURL, "/"+wantQueue) {
		t.Errorf("queue URL %q must end in %q", got.queueURL, wantQueue)
	}
	if got.body != envelope {
		t.Errorf("envelope must be forwarded verbatim; got %q", got.body)
	}
}

func TestCreateHandler_SlackEnvelope_Empty_NoEnqueue(t *testing.T) {
	mockSQS := &mockSQSSendAPI{}
	h := newDrainTestHandler(mockSQS)
	event := CreateEvent{SandboxID: "sb-plain", ArtifactBucket: "km-artifacts", ArtifactPrefix: "remote-create/sb-plain"}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if mockSQS.sendMessageCalled != 0 {
		t.Errorf("expected 0 SendMessage calls, got %d", mockSQS.sendMessageCalled)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd cmd/create-handler && go test -run 'SlackEnvelope' .`
Expected: FAIL to compile (`SlackEnvelope` field unknown).

- [ ] **Step 3: Implement**

`pkg/aws/eventbridge.go`, after `GithubEnvelope`:

```go
	// H1Envelope / SlackEnvelope: the HackerOne and Slack bridges' cold-create
	// envelopes, drained by the create-handler into the new box's h1-inbound /
	// slack-inbound FIFO after provisioning. Empty for every other create.
	H1Envelope    string `json:"h1_envelope,omitempty"`
	SlackEnvelope string `json:"slack_envelope,omitempty"`
```

(If `H1Envelope` is already present on this struct, add only `SlackEnvelope`.)

`cmd/create-handler/main.go` `CreateEvent`, after `H1Envelope`:

```go
	// SlackEnvelope carries the Slack bridge's InboundQueueBody for a default-route
	// cold-create (2026-10-16). Drained into the slack-inbound FIFO after
	// provisioning. Empty for non-slack creates (dormant).
	SlackEnvelope string `json:"slack_envelope,omitempty"`
```

Drain, after the H1 block:

```go
	// Step 6c (2026-10-16): Slack default-route cold-create drain.
	if event.SlackEnvelope != "" && h.SQSClient != nil {
		queue := awspkg.SlackInboundQueueName(resourcePrefix(), event.SandboxID)
		if enqErr := h.drainInboundEnvelope(ctx, event.SandboxID, queue, event.SlackEnvelope, "slack"); enqErr != nil {
			log.Warn().Err(enqErr).Str("sandbox_id", event.SandboxID).
				Msg("failed to enqueue slack envelope into slack-inbound queue (non-fatal — the sender can re-mention)")
		}
	}
```

- [ ] **Step 4: Run the tests**

Run: `cd cmd/create-handler && go test . && cd ../../pkg/aws && go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/aws/eventbridge.go cmd/create-handler/main.go cmd/create-handler/main_test.go
git commit -m "feat(create-handler): drain a Slack cold-create envelope into the slack-inbound queue" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 10: Poller reply routing in userdata

**Files:**
- Modify: `pkg/compiler/userdata.go` — the `km-slack-inbound-poller` heredoc: envelope parse (~line 2130), sender preamble (~2330), handoff gate (~2345), the three `dispatch_as_sandbox` blocks (~2470–2525), handoff post sites (~2356–2410), final reply post (~2620)
- Modify: `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` (hand-patch, same hunks)
- Regenerate: `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` (with the package's sanctioned capture flag; check `grep -rn "UPDATE_GOLDEN\|CAPTURE_" pkg/compiler/*_test.go` for its name)
- Test: `pkg/compiler/userdata_slack_reply_channel_test.go` (create)

**Interfaces:**
- Produces in the rendered poller: `REPLY_CHANNEL` shell variable; per-turn `export KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'`; preamble text `[Slack] From: <@U…> in <#C…>`; marker comments `# --- km-slack-reply-channel ---` / `# --- end km-slack-reply-channel ---` around the parse so the test can execute it.

- [ ] **Step 1: Write the failing tests**

```go
package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const replyChannelStart = "# --- km-slack-reply-channel ---"
const replyChannelEnd = "# --- end km-slack-reply-channel ---"

// runReplyChannelSnippet executes the marker-delimited snippet under bash with
// BODY set and returns the value of REPLY_CHANNEL it produced.
func runReplyChannelSnippet(t *testing.T, block, body string) string {
	t.Helper()
	start := strings.Index(block, replyChannelStart)
	end := strings.Index(block, replyChannelEnd)
	if start == -1 || end == -1 || end < start {
		t.Fatalf("reply-channel markers not found in slack poller block")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	script := "set -euo pipefail\nKM_SLACK_CHANNEL_ID=CHOME\nBODY=" + shellQuote(body) + "\n" + block[start:end] + "\nprintf '%s' \"$REPLY_CHANNEL\"\n"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("snippet failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestUserdata_SlackInbound_ReplyChannelComesFromEnvelope(t *testing.T) {
	block := slackPollerBlock(t)
	if got := runReplyChannelSnippet(t, block, `{"channel":"CFOREIGN","thread_ts":"1.0","text":"hi","user":"U1"}`); got != "CFOREIGN" {
		t.Fatalf("REPLY_CHANNEL=%q want CFOREIGN", got)
	}
	// Mapped channel: envelope channel equals the box's own → unchanged behaviour.
	if got := runReplyChannelSnippet(t, block, `{"channel":"CHOME","thread_ts":"1.0","text":"hi","user":"U1"}`); got != "CHOME" {
		t.Fatalf("REPLY_CHANNEL=%q want CHOME", got)
	}
}

func TestUserdata_SlackInbound_EveryPostUsesReplyChannel(t *testing.T) {
	block := slackPollerBlock(t)
	// After the parse, no post/permalink/upload inside the poller may name the
	// boot-time channel directly.
	tail := block[strings.Index(block, replyChannelEnd):]
	for _, bad := range []string{`--channel "$KM_SLACK_CHANNEL_ID"`} {
		if strings.Contains(tail, bad) {
			t.Fatalf("poller still posts to the boot-time channel: %s", bad)
		}
	}
	if strings.Count(tail, `--channel "$REPLY_CHANNEL"`) < 3 {
		t.Fatalf("expected the handoff + final-reply posts to use REPLY_CHANNEL; block:\n%s", abbreviateUD(tail))
	}
}

func TestUserdata_SlackInbound_DispatchExportsReplyChannel(t *testing.T) {
	block := slackPollerBlock(t)
	n := strings.Count(block, "export KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'")
	if n != 3 {
		t.Fatalf("want the per-turn channel exported in all 3 dispatch blocks, found %d", n)
	}
}

func TestUserdata_SlackInbound_SenderPreambleNamesChannel(t *testing.T) {
	block := slackPollerBlock(t)
	got := runSenderPreamble(t, block, `{"channel":"CFOREIGN","thread_ts":"1.2","text":"hi","user":"U0KURT"}`, "hi")
	if !strings.HasPrefix(got, "[Slack] From: <@U0KURT> in <#CFOREIGN>") {
		t.Fatalf("preamble must name the channel:\n%s", got)
	}
}

func TestUserdata_SlackInbound_HandoffSkippedInRoutedChannel(t *testing.T) {
	block := slackPollerBlock(t)
	if !strings.Contains(block, `if [ "$DO_SWITCH" -eq 1 ] && [ "$REPLY_CHANNEL" != "$KM_SLACK_CHANNEL_ID" ]; then`) {
		t.Fatalf("handoff must be skipped when the turn is in a routed (foreign) channel")
	}
}

func TestUserdata_NoSlackInbound_ByteIdentical(t *testing.T) {
	p := minimalSlackInboundProfile(t, false)
	out := compileInboundUserData(t, p)
	if strings.Contains(out, "REPLY_CHANNEL") {
		t.Fatal("profiles without Slack inbound must not render the reply-channel plumbing")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd pkg/compiler && go test -run 'ReplyChannel|SenderPreambleNamesChannel|HandoffSkipped|DispatchExportsReplyChannel|EveryPostUsesReplyChannel' .`
Expected: FAIL (markers not found / counts wrong).

- [ ] **Step 3: Edit the poller heredoc**

Right after `ATTACH_COUNT=$(echo "$BODY" | jq -r '.attachments // [] | length')` (the first parse, before the malformed-body check) add:

```bash
    # --- km-slack-reply-channel ---
    # Reply where the question was asked. A default-routed turn (2026-10-16)
    # arrives from a channel this box does not own; the bridge authorizes posts
    # into threads it routed, so every post in this turn targets the envelope's
    # channel, not the boot-time KM_SLACK_CHANNEL_ID. For a mapped channel the
    # two are equal and nothing changes.
    REPLY_CHANNEL="$CHANNEL"
    # --- end km-slack-reply-channel ---
```

If the parse happens twice (line ~2130 and again inside the backgrounded subshell at ~2181), put the markers on the FIRST and set `REPLY_CHANNEL="$CHANNEL"` again after the second parse without markers.

Sender preamble: change the printf to

```bash
        printf '[Slack] From: <@%s> in <#%s> (also in $KM_SLACK_SENDER_ID). To notify this person in your reply write <@%s> inline, or pass --mention %s to km-slack post/reply. A bare @name is plain text to Slack — never invent an id; use this one or a <@U…> token from the message.\n\n' "$SENDER_ID" "$REPLY_CHANNEL" "$SENDER_ID" "$SENDER_ID" > "$HDR_FILE"
```

Handoff gate: change `if [ "$DO_SWITCH" -eq 1 ]; then` to

```bash
      if [ "$DO_SWITCH" -eq 1 ] && [ "$REPLY_CHANNEL" != "$KM_SLACK_CHANNEL_ID" ]; then
        echo "[km-slack-inbound-poller] agent switch requested in a routed channel ($REPLY_CHANNEL); the bridge only authorizes posts into the existing thread, so continuing with $CURRENT_AGENT" >&2
        DO_SWITCH=0
      fi
      if [ "$DO_SWITCH" -eq 1 ]; then
```

Every `--channel "$KM_SLACK_CHANNEL_ID"` inside the poller heredoc (handoff permalinks/posts at ~2356–2410 and the final reply at ~2622) becomes `--channel "$REPLY_CHANNEL"`. The guard `[ -n "$KM_SLACK_CHANNEL_ID" ]` before the final post stays (it proves the box has a Slack identity at all) and additionally checks `[ -n "$REPLY_CHANNEL" ]`.

In each of the three `dispatch_as_sandbox "..."` blocks add, on the line after `export KM_SLACK_THREAD_TS='$THREAD_TS'`:

```bash
            export KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'
```

(Two spaces less indentation in the claude block, matching its neighbours. This overrides the profile.d value for the turn only, which is what makes `km-notify-hook` streaming posts and `km-slack reply` land in the routed thread.)

Do NOT touch `_km_stream_drain` or the transcript-upload sites outside the poller heredoc (lines ~793–1090): those run from the notify-hook with the per-turn environment and already pick up the override.

- [ ] **Step 4: Run the compiler tests, then patch goldens**

Run: `cd pkg/compiler && go test .`
Expected: the new tests PASS; two golden tests FAIL with diffs confined to the poller heredoc.

For `userdata_additional_volume_only.golden.sh`: regenerate with the package's capture flag (find it with `grep -rn "os.Getenv(\"UPDATE\|os.Getenv(\"CAPTURE" pkg/compiler/*_test.go`), then `git diff --stat` and eyeball that only poller lines changed.

For `userdata_learn_v2_pre92_baseline.golden.sh`: NEVER use `CAPTURE_PRE92_BASELINE=1` (it writes the unstripped output and corrupts the golden). Apply the same hunks by hand with `sed`/an editor, then run the byte-identity test and read its diff until it is empty.

Run: `cd pkg/compiler && go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_slack_reply_channel_test.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/testdata/userdata_additional_volume_only.golden.sh
git commit -m "feat(compiler): Slack poller replies to the envelope's channel; per-turn KM_SLACK_CHANNEL_ID" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 11: Terraform — bridge env/IAM, live inputs, operator policy SendMessage

**Files:**
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` (append)
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` (env block near line 427; new IAM policy resources after the existing sandboxes policies)
- Modify: `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` (inputs after `slack_allow`)
- Modify: `infra/modules/km-operator-policy/v1.0.0/main.tf` (`SlackInboundQueueLifecycle` statement near line 639)
- Test: `pkg/terragrunt/slack_default_route_module_test.go` (create)

- [ ] **Step 1: Write the failing guard tests**

```go
package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every KM_SLACK_DEFAULT_ROUTE_* var the bridge reads must be set by the module
// from a variable the live unit feeds from get_env — all three, by name.
func TestSlackBridgeModule_DefaultRouteEnvIsPlumbedEndToEnd(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")
	for _, suffix := range []string{"ALIAS", "PROFILE", "ALLOW"} {
		env := "KM_SLACK_DEFAULT_ROUTE_" + suffix
		v := "slack_default_route_" + strings.ToLower(suffix)
		if !regexp.MustCompile(env + `\s*=\s*var\.` + v).MatchString(main) {
			t.Errorf("main.tf must set %s = var.%s", env, v)
		}
		if !strings.Contains(vars, `variable "`+v+`"`) {
			t.Errorf("variables.tf missing %s", v)
		}
		if !strings.Contains(live, v+` = get_env("`+env+`", "")`) {
			t.Errorf("live terragrunt.hcl must feed %s from get_env(%q, \"\")", v, env)
		}
	}
}

func TestSlackBridgeModule_DefaultRouteIAM(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	if !strings.Contains(main, "/index/alias-index") {
		t.Error("bridge needs dynamodb:Query on the sandboxes alias-index GSI")
	}
	if !strings.Contains(main, `"events:PutEvents"`) {
		t.Error("bridge needs events:PutEvents for cold-create")
	}
}

func TestOperatorPolicy_CreateHandlerCanSendToSlackInbound(t *testing.T) {
	main := repoFile(t, "infra/modules/km-operator-policy/v1.0.0/main.tf")
	i := strings.Index(main, `Sid    = "SlackInboundQueueLifecycle"`)
	if i == -1 {
		t.Fatal("SlackInboundQueueLifecycle statement not found")
	}
	stmt := main[i:]
	stmt = stmt[:strings.Index(stmt, "Resource")]
	if !strings.Contains(stmt, `"sqs:SendMessage"`) {
		t.Error("the create-handler drains Slack cold-create envelopes into slack-inbound queues and needs sqs:SendMessage there (GitHub and H1 statements already have it)")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd pkg/terragrunt && go test -run 'SlackBridgeModule|OperatorPolicy_CreateHandlerCanSend' .`
Expected: FAIL on all three.

- [ ] **Step 3: Edit the HCL**

`variables.tf`, append:

```hcl
# 2026-10-16: Slack default route. All three are exported together by km init
# (ExportTerragruntEnvVars) or not at all; the bridge treats a partial set as OFF.
variable "slack_default_route_alias" {
  description = "Alias of the sandbox that receives @-mentions from unmapped channels/DMs. Empty = route OFF."
  type        = string
  default     = ""
}

variable "slack_default_route_profile" {
  description = "Profile path used to cold-create the default-route sandbox when the alias is absent."
  type        = string
  default     = ""
}

variable "slack_default_route_allow" {
  description = "Comma-joined, lowercased emails allowed to drive the default route. Empty = route OFF (deny-by-default)."
  type        = string
  default     = ""
}
```

`main.tf` env block, after `KM_SLACK_ALLOW`:

```hcl
      # 2026-10-16 — default route (unmapped channel/DM → one alias; email-gated)
      KM_SLACK_DEFAULT_ROUTE_ALIAS   = var.slack_default_route_alias
      KM_SLACK_DEFAULT_ROUTE_PROFILE = var.slack_default_route_profile
      KM_SLACK_DEFAULT_ROUTE_ALLOW   = var.slack_default_route_allow
```

`main.tf`, new resources (copy the GitHub module's shapes; `aws_iam_role.slack_bridge` is the role resource name in this module — confirm with `grep -n 'resource "aws_iam_role"' main.tf` and use the real name):

```hcl
# 2026-10-16: default route — alias-index GSI query (alias → sandbox) and
# EventBridge PutEvents (cold-create). Both unconditional: the statements grant
# nothing dangerous and keeping them keyed to a variable would make a
# half-configured route fail with AccessDenied instead of staying OFF.
resource "aws_iam_role_policy" "dynamodb_sandboxes_alias_gsi" {
  name = "${local.function_name}-dynamodb-alias-gsi"
  role = aws_iam_role.slack_bridge.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "DDBSandboxesAliasGSI"
      Effect   = "Allow"
      Action   = ["dynamodb:Query"]
      Resource = ["${var.sandboxes_table_arn}/index/alias-index"]
    }]
  })
}

resource "aws_iam_role_policy" "eventbridge_sandbox_create" {
  name = "${local.function_name}-eventbridge"
  role = aws_iam_role.slack_bridge.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "EventBridgePutSandboxCreate"
      Effect   = "Allow"
      Action   = ["events:PutEvents"]
      Resource = "arn:aws:events:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:event-bus/default"
    }]
  })
}
```

`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`, after `slack_allow`:

```hcl
  # 2026-10-16 — Slack default route. km init exports all three from
  # km-config.yaml slack.default_route or none. Requires km init --dry-run=false
  # (env-block change; --sidecars does not update it).
  slack_default_route_alias   = get_env("KM_SLACK_DEFAULT_ROUTE_ALIAS", "")
  slack_default_route_profile = get_env("KM_SLACK_DEFAULT_ROUTE_PROFILE", "")
  slack_default_route_allow   = get_env("KM_SLACK_DEFAULT_ROUTE_ALLOW", "")
```

`km-operator-policy/v1.0.0/main.tf`, in `SlackInboundQueueLifecycle`'s `Action` list add `"sqs:SendMessage",` after `"sqs:TagQueue",` with the comment `# 2026-10-16: the create-handler drains Slack cold-create envelopes here.`

- [ ] **Step 4: Validate**

Run: `cd pkg/terragrunt && go test .` then `terraform -chdir=infra/modules/lambda-slack-bridge/v1.0.0 fmt -check && terraform -chdir=infra/modules/lambda-slack-bridge/v1.0.0 validate` (run `terraform init -backend=false` there first if validate asks for it; delete any `.terraform.lock.hcl` it leaves behind — see memory `project_module_source_lock_drift`).
Expected: tests PASS; fmt clean; validate OK.

- [ ] **Step 5: Commit**

```bash
git add infra/modules/lambda-slack-bridge/v1.0.0/variables.tf infra/modules/lambda-slack-bridge/v1.0.0/main.tf infra/live/use1/lambda-slack-bridge/terragrunt.hcl infra/modules/km-operator-policy/v1.0.0/main.tf pkg/terragrunt/slack_default_route_module_test.go
git commit -m "feat(infra): Slack bridge default-route env + IAM; create-handler SendMessage on slack-inbound" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 12: Slack App manifest scopes and events

**Files:**
- Modify: `internal/app/cmd/slack_manifest_template.json`
- Modify: `internal/app/cmd/testdata/slack_manifest_golden.json`
- Modify: `internal/app/cmd/slack.go` (`VerifyEventsAPIScopes` near line 963)
- Test: `internal/app/cmd/slack_manifest_test.go` (append)

- [ ] **Step 1: Write the failing test**

```go
// TestSlackManifest_DMScopesAndEvents: the default route accepts DMs and group
// DMs, which need im:history/mpim:history and the message.im/message.mpim events.
func TestSlackManifest_DMScopesAndEvents(t *testing.T) {
	var buf bytes.Buffer
	if err := cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{AppName: "x", EventsURL: "https://e/events"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`"im:history"`, `"mpim:history"`, `"message.im"`, `"message.mpim"`} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %s", want)
		}
	}
	if strings.Contains(out, `"im:write"`) {
		t.Error("the bot never opens a DM; im:write must not be requested")
	}
}

func TestVerifyEventsAPIScopes_RequiresIMHistory(t *testing.T) {
	ok, missing := cmd.VerifyEventsAPIScopes([]string{"channels:history", "groups:history", "reactions:write", "files:read"})
	if ok || !contains(missing, "im:history") {
		t.Fatalf("im:history must be required; ok=%v missing=%v", ok, missing)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd internal/app/cmd && go test -run 'SlackManifest_DMScopes|VerifyEventsAPIScopes_RequiresIMHistory' .`
Expected: FAIL.

- [ ] **Step 3: Edit**

In the template's `scopes.bot` array add `"im:history",` and `"mpim:history",` after `"groups:history",`. In `bot_events` add `"message.im",` and `"message.mpim"` after `"message.groups"` (mind the trailing comma on the previous last entry). Apply the identical edit to `testdata/slack_manifest_golden.json` (it is the rendered form of the template with the test's app name and URL; keep byte-for-byte formatting).

`VerifyEventsAPIScopes`: `required := []string{"channels:history", "groups:history", "im:history", "mpim:history", "reactions:write", "files:read"}`.

- [ ] **Step 4: Run the tests**

Run: `cd internal/app/cmd && go test -run 'SlackManifest|VerifyEventsAPIScopes' .`
Expected: PASS including `TestSlackManifest_Golden`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/slack_manifest_template.json internal/app/cmd/testdata/slack_manifest_golden.json internal/app/cmd/slack.go internal/app/cmd/slack_manifest_test.go
git commit -m "feat(slack): manifest requests im/mpim history + message.im/mpim for the default route" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 13: `km doctor` group "Slack default route"

**Files:**
- Modify: `internal/app/cmd/doctor_slack.go` (append check function)
- Modify: `internal/app/cmd/doctor.go` (register after the peer-bridges check near line 4654; add `SlackEmailLookup` to `DoctorDeps` near line 532)
- Modify: the production `DoctorDeps` construction (find with `grep -n "SlackSSMStore:" internal/app/cmd/*.go`) to wire `SlackEmailLookup` from the bot token when available
- Test: `internal/app/cmd/doctor_slack_default_route_test.go` (create)

**Interfaces:**
- Produces: `type SlackEmailLookup interface{ LookupUserByEmail(ctx, email string) (string, bool, error) }` (satisfied by `*pkgslack.Client`), `checkSlackDefaultRoute(ctx, route *config.SlackDefaultRoute, searchPaths []string, lookup SlackEmailLookup, scopesCSV string) []CheckResult`.

- [ ] **Step 1: Write the failing tests**

```go
package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

type fakeEmailLookup struct{ known map[string]string }

func (f *fakeEmailLookup) LookupUserByEmail(_ context.Context, email string) (string, bool, error) {
	id, ok := f.known[email]
	return id, ok, nil
}

func statusOf(rs []CheckResult, name string) (CheckStatus, string) {
	for _, r := range rs {
		if r.Name == name {
			return r.Status, r.Message
		}
	}
	return "", "missing"
}

func TestCheckSlackDefaultRoute_SkippedWhenAbsent(t *testing.T) {
	rs := checkSlackDefaultRoute(context.Background(), nil, nil, nil, "")
	if len(rs) != 1 || rs[0].Status != CheckSkipped {
		t.Fatalf("want one SKIPPED result, got %+v", rs)
	}
}

func TestCheckSlackDefaultRoute_AllGreen(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"kurt@example.com"}}
	rs := checkSlackDefaultRoute(context.Background(), route, nil, &fakeEmailLookup{known: map[string]string{"kurt@example.com": "U1"}}, "channels:history,im:history,mpim:history")
	for _, name := range []string{"Slack default route profile", "Slack default route allowlist", "Slack default route scopes"} {
		if st, msg := statusOf(rs, name); st != CheckOK {
			t.Errorf("%s: %s (%s)", name, st, msg)
		}
	}
}

func TestCheckSlackDefaultRoute_Warnings(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "quiet.yaml", routeProfileNoInbound)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"ghost@example.com"}}
	rs := checkSlackDefaultRoute(context.Background(), route, nil, &fakeEmailLookup{}, "channels:history")
	if st, _ := statusOf(rs, "Slack default route profile"); st != CheckWarn {
		t.Errorf("profile without inbound must WARN")
	}
	if st, msg := statusOf(rs, "Slack default route allowlist"); st != CheckWarn || !strings.Contains(msg, "ghost@example.com") {
		t.Errorf("unresolvable email must WARN and be named; got %s %q", st, msg)
	}
	if st, msg := statusOf(rs, "Slack default route scopes"); st != CheckWarn || !strings.Contains(msg, "im:history") {
		t.Errorf("missing im:history must WARN; got %s %q", st, msg)
	}
	_ = filepath.Base
}
```

(`writeProfile`, `routeProfileOK`, `routeProfileNoInbound` come from Task 2's test file, same package.)

- [ ] **Step 2: Run to verify failure**

Run: `cd internal/app/cmd && go test -run 'CheckSlackDefaultRoute' .`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

`doctor_slack.go`, append:

```go
// SlackEmailLookup is the one users.lookupByEmail call the default-route doctor
// group makes; *pkgslack.Client satisfies it.
type SlackEmailLookup interface {
	LookupUserByEmail(ctx context.Context, email string) (string, bool, error)
}

// checkSlackDefaultRoute validates the slack.default_route block. Read-only.
// One SKIPPED result when the block is absent; otherwise three results.
func checkSlackDefaultRoute(ctx context.Context, route *config.SlackDefaultRoute, searchPaths []string, lookup SlackEmailLookup, scopesCSV string) []CheckResult {
	if route == nil {
		return []CheckResult{{Name: "Slack default route", Status: CheckSkipped, Message: "slack.default_route not set — route off"}}
	}
	var out []CheckResult

	// 1. Profile: exists, resolves, has Slack inbound.
	if err := ValidateSlackDefaultRouteProfile(route, searchPaths); err != nil {
		out = append(out, CheckResult{Name: "Slack default route profile", Status: CheckWarn, Message: err.Error(),
			Remediation: "Fix km-config.yaml slack.default_route and the profile, then `km init --dry-run=false`."})
	} else {
		out = append(out, CheckResult{Name: "Slack default route profile", Status: CheckOK,
			Message: fmt.Sprintf("%s → alias %q, Slack inbound enabled", route.Profile, route.Alias)})
	}

	// 2. Allowlist: non-empty and every email resolves in the workspace.
	emails := route.NormalizedEmails()
	switch {
	case len(emails) == 0:
		out = append(out, CheckResult{Name: "Slack default route allowlist", Status: CheckWarn,
			Message: "allow_emails is empty — the route is OFF (deny-by-default)",
			Remediation: "List at least one workspace email under slack.default_route.allow_emails."})
	case lookup == nil:
		out = append(out, CheckResult{Name: "Slack default route allowlist", Status: CheckSkipped,
			Message: fmt.Sprintf("%d email(s) configured; bot token unavailable so resolution was not checked", len(emails))})
	default:
		var unresolved []string
		for _, e := range emails {
			if _, ok, err := lookup.LookupUserByEmail(ctx, e); err != nil || !ok {
				unresolved = append(unresolved, e)
			}
		}
		if len(unresolved) > 0 {
			out = append(out, CheckResult{Name: "Slack default route allowlist", Status: CheckWarn,
				Message: "not found in the workspace: " + strings.Join(unresolved, ", "),
				Remediation: "Use the email on the person's Slack profile. Removals take effect after `km init --dry-run=false` plus up to 15 minutes of bridge cache."})
		} else {
			out = append(out, CheckResult{Name: "Slack default route allowlist", Status: CheckOK,
				Message: fmt.Sprintf("%d email(s) resolve in the workspace", len(emails))})
		}
	}

	// 3. Scopes: DMs need im:history / mpim:history.
	have := map[string]bool{}
	for _, s := range strings.Split(scopesCSV, ",") {
		have[strings.TrimSpace(s)] = true
	}
	var missing []string
	for _, s := range []string{"im:history", "mpim:history"} {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	if scopesCSV == "" {
		out = append(out, CheckResult{Name: "Slack default route scopes", Status: CheckSkipped, Message: "bot scopes cache unavailable"})
	} else if len(missing) > 0 {
		out = append(out, CheckResult{Name: "Slack default route scopes", Status: CheckWarn,
			Message: "missing " + strings.Join(missing, ", ") + " — DMs to the bot are never delivered",
			Remediation: "`km slack manifest` → paste into the Slack App → reinstall → `km slack rotate-token --bot-token <new>`."})
	} else {
		out = append(out, CheckResult{Name: "Slack default route scopes", Status: CheckOK, Message: "im:history and mpim:history present"})
	}
	return out
}
```

`doctor.go`: add `SlackEmailLookup SlackEmailLookup` to `DoctorDeps` (comment: nil when the bot token is unavailable). After the peer-bridges registration:

```go
	// 2026-10-16 — Slack default route (skipped silently when unset).
	defaultRoute := cfg.Slack.DefaultRoute
	defaultRouteSearchPaths := cfg.ProfileSearchPaths
	emailLookup := deps.SlackEmailLookup
	checks = append(checks, func(ctx context.Context) CheckResult {
		var scopes string
		if slackSSMStore != nil {
			scopes, _ = slackSSMStore.Get(ctx, cfg.GetSsmPrefix()+"slack/bot-scopes-cache", false)
		}
		rs := checkSlackDefaultRoute(ctx, defaultRoute, defaultRouteSearchPaths, emailLookup, scopes)
		// The checks slice is one-result-per-func: fold to the worst status and
		// join the messages so nothing is lost in the summary.
		return foldCheckResults("Slack default route", rs)
	})
```

If the doctor framework supports appending several funcs, register one per result instead; otherwise add:

```go
func foldCheckResults(name string, rs []CheckResult) CheckResult {
	worst := CheckOK
	var msgs, rems []string
	for _, r := range rs {
		switch {
		case r.Status == CheckError || r.Status == CheckWarn:
			worst = CheckWarn
		case r.Status == CheckSkipped && worst == CheckOK && len(rs) == 1:
			worst = CheckSkipped
		}
		msgs = append(msgs, r.Name+": "+r.Message)
		if r.Remediation != "" {
			rems = append(rems, r.Remediation)
		}
	}
	return CheckResult{Name: name, Status: worst, Message: strings.Join(msgs, "; "), Remediation: strings.Join(rems, " "), Details: msgs}
}
```

Wire the production dep where `SlackSSMStore:` is set: when a bot token is readable from SSM, `SlackEmailLookup: pkgslack.NewClient(token, http.DefaultClient)`; otherwise leave nil. Follow the exact pattern the file uses for `SlackDeadChannelChecker`.

- [ ] **Step 4: Run the tests**

Run: `cd internal/app/cmd && go test -run 'CheckSlackDefaultRoute|TestDoctor' .`
Expected: PASS (existing doctor tests unaffected: a nil route yields one SKIPPED result).

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/doctor_slack.go internal/app/cmd/doctor.go internal/app/cmd/doctor_slack_default_route_test.go
git add $(git diff --name-only -- internal/app/cmd | grep -v _test | grep -v doctor_slack.go | grep -v 'doctor.go$')
git commit -m "feat(doctor): Slack default route group (profile, allowlist resolution, DM scopes)" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 14: Docs, CLAUDE.md, full-suite run

**Files:**
- Modify: `docs/slack-notifications.md` (new section after Phase 119; add a TOC entry)
- Modify: `CLAUDE.md` (new phase block at the top of the project notes; one row in "Where to look")
- Modify: `docs/superpowers/specs/2026-10-16-slack-default-route-design.md`: §3 now says ONE merge-list entry (`slack.default_route`, loaded via `UnmarshalKey` like `github`) rather than three; §7 gains the `slack-profiles/{slug}/` pre-stage and the create-handler `sqs:SendMessage` grant; §11 marks the first two open items resolved (grant added in Task 11; no in-turn caller of `km-slack update` exists — verified during planning with a repo-wide grep).

- [ ] **Step 1: Write the operator section in `docs/slack-notifications.md`**

Headed `## Default route — @-mention the bot from any channel or DM (2026-10-16)`, covering in this order, each as a short subsection:

1. **What it does.** Invite the bot to a channel (or DM it), @-mention it, get a threaded reply there; follow-ups in the thread need no mention. One configured alias receives every unmapped conversation.
2. **Config.** The `slack.default_route` YAML block verbatim from the spec §3, the three env vars, deny-by-default, why `slack.allow` is not reused, the profile requirement (`notification.slack.enabled` + `inbound.enabled`), and that the profile is pre-staged to `slack-profiles/{slug}/` by `km init`.
3. **Who can use it.** Email resolved at event time via `users.info`; deny on deleted/bot/no-email/error; 15-minute allow cache; removals = `km init` + cache TTL; the gate applies to every turn including thread follow-ups; non-listed senders see nothing.
4. **How a message is routed.** The six-step ladder from spec §5 in prose, including: DMs need no mention, `mpim` does; relay/peer claims win; the Phase 96 orphan reply is retired on an install with a route; cold-create reacts 👀 and answers when the box boots.
5. **Replies land in the originating thread.** The poller's `REPLY_CHANNEL`, the per-turn `KM_SLACK_CHANNEL_ID`, the bridge's thread-scoped authorization, and the one limitation: `claude:`/`codex:` agent switching is skipped in a routed thread (log line names it).
6. **Slack App changes.** `im:history`, `mpim:history`, `message.im`, `message.mpim`; manifest → reinstall → `km slack rotate-token`.
7. **Deploy.** The seven steps from spec §8 and the three half-deploy outcomes.
8. **Troubleshooting.** Table: symptom → cause → fix, covering: mention ignored (not in allowlist / bot not in channel / no mention in a channel / route not exported — check `km doctor`), DM ignored (scopes), reply in `#sb-…` instead of the channel (old poller — recreate), `channel_mismatch` in bridge logs (post without thread in a foreign channel), cold-create box never answers (profile lacks inbound / `slack_envelope` ignored by an old create-handler / missing `sqs:SendMessage`), first reply slow (cold-create).

Add the TOC entry next to the Phase 119 one.

- [ ] **Step 2: Write the CLAUDE.md phase block**

Insert above the `km-volumes` block, in the house style (bold lead sentence per bullet), covering: the ladder and why continuity runs before the relay; the two-sided reply fix and the `channel_mismatch` authz rule; identity at event time and the never-cache-a-deny rule; the three things found during planning (create-handler `SendMessage`, `slack-profiles/` pre-stage, Phase 96 retirement when a route exists); deploy = `make build` + `make build-lambdas` + `km init --dry-run=false` + manifest/reinstall/rotate-token + `km destroy && km create` of the default sandbox, with the three half-deploy outcomes; frozen golden hand-patched. Add a "Where to look" row pointing at the new doc section.

- [ ] **Step 3: Update spec §11 and run the full suite**

Run: `go build ./... && go test ./... 2>&1 | tail -40; echo EXIT=${PIPESTATUS[0]}`
Expected: `EXIT=0` apart from the known pre-existing `internal/app/cmd` failures listed in memory `project_cmd_suite_pre_existing_failures`; confirm by name that every FAIL line is one of those. Any other FAIL is a regression from this plan and must be fixed before commit.

Also: `make build` (bumps VERSION; confirm `./bin/km --version` or the repo's binary path reports the new GitCommit) and `go vet ./pkg/slack/... ./cmd/km-slack-bridge/... ./cmd/create-handler/...`.

- [ ] **Step 4: Commit**

```bash
git add docs/slack-notifications.md CLAUDE.md docs/superpowers/specs/2026-10-16-slack-default-route-design.md
git commit -m "docs: Slack default route runbook, CLAUDE.md phase block, spec open items resolved" -m "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

## Execution order

1 → 2 → 3 → 4 → 9 → 5 → 6 → 7 → 8 → 10 → 11 → 12 → 13 → 14. (Task 9 before 5/6 so the `SlackEnvelope` field exists when the bridge adapters compile.)

## Live UAT (after deploy, not part of the code tasks)

Spec §10, eleven steps. Record results in `docs/slack-notifications.md` § Default route → "Live UAT" with the sandbox id, channel ids, and CloudWatch log lines (`events: route: …`, `bridge: authz_routed_thread`).
