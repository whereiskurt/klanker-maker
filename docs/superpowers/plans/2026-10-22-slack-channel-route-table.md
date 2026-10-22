# Slack Channel Route Table Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Map named Slack channels to klanker aliases in `km-config.yaml` so the bridge dispatches a mapped channel's turns to that alias, falling back to `slack.default_route`.

**Architecture:** `slack.routes` is a list beside `slack.default_route`. `km init` resolves channel names to ids from the bot's own conversation list, pre-stages each route's profile, writes one canonical JSON document to SSM `/{prefix}/slack/routes` and exports its SHA-256 into the bridge env. The bridge builds a `map[channelID]*DefaultRoute` at cold start and the existing route ladder runs against `table[channel] ?? DefaultRoute`. Ownership (`slack_channel_id` GSI) is untouched.

**Tech Stack:** Go 1.25, viper config, AWS SDK v2 (SSM, S3), Terraform module `infra/modules/lambda-slack-bridge/v1.0.0` (edited in place, additive), terragrunt live unit.

**Spec:** `docs/superpowers/specs/2026-10-22-slack-channel-route-table-design.md`

## Global Constraints

- Every commit message ends with the two trailer lines the session requires:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`.
- Stage commits by pathspec only (`git add <files>`), never `git add -A`.
- New `km-config.yaml` key `slack.routes` MUST be added to the v2→v merge-list in `internal/app/config/config.go` with a regression test (spec §3).
- Email entries keep the existing grammar: exact address or exactly `*@<domain>` (`bridge.ValidateEmailEntry`).
- Effective allow list per route, first non-empty wins: route `allow_emails` → `slack.default_route.allow_emails` → the route profile's `notification.slack.inbound.allowEmails`; all empty is a `km init` error (spec §3).
- Channel in config is `#name` (`^#[a-z0-9][a-z0-9._-]{0,79}$`) or an id (`^[CG][A-Z0-9]+$`).
- SSM parameter path: `/{prefix}/slack/routes`, type String. Env var: `KM_SLACK_ROUTES_SHA256` (hex SHA-256 of the parameter bytes). Env var `KM_SLACK_ROUTES_PARAM` carries the path to the Lambda.
- Canonical JSON: `{"v":1,"routes":[...]}`, routes sorted by channel id, each `allow` list sorted, no HTML escaping, no trailing newline.
- Precedence in the bridge: owned channel → routed-thread continuity → peer claim → `Routes[channel]` → `DefaultRoute` → Phase 96 (spec §5.3).
- Mention required by default; a route's `mention_only: false` lifts it for top-level posts only; DMs never need one (spec §5.4).
- Name resolution never calls `conversations.list`; it uses `users.conversations` (the bot's own memberships), so a name the bot cannot hear is unresolvable by construction. (Refines spec §4 steps 2–4: no cache lookup; "not found among the bot's channels" is the error, and the message lists the bot's channels.)
- No routes configured ⇒ no Slack call; the parameter is overwritten with the empty document only if it already exists; env hash `""`. (Refines spec §4: overwrite-with-empty instead of delete, so no SSM delete capability is added.)
- Deploy surface: first time `make build` → `make build-lambdas` → `km init --dry-run=false`; afterwards `km init --slack`. No sandbox recreate.
- Byte-identity: with no `routes:` block, bridge behaviour and `km init` env exports are identical to today except the two new env vars (`KM_SLACK_ROUTES_PARAM`, `KM_SLACK_ROUTES_SHA256=""`).
- Run `go test ./internal/app/config/ ./internal/app/cmd/ ./pkg/slack/... ./pkg/terragrunt/ ./cmd/km-slack-bridge/` before every commit; the whole suite (`go test ./...`) before the final review.

---

## File map

| File | Responsibility |
|---|---|
| `internal/app/config/config.go` | `SlackRoute` type, `SlackConfig.Routes`, merge-list entry, load, validation |
| `internal/app/config/config_slack_routes_test.go` | config tests |
| `pkg/slack/bridge/channel_routes.go` | shared document: `RouteEntry`, `RoutesDocument`, `CanonicalRoutesJSON`, `RoutesSHA256`, `ParseRoutesDocument`, `BuildRouteTable`, `RoutesParamPath` |
| `pkg/slack/bridge/channel_routes_test.go` | document tests |
| `pkg/slack/bridge/default_route.go` | `DefaultRoute.MentionOnly` |
| `pkg/slack/client.go` | `ListBotChannels` (`users.conversations`) |
| `pkg/slack/client_test.go` | httptest test for it |
| `internal/app/cmd/slack_routes.go` | resolution, effective lists, pre-stage, SSM write, env export |
| `internal/app/cmd/slack_routes_test.go` | resolution tests with fakes |
| `internal/app/cmd/init.go` | hook into full init, scoped `--slack`, `--plan`; `routeProfileInboundAllow` extraction |
| `internal/app/cmd/slack.go` | hook into `km slack init` |
| `internal/app/cmd/create_slack.go` | nothing new; `productionSSMParamStore` reused |
| `infra/modules/lambda-slack-bridge/v1.0.0/{main,variables}.tf` | env + IAM |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `get_env` feed |
| `pkg/terragrunt/slack_channel_routes_module_test.go` | module pairing test |
| `pkg/slack/bridge/events_handler.go` | `Routes` field, selection, route threaded through the ladder |
| `pkg/slack/bridge/events_handler_channel_routes_test.go` | ladder tests |
| `cmd/km-slack-bridge/main.go` | `wireChannelRoutes`, shared route deps wiring |
| `cmd/km-slack-bridge/channel_routes_wiring_test.go` | wiring test |
| `internal/app/cmd/doctor_slack_routes.go` + `doctor.go` | doctor group |
| `internal/app/cmd/doctor_slack_routes_test.go` | doctor tests |
| `docs/slack-notifications.md`, `docs/slack-default-route.md`, `OPERATOR-GUIDE.md`, `CLAUDE.md` | docs |

---

### Task 1: Config — `slack.routes`

**Files:**
- Modify: `internal/app/config/config.go` (after `SlackDefaultRoute` ~line 236; merge-list ~line 1253; load ~line 1460)
- Test: `internal/app/config/config_slack_routes_test.go`

**Interfaces:**
- Produces: `config.SlackRoute{Channel, Alias, Profile string; AllowEmails []string; MentionOnly *bool}`, methods `Validate() error`, `NormalizedEmails() []string`, `ChannelName() (string, bool)`, `ChannelID() (string, bool)`, `MentionRequired() bool`; `SlackConfig.Routes []SlackRoute`; `(*SlackConfig).ValidateRoutes() error`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/app/config/config_slack_routes_test.go
package config_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// Merge-list regression guard: "slack.routes" absent from the v2→v merge-list
// leaves Routes empty and this test fails loud.
func TestLoadSlackRoutes_Set(t *testing.T) {
	cfg := loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    routes:
      - channel: "#security-alerts"
        alias: ir-bot
        profile: profiles/ir-bot.yaml
        allow_emails: [" Kurt@Example.com ", "*@example.com"]
        mention_only: false
      - channel: C0AB12CD3
        alias: platform-bot
        profile: profiles/platform.yaml
`)
	rs := cfg.Slack.Routes
	if len(rs) != 2 {
		t.Fatalf("Slack.Routes = %d entries — add \"slack.routes\" to the v2→v merge-list (project_config_key_merge_list footgun)", len(rs))
	}
	if rs[0].Alias != "ir-bot" || rs[0].Profile != "profiles/ir-bot.yaml" {
		t.Errorf("route 0: %+v", rs[0])
	}
	if name, ok := rs[0].ChannelName(); !ok || name != "security-alerts" {
		t.Errorf("route 0 ChannelName = %q,%v", name, ok)
	}
	if got := rs[0].NormalizedEmails(); len(got) != 2 || got[0] != "kurt@example.com" || got[1] != "*@example.com" {
		t.Errorf("NormalizedEmails: %v", got)
	}
	if rs[0].MentionRequired() {
		t.Error("mention_only: false must read as not required")
	}
	if id, ok := rs[1].ChannelID(); !ok || id != "C0AB12CD3" {
		t.Errorf("route 1 ChannelID = %q,%v", id, ok)
	}
	if !rs[1].MentionRequired() {
		t.Error("absent mention_only must read as required")
	}
}

func TestLoadSlackRoutes_AbsentAndWithoutDefaultRoute(t *testing.T) {
	cfg := loadFrom(t, "domain: example.com\nregion: us-east-1\n")
	if len(cfg.Slack.Routes) != 0 {
		t.Fatalf("expected no routes, got %+v", cfg.Slack.Routes)
	}
	cfg = loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    routes:
      - channel: "#a"
        alias: a
        profile: profiles/a.yaml
`)
	if cfg.Slack.DefaultRoute != nil || len(cfg.Slack.Routes) != 1 {
		t.Fatalf("routes without default_route must load: %+v / %+v", cfg.Slack.DefaultRoute, cfg.Slack.Routes)
	}
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		t.Fatalf("ValidateRoutes: %v", err)
	}
}

func TestSlackRoute_Validate(t *testing.T) {
	cases := []struct {
		name  string
		r     config.SlackRoute
		wantE string
	}{
		{"ok name", config.SlackRoute{Channel: "#sec-alerts_1.x", Alias: "a", Profile: "p"}, ""},
		{"ok id", config.SlackRoute{Channel: "G0ABC", Alias: "a", Profile: "p"}, ""},
		{"bare name", config.SlackRoute{Channel: "sec", Alias: "a", Profile: "p"}, "channel"},
		{"upper name", config.SlackRoute{Channel: "#Sec", Alias: "a", Profile: "p"}, "channel"},
		{"space", config.SlackRoute{Channel: "#a b", Alias: "a", Profile: "p"}, "channel"},
		{"no alias", config.SlackRoute{Channel: "#a", Profile: "p"}, "alias"},
		{"no profile", config.SlackRoute{Channel: "#a", Alias: "a"}, "profile"},
		{"bad email", config.SlackRoute{Channel: "#a", Alias: "a", Profile: "p", AllowEmails: []string{"*example.com"}}, "allow_emails"},
	}
	for _, c := range cases {
		err := c.r.Validate()
		if c.wantE == "" && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.wantE != "" && (err == nil || !strings.Contains(err.Error(), c.wantE)) {
			t.Errorf("%s: want error naming %q, got %v", c.name, c.wantE, err)
		}
	}
}

func TestSlackConfig_ValidateRoutes_LiteralDuplicate(t *testing.T) {
	s := config.SlackConfig{Routes: []config.SlackRoute{
		{Channel: "#a", Alias: "x", Profile: "p"},
		{Channel: " #A ", Alias: "y", Profile: "p"},
	}}
	err := s.ValidateRoutes()
	if err == nil || !strings.Contains(err.Error(), "#a") || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("want duplicate error naming #a, got %v", err)
	}
	s.Routes[1].Channel = "#b"
	if err := s.ValidateRoutes(); err != nil {
		t.Fatalf("distinct channels, same profile must validate: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /Users/khundeck/working/klankrmkr && go test ./internal/app/config/ -run 'SlackRoute|SlackRoutes' 2>&1 | tail -5`
Expected: compile errors (`SlackRoute` undefined).

- [ ] **Step 3: Add the type, loader, merge-list entry and validation**

In `config.go`, immediately after the `SlackDefaultRoute` field in `SlackConfig` (line 187):

```go
	// Routes (2026-10-22): per-channel routes beside DefaultRoute. A message in
	// a listed channel that no sandbox owns is dispatched to that route's alias
	// instead of the default route; unlisted channels fall through to
	// DefaultRoute, then Phase 96. Names resolve to ids at km init from the
	// bot's own conversation list (users.conversations). The table reaches the
	// bridge as SSM /{prefix}/slack/routes + KM_SLACK_ROUTES_SHA256 (km init
	// --slack after every change). CRITICAL: "slack.routes" must be in the
	// v2→v merge-list.
	Routes []SlackRoute `mapstructure:"routes" yaml:"routes,omitempty"`
```

After the `SlackDefaultRoute.Validate` method:

```go
// SlackRoute is one entry of slack.routes. Channel is "#name" or a channel id.
type SlackRoute struct {
	Channel     string   `mapstructure:"channel"      yaml:"channel"`
	Alias       string   `mapstructure:"alias"        yaml:"alias"`
	Profile     string   `mapstructure:"profile"      yaml:"profile"`
	AllowEmails []string `mapstructure:"allow_emails" yaml:"allow_emails,omitempty"`
	MentionOnly *bool    `mapstructure:"mention_only" yaml:"mention_only,omitempty"`
}

// slackRouteChannelName: "#" + a Slack-legal name (lowercase letters, digits,
// '.', '_', '-'; 1–80 chars). slackRouteChannelID: a public (C) or legacy
// private (G) channel id.
var slackRouteChannelName = regexp.MustCompile(`^#[a-z0-9][a-z0-9._-]{0,79}$`)
var slackRouteChannelID = regexp.MustCompile(`^[CG][A-Z0-9]+$`)

// ChannelName returns the name without '#' when Channel is a name.
func (r *SlackRoute) ChannelName() (string, bool) {
	c := strings.TrimSpace(r.Channel)
	if slackRouteChannelName.MatchString(c) {
		return strings.TrimPrefix(c, "#"), true
	}
	return "", false
}

// ChannelID returns the id when Channel is an id.
func (r *SlackRoute) ChannelID() (string, bool) {
	c := strings.TrimSpace(r.Channel)
	if slackRouteChannelID.MatchString(c) {
		return c, true
	}
	return "", false
}

// MentionRequired: absent mention_only ⇒ true (same rule as the default route).
func (r *SlackRoute) MentionRequired() bool { return r.MentionOnly == nil || *r.MentionOnly }

// NormalizedEmails returns AllowEmails lowercased, trimmed, empties dropped.
func (r *SlackRoute) NormalizedEmails() []string {
	out := make([]string, 0, len(r.AllowEmails))
	for _, e := range r.AllowEmails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Validate checks one route's own shape. No disk, no Slack. An empty
// allow_emails is valid: the install list or the route profile's list may
// supply it (km init decides, the only place that sees all three).
func (r *SlackRoute) Validate() error {
	c := strings.TrimSpace(r.Channel)
	if !slackRouteChannelName.MatchString(c) && !slackRouteChannelID.MatchString(c) {
		return fmt.Errorf("slack.routes: channel %q must be \"#name\" (lowercase Slack channel name) or a channel id (C…)", r.Channel)
	}
	if strings.TrimSpace(r.Alias) == "" {
		return fmt.Errorf("slack.routes: channel %q: alias is required", r.Channel)
	}
	if strings.TrimSpace(r.Profile) == "" {
		return fmt.Errorf("slack.routes: channel %q: profile is required", r.Channel)
	}
	for _, e := range r.AllowEmails {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if err := validateRouteEmailEntry(e); err != nil {
			return fmt.Errorf("slack.routes: channel %q: allow_emails: %w", r.Channel, err)
		}
	}
	return nil
}

// ValidateRoutes validates every route and rejects two routes naming the same
// channel literally (case-insensitive, trimmed). A name and an id that resolve
// to one channel are caught at km init, after resolution.
func (s *SlackConfig) ValidateRoutes() error {
	seen := map[string]int{}
	for i := range s.Routes {
		if err := s.Routes[i].Validate(); err != nil {
			return fmt.Errorf("slack.routes[%d]: %w", i, err)
		}
		key := strings.ToLower(strings.TrimSpace(s.Routes[i].Channel))
		if j, dup := seen[key]; dup {
			return fmt.Errorf("slack.routes: channel %q listed twice (entries %d and %d)", key, j, i)
		}
		seen[key] = i
	}
	return nil
}
```

Merge-list, immediately after the `"slack.default_route",` entry (~line 1253):

```go
			// 2026-10-22: per-channel route table (list-of-objects, UnmarshalKey).
			// CRITICAL: without it the whole list is silently dropped.
			"slack.routes",
```

Load, immediately after the `slack.default_route` UnmarshalKey block (~line 1460):

```go
	// 2026-10-22: slack.routes is a list of objects → UnmarshalKey (same shape
	// as github.repos). Absent ⇒ empty ⇒ no channel routes anywhere downstream.
	if v.IsSet("slack.routes") {
		if err := v.UnmarshalKey("slack.routes", &cfg.Slack.Routes); err != nil {
			return nil, fmt.Errorf("unmarshal slack.routes: %w", err)
		}
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/config/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/config/config.go internal/app/config/config_slack_routes_test.go
git commit -m "feat(config): slack.routes per-channel route list

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 2: Shared route document in `pkg/slack/bridge`

**Files:**
- Create: `pkg/slack/bridge/channel_routes.go`
- Modify: `pkg/slack/bridge/default_route.go` (`DefaultRoute` struct line 28; `NewDefaultRoute` line 38)
- Test: `pkg/slack/bridge/channel_routes_test.go`

**Interfaces:**
- Produces:
  - `type RouteEntry struct { Channel, Name, Alias, Profile string; Allow []string; MentionOnly bool }`
  - `type RoutesDocument struct { V int; Routes []RouteEntry }`, `const RoutesDocumentVersion = 1`
  - `func CanonicalRoutesJSON(routes []RouteEntry) ([]byte, error)`
  - `func RoutesSHA256(b []byte) string`
  - `func ParseRoutesDocument(b []byte) (*RoutesDocument, error)`
  - `func BuildRouteTable(doc *RoutesDocument, warn func(msg string, kv ...any)) map[string]*DefaultRoute`
  - `func RoutesParamPath(prefix string) string`
  - `DefaultRoute.MentionOnly bool` (true from `NewDefaultRoute`).

- [ ] **Step 1: Write the failing tests**

```go
// pkg/slack/bridge/channel_routes_test.go
package bridge

import (
	"strings"
	"testing"
)

func TestCanonicalRoutesJSON_StableOrderAndHash(t *testing.T) {
	a := []RouteEntry{
		{Channel: "C2", Name: "platform", Alias: "platform-bot", Profile: "platform", Allow: []string{"z@example.com", "a@example.com"}, MentionOnly: true},
		{Channel: "C1", Name: "sec", Alias: "ir-bot", Profile: "ir-bot", Allow: []string{"*@example.com"}, MentionOnly: false},
	}
	b := []RouteEntry{a[1], a[0]}
	b[1].Allow = []string{"a@example.com", "z@example.com"}
	ja, err := CanonicalRoutesJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := CanonicalRoutesJSON(b)
	if string(ja) != string(jb) {
		t.Fatalf("order must not change the bytes:\n%s\n%s", ja, jb)
	}
	want := `{"v":1,"routes":[{"channel":"C1","name":"sec","alias":"ir-bot","profile":"ir-bot","allow":["*@example.com"],"mention_only":false},{"channel":"C2","name":"platform","alias":"platform-bot","profile":"platform","allow":["a@example.com","z@example.com"],"mention_only":true}]}`
	if string(ja) != want {
		t.Fatalf("canonical form:\n got %s\nwant %s", ja, want)
	}
	if RoutesSHA256(ja) != RoutesSHA256(jb) || len(RoutesSHA256(ja)) != 64 {
		t.Fatal("hash must be the hex sha256 of the bytes")
	}
	empty, _ := CanonicalRoutesJSON(nil)
	if string(empty) != `{"v":1,"routes":[]}` {
		t.Fatalf("empty document: %s", empty)
	}
}

func TestParseRoutesDocument(t *testing.T) {
	doc, err := ParseRoutesDocument([]byte(`{"v":1,"routes":[{"channel":"C1","alias":"ir-bot","profile":"ir-bot","allow":["*@example.com"],"mention_only":false}]}`))
	if err != nil || len(doc.Routes) != 1 || doc.Routes[0].Channel != "C1" {
		t.Fatalf("parse: %v %+v", err, doc)
	}
	for _, bad := range []string{
		`{"v":2,"routes":[]}`,
		`{"v":1,"routes":[{"channel":"","alias":"a","profile":"p","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[{"channel":"C1","alias":"","profile":"p","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[{"channel":"C1","alias":"a","profile":"","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[{"channel":"C1","alias":"a","profile":"p","allow":[]}]}`,
		`not json`,
	} {
		if _, err := ParseRoutesDocument([]byte(bad)); err == nil {
			t.Errorf("must reject %s", bad)
		}
	}
}

func TestBuildRouteTable(t *testing.T) {
	var warned []string
	warn := func(msg string, kv ...any) { warned = append(warned, msg) }
	doc := &RoutesDocument{V: 1, Routes: []RouteEntry{
		{Channel: "C1", Alias: "ir-bot", Profile: "ir-bot", Allow: []string{"*@example.com"}, MentionOnly: false},
		{Channel: "C2", Alias: "platform-bot", Profile: "platform", Allow: []string{"kurt@example.com"}, MentionOnly: true},
		{Channel: "C1", Alias: "dup", Profile: "dup", Allow: []string{"kurt@example.com"}},
		{Channel: "C3", Alias: "bad", Profile: "bad", Allow: []string{"not-an-email"}},
	}}
	table := BuildRouteTable(doc, warn)
	if len(table) != 2 {
		t.Fatalf("want 2 routes, got %d (warned %v)", len(table), warned)
	}
	if r := table["C1"]; r == nil || r.Alias != "ir-bot" || r.MentionOnly || !r.Allows("x@example.com") || !r.AllowsCreate("x@example.com") {
		t.Errorf("C1: %+v", r)
	}
	if r := table["C2"]; r == nil || !r.MentionOnly || r.Allows("x@example.com") || !r.Allows("kurt@example.com") {
		t.Errorf("C2: %+v", r)
	}
	if len(warned) != 2 {
		t.Errorf("duplicate and invalid entries must each warn once: %v", warned)
	}
	if RoutesParamPath("km") != "/km/slack/routes" {
		t.Error(RoutesParamPath("km"))
	}
	if !strings.HasPrefix(RoutesParamPath("kph"), "/kph/") {
		t.Error("prefix must be honoured")
	}
	if !NewDefaultRoute("a", "p", []string{"k@example.com"}, nil).MentionOnly {
		t.Error("NewDefaultRoute must default MentionOnly to true")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./pkg/slack/bridge/ -run 'CanonicalRoutes|ParseRoutesDocument|BuildRouteTable' 2>&1 | tail -3`
Expected: compile errors.

- [ ] **Step 3: Implement**

In `default_route.go`, add the field to `DefaultRoute` and set it in `NewDefaultRoute`:

```go
type DefaultRoute struct {
	Alias       string
	Profile     string
	list        *EmailAllowlist
	profileList *EmailAllowlist
	// MentionOnly: a top-level post in a channel needs an @-mention of the bot
	// to start a turn. Always true for the default route; a channel route may
	// set mention_only: false. DMs never need one (requiresMention).
	MentionOnly bool
}
```
and in `NewDefaultRoute`'s return: `return &DefaultRoute{Alias: alias, Profile: profile, list: install, profileList: prof, MentionOnly: true}`.

Create `channel_routes.go`:

```go
package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// RoutesDocumentVersion is the "v" of the channel route table km init writes to
// SSM /{prefix}/slack/routes and the bridge reads at cold start.
const RoutesDocumentVersion = 1

// RouteEntry is one resolved channel route. Channel is the Slack channel id;
// Name is the channel's name at resolution time (logs and km doctor only);
// Profile is the pre-stage slug (slack-profiles/{Profile}/.km-profile.yaml);
// Allow is the EFFECTIVE allow list km init computed (route list, else install
// list, else the route profile's list), already normalised.
type RouteEntry struct {
	Channel     string   `json:"channel"`
	Name        string   `json:"name,omitempty"`
	Alias       string   `json:"alias"`
	Profile     string   `json:"profile"`
	Allow       []string `json:"allow"`
	MentionOnly bool     `json:"mention_only"`
}

// RoutesDocument is the on-the-wire table.
type RoutesDocument struct {
	V      int          `json:"v"`
	Routes []RouteEntry `json:"routes"`
}

// RoutesParamPath is the SSM parameter the table lives at.
func RoutesParamPath(prefix string) string { return "/" + prefix + "/slack/routes" }

// CanonicalRoutesJSON encodes the table so equal tables are equal bytes:
// routes sorted by channel id, each allow list sorted, no HTML escaping, no
// trailing newline. km doctor recomputes it to detect drift, so the form must
// never depend on map order.
func CanonicalRoutesJSON(routes []RouteEntry) ([]byte, error) {
	out := make([]RouteEntry, len(routes))
	copy(out, routes)
	for i := range out {
		allow := append([]string(nil), out[i].Allow...)
		sort.Strings(allow)
		if allow == nil {
			allow = []string{}
		}
		out[i].Allow = allow
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	if out == nil {
		out = []RouteEntry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(RoutesDocument{V: RoutesDocumentVersion, Routes: out}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// RoutesSHA256 is the hex SHA-256 of the parameter bytes (KM_SLACK_ROUTES_SHA256).
func RoutesSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseRoutesDocument decodes and shape-checks a table. It rejects a version it
// does not know and any entry missing channel, alias, profile or allow: a
// route that admits nobody must never load as a route that admits everybody.
func ParseRoutesDocument(b []byte) (*RoutesDocument, error) {
	var doc RoutesDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("slack routes: decode: %w", err)
	}
	if doc.V != RoutesDocumentVersion {
		return nil, fmt.Errorf("slack routes: unsupported version %d (want %d)", doc.V, RoutesDocumentVersion)
	}
	for i, r := range doc.Routes {
		switch {
		case r.Channel == "":
			return nil, fmt.Errorf("slack routes: entry %d has no channel", i)
		case r.Alias == "":
			return nil, fmt.Errorf("slack routes: entry %d (%s) has no alias", i, r.Channel)
		case r.Profile == "":
			return nil, fmt.Errorf("slack routes: entry %d (%s) has no profile", i, r.Channel)
		case len(r.Allow) == 0:
			return nil, fmt.Errorf("slack routes: entry %d (%s) has an empty allow list", i, r.Channel)
		}
	}
	return &doc, nil
}

// ErrNoRoutes is returned by callers that treat an absent parameter as an
// empty table.
var ErrNoRoutes = errors.New("slack routes: no table")

// BuildRouteTable compiles a document into channel id → route. An entry whose
// allow list fails to compile, or a second entry for a channel, is skipped
// with one warn line and never widens or replaces a route (same disposition
// as NewDefaultRoute returning nil on an invalid list).
func BuildRouteTable(doc *RoutesDocument, warn func(msg string, kv ...any)) map[string]*DefaultRoute {
	table := map[string]*DefaultRoute{}
	if doc == nil {
		return table
	}
	for _, e := range doc.Routes {
		if _, dup := table[e.Channel]; dup {
			warn("km-slack-bridge: channel route listed twice; keeping the first", "channel", e.Channel, "alias", e.Alias)
			continue
		}
		list, err := CompileEmailAllowlist(e.Allow)
		if err != nil || list.Len() == 0 {
			warn("km-slack-bridge: channel route has an invalid allow list; skipping", "channel", e.Channel, "alias", e.Alias, "err", err)
			continue
		}
		r := NewDefaultRoute(e.Alias, e.Profile, e.Allow, nil)
		if r == nil {
			warn("km-slack-bridge: channel route incomplete; skipping", "channel", e.Channel, "alias", e.Alias)
			continue
		}
		r.MentionOnly = e.MentionOnly
		table[e.Channel] = r
	}
	return table
}
```

Check `EmailAllowlist.Len()` exists (it is used by `HasProfileList`); if the receiver is `*EmailAllowlist`, the code above is correct as written.

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/slack/bridge/ 2>&1 | tail -3`
Expected: `ok` (every existing default-route test still passes with `MentionOnly` defaulting to true).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/channel_routes.go pkg/slack/bridge/channel_routes_test.go pkg/slack/bridge/default_route.go
git commit -m "feat(slack-bridge): canonical channel route document and table builder

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 3: Slack client — `ListBotChannels`

**Files:**
- Modify: `pkg/slack/client.go` (after `FindChannelByName`, ~line 715)
- Test: `pkg/slack/client_test.go`

**Interfaces:**
- Produces: `func (c *Client) ListBotChannels(ctx context.Context) ([]SlackChannelSummary, error)` — every non-archived public and private channel the bot is a member of, via `users.conversations`, paginated to completion.

- [ ] **Step 1: Write the failing test**

Append to `pkg/slack/client_test.go` (it already has `newClientAgainstServer` and `slackOK`):

```go
func TestListBotChannels_PaginatesUsersConversations(t *testing.T) {
	var calls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.URL.Path != "/users.conversations" {
			t.Errorf("unexpected method %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["types"] != "public_channel,private_channel" {
			t.Errorf("types = %v", body["types"])
		}
		if body["cursor"] == nil {
			w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"sec"}],"response_metadata":{"next_cursor":"p2"}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"channels":[{"id":"G2","name":"private-ops"}],"response_metadata":{"next_cursor":""}}`))
	}))
	defer ts.Close()
	got, err := newClientAgainstServer(ts).ListBotChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "C1" || got[1].Name != "private-ops" {
		t.Fatalf("got %+v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("want 2 pages, got %d", len(calls))
	}
}

func TestListBotChannels_SlackError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer ts.Close()
	if _, err := newClientAgainstServer(ts).ListBotChannels(context.Background()); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("want missing_scope error, got %v", err)
	}
}
```

Add `"context"`, `"strings"` to the test imports if absent.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/slack/ -run ListBotChannels 2>&1 | tail -3`
Expected: `ListBotChannels` undefined.

- [ ] **Step 3: Implement**

After `FindChannelByName` in `client.go`:

```go
// ListBotChannels lists every non-archived public and private channel the bot
// is a member of (users.conversations with no user ⇒ the caller's own
// memberships; scopes channels:read + groups:read). Unlike conversations.list
// the result is bounded by the bot's membership, not the workspace, so it
// pages to completion with no scan cap. The 100-page ceiling is a loop guard
// against a server that never clears next_cursor.
func (c *Client) ListBotChannels(ctx context.Context) ([]SlackChannelSummary, error) {
	var out []SlackChannelSummary
	cursor := ""
	for page := 0; page < 100; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body := map[string]any{
			"types":            "public_channel,private_channel",
			"limit":            1000,
			"exclude_archived": true,
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		resp, err := c.callJSON(ctx, "users.conversations", body)
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Channels...)
		if resp.ResponseMetadata.NextCursor == "" {
			return out, nil
		}
		cursor = resp.ResponseMetadata.NextCursor
	}
	return nil, fmt.Errorf("slack: users.conversations: cursor never cleared after 100 pages")
}
```

If `callJSON` returns the Slack `error` code wrapped in `*SlackAPIError` (it does for `FindChannelByName`), the error test passes as written; confirm the error string contains the code.

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/slack/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/client.go pkg/slack/client_test.go
git commit -m "feat(slack): ListBotChannels via users.conversations

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 4: `km init` — resolve, pre-stage, write the table, export the hash

**Files:**
- Create: `internal/app/cmd/slack_routes.go`
- Modify: `internal/app/cmd/init.go` (extract `routeProfileInboundAllow` from `ValidateSlackDefaultRouteProfile`, lines 1193–1231)
- Test: `internal/app/cmd/slack_routes_test.go`

**Interfaces:**
- Consumes: Task 1 (`config.SlackRoute`), Task 2 (`slackbridge.RouteEntry`, `CanonicalRoutesJSON`, `RoutesSHA256`, `RoutesParamPath`), Task 3 (`kmslack.SlackChannelSummary`), existing `locateSlackDefaultRouteProfile`, `resolveSlackDefaultRouteProfile`, `PreStageSlackDefaultRouteProfile`, `SlackDefaultRouteProfileSlug`, `S3ProfileUploader`, `SSMParamStore` + `ssmParamWriter`.
- Produces:
  - `type SlackRouteChannelLister interface { ListBotChannels(ctx context.Context) ([]kmslack.SlackChannelSummary, error) }`
  - `type slackRoutesPlan struct { Entries []slackbridge.RouteEntry; Profiles []*config.SlackDefaultRoute; JSON []byte; SHA string }`
  - `func effectiveSlackChannelRoutes(cfg *config.Config, channels []kmslack.SlackChannelSummary) (*slackRoutesPlan, error)`
  - `func prepareSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore, up S3ProfileUploader) error`
  - `func exportSlackRoutesHashFromSSM(ctx context.Context, cfg *config.Config, store SSMParamStore)`
  - `const envSlackRoutesSHA = "KM_SLACK_ROUTES_SHA256"`
  - `func routeProfileInboundAllow(label string, route *config.SlackDefaultRoute, searchPaths []string) ([]string, error)` (extracted; used by the default route too).

- [ ] **Step 1: Write the failing tests**

```go
// internal/app/cmd/slack_routes_test.go
package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakeLister struct {
	channels []kmslack.SlackChannelSummary
	err      error
	calls    int
}

func (f *fakeLister) ListBotChannels(context.Context) ([]kmslack.SlackChannelSummary, error) {
	f.calls++
	return f.channels, f.err
}

// fakeRoutesSSM: Get/Put over a map; implements ssmParamWriter.
type fakeRoutesSSM struct{ m map[string]string }

func (f *fakeRoutesSSM) Get(_ context.Context, name string, _ bool) (string, error) {
	return f.m[name], nil
}
func (f *fakeRoutesSSM) Put(_ context.Context, name, value string, _ bool) error {
	f.m[name] = value
	return nil
}

type fakeUploader struct{ keys []string }

func (f *fakeUploader) PutObject(_ context.Context, _, key string, _ []byte) error {
	f.keys = append(f.keys, key)
	return nil
}

func routesCfg(t *testing.T, routes []config.SlackRoute, dr *config.SlackDefaultRoute) *config.Config {
	t.Helper()
	cfg := &config.Config{ArtifactsBucket: "b", ResourcePrefix: "km"}
	cfg.Slack.Routes = routes
	cfg.Slack.DefaultRoute = dr
	return cfg
}

var botChannels = []kmslack.SlackChannelSummary{{ID: "C1", Name: "sec"}, {ID: "G2", Name: "ops"}}

func TestEffectiveSlackChannelRoutes_ListFallbackAndCanonical(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	pl := writeProfile(t, dir, "listed.yaml", routeProfileOK+"        allowEmails: [prof@example.com]\n")
	f := false
	cfg := routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"Own@Example.com"}, MentionOnly: &f},
		{Channel: "G2", Alias: "ops-bot", Profile: pl},
	}, &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"inst@example.com"}})
	plan, err := effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("entries: %+v", plan.Entries)
	}
	byCh := map[string]slackbridge.RouteEntry{}
	for _, e := range plan.Entries {
		byCh[e.Channel] = e
	}
	if e := byCh["C1"]; e.Name != "sec" || e.Alias != "ir-bot" || e.Profile != "ir-bot" || e.MentionOnly || len(e.Allow) != 1 || e.Allow[0] != "own@example.com" {
		t.Errorf("C1: own list must replace: %+v", e)
	}
	if e := byCh["G2"]; !e.MentionOnly || len(e.Allow) != 1 || e.Allow[0] != "inst@example.com" {
		t.Errorf("G2: install list must win over the profile list: %+v", e)
	}
	// No install list ⇒ the profile list.
	cfg.Slack.DefaultRoute = nil
	plan, err = effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if e.Channel == "G2" && (len(e.Allow) != 1 || e.Allow[0] != "prof@example.com") {
			t.Errorf("G2 without install list: %+v", e)
		}
	}
	if plan.SHA == "" || len(plan.JSON) == 0 || !strings.HasPrefix(string(plan.JSON), `{"v":1,"routes":[{"channel":"C1"`) {
		t.Errorf("plan JSON/SHA: %s %s", plan.JSON, plan.SHA)
	}
	if len(plan.Profiles) != 2 {
		t.Errorf("two distinct profiles to stage, got %d", len(plan.Profiles))
	}
}

func TestEffectiveSlackChannelRoutes_Errors(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	noInbound := writeProfile(t, dir, "noin.yaml", routeProfileNoInbound)
	cases := []struct {
		name   string
		routes []config.SlackRoute
		want   string
	}{
		{"unknown name", []config.SlackRoute{{Channel: "#nope", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}}}, "#nope"},
		{"unknown id", []config.SlackRoute{{Channel: "C9", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}}}, "C9"},
		{"name and id collide", []config.SlackRoute{
			{Channel: "#sec", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}},
			{Channel: "C1", Alias: "b", Profile: p, AllowEmails: []string{"k@example.com"}}}, "twice"},
		{"no list anywhere", []config.SlackRoute{{Channel: "#sec", Alias: "a", Profile: p}}, "no allowlist"},
		{"profile without inbound", []config.SlackRoute{{Channel: "#sec", Alias: "a", Profile: noInbound, AllowEmails: []string{"k@example.com"}}}, "inbound"},
	}
	for _, c := range cases {
		_, err := effectiveSlackChannelRoutes(routesCfg(t, c.routes, nil), botChannels)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	// The unknown-name error lists the bot's channels so a rename is obvious.
	_, err := effectiveSlackChannelRoutes(routesCfg(t, cases[0].routes, nil), botChannels)
	if !strings.Contains(err.Error(), "#sec") || !strings.Contains(err.Error(), "#ops") {
		t.Errorf("unknown-name error must list the bot's channels: %v", err)
	}
}

func TestPrepareSlackChannelRoutes_WritesParamStagesProfilesExportsHash(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "ops-bot", Profile: p, AllowEmails: []string{"k@example.com"}},
	}, nil)
	lister := &fakeLister{channels: botChannels}
	ssm := &fakeRoutesSSM{m: map[string]string{}}
	up := &fakeUploader{}
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, up); err != nil {
		t.Fatal(err)
	}
	if lister.calls != 1 {
		t.Errorf("one users.conversations listing per init, got %d", lister.calls)
	}
	doc := ssm.m["/km/slack/routes"]
	if doc == "" {
		t.Fatal("parameter not written")
	}
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte(doc)) {
		t.Errorf("env hash %q must be the hash of the written bytes", os.Getenv(envSlackRoutesSHA))
	}
	if len(up.keys) != 1 || up.keys[0] != "slack-profiles/ir-bot/.km-profile.yaml" {
		t.Errorf("a shared profile is staged once: %v", up.keys)
	}
}

func TestPrepareSlackChannelRoutes_NoRoutes(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "stale")
	cfg := routesCfg(t, nil, nil)
	lister := &fakeLister{err: context.Canceled}
	ssm := &fakeRoutesSSM{m: map[string]string{}}
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, &fakeUploader{}); err != nil {
		t.Fatal(err)
	}
	if lister.calls != 0 || len(ssm.m) != 0 || os.Getenv(envSlackRoutesSHA) != "" {
		t.Fatalf("no routes ⇒ no Slack call, no parameter, empty hash: calls=%d m=%v env=%q", lister.calls, ssm.m, os.Getenv(envSlackRoutesSHA))
	}
	// A table from an earlier config is overwritten with the empty document.
	ssm.m["/km/slack/routes"] = `{"v":1,"routes":[{"channel":"C1","alias":"a","profile":"p","allow":["k@example.com"]}]}`
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, &fakeUploader{}); err != nil {
		t.Fatal(err)
	}
	if ssm.m["/km/slack/routes"] != `{"v":1,"routes":[]}` {
		t.Fatalf("stale table must be emptied, got %s", ssm.m["/km/slack/routes"])
	}
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte(`{"v":1,"routes":[]}`)) {
		t.Error("hash must follow the emptied document")
	}
}

func TestExportSlackRoutesHashFromSSM(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	cfg := routesCfg(t, nil, nil)
	exportSlackRoutesHashFromSSM(context.Background(), cfg, &fakeRoutesSSM{m: map[string]string{}})
	if os.Getenv(envSlackRoutesSHA) != "" {
		t.Error("absent parameter ⇒ empty hash")
	}
	exportSlackRoutesHashFromSSM(context.Background(), cfg, &fakeRoutesSSM{m: map[string]string{"/km/slack/routes": "abc"}})
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte("abc")) {
		t.Error("hash of the live parameter")
	}
}
```

`writeProfile`, `routeProfileOK`, `routeProfileNoInbound` exist in `init_slack_default_route_test.go`. Confirm `config.Config` has a `ResourcePrefix` field and that `cfg.GetSsmPrefix()` yields `/km/` from it; if the prefix comes from another field, set that one in `routesCfg` instead.

- [ ] **Step 2: Run to verify they fail**

Run: `cd internal/app/cmd && go test . -run 'SlackChannelRoutes|SlackRoutesHash' 2>&1 | tail -3`
Expected: compile errors.

- [ ] **Step 3: Extract `routeProfileInboundAllow` in `init.go`**

Replace the body of `ValidateSlackDefaultRouteProfile` so the resolve-and-inbound part is shared:

```go
func ValidateSlackDefaultRouteProfile(route *config.SlackDefaultRoute, searchPaths []string) ([]string, error) {
	if route == nil {
		return nil, nil
	}
	if err := route.Validate(); err != nil {
		return nil, err
	}
	profileAllow, err := routeProfileInboundAllow("slack.default_route.profile", route, searchPaths)
	if err != nil {
		return nil, err
	}
	if len(profileAllow) == 0 && len(route.NormalizedEmails()) == 0 {
		return nil, errSlackDefaultRouteNoAllowlist(route.Profile)
	}
	return profileAllow, nil
}

// routeProfileInboundAllow resolves a route's profile (extends included),
// proves it has Slack inbound enabled, and returns its own
// notification.slack.inbound.allowEmails normalised (nil when none). label
// names the config key in errors ("slack.default_route.profile" or
// "slack.routes[i].profile"). Shared by the default route and channel routes.
func routeProfileInboundAllow(label string, route *config.SlackDefaultRoute, searchPaths []string) ([]string, error) {
	p, err := resolveSlackDefaultRouteProfile(route, searchPaths)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", label, route.Profile, err)
	}
	n := p.Spec.Notification
	slackOn := n != nil && n.Slack != nil && n.Slack.Enabled != nil && *n.Slack.Enabled
	inboundOn := slackOn && n.Slack.Inbound != nil && n.Slack.Inbound.Enabled != nil && *n.Slack.Inbound.Enabled
	if !slackOn || !inboundOn {
		return nil, fmt.Errorf("%s %q must set notification.slack.enabled and notification.slack.inbound.enabled: a routed sandbox replies through its inbound poller and has no other way to answer", label, route.Profile)
	}
	var profileAllow []string
	for _, e := range n.Slack.Inbound.AllowEmails {
		if strings.TrimSpace(e) == "" {
			continue
		}
		norm, _, verr := slackbridge.ValidateEmailEntry(e)
		if verr != nil {
			return nil, fmt.Errorf("%s %q: notification.slack.inbound.allowEmails: %w", label, route.Profile, verr)
		}
		profileAllow = append(profileAllow, norm)
	}
	return profileAllow, nil
}
```

Run `go test . -run DefaultRoute` in `internal/app/cmd` — the default-route tests must still pass (error texts kept).

- [ ] **Step 4: Create `slack_routes.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// envSlackRoutesSHA is exported for infra/live/use1/lambda-slack-bridge: the
// hex SHA-256 of the SSM table bytes. It is only a cold-start trigger — the
// bridge reads the table from SSM — so a wrong or empty value never misroutes;
// it only decides whether the next apply restarts the Lambda.
const envSlackRoutesSHA = "KM_SLACK_ROUTES_SHA256"

// SlackRouteChannelLister is the one Slack call channel-route resolution makes;
// *kmslack.Client satisfies it.
type SlackRouteChannelLister interface {
	ListBotChannels(ctx context.Context) ([]kmslack.SlackChannelSummary, error)
}

// slackRoutesPlan is a resolved table: the entries to write, the distinct
// profiles to pre-stage, and the canonical bytes + hash.
type slackRoutesPlan struct {
	Entries  []slackbridge.RouteEntry
	Profiles []*config.SlackDefaultRoute // one per distinct pre-stage slug
	JSON     []byte
	SHA      string
}

// effectiveSlackChannelRoutes resolves slack.routes against the bot's channel
// list. Fail-closed: any route that cannot be resolved, gated, or whose profile
// cannot answer stops the caller before any apply.
func effectiveSlackChannelRoutes(cfg *config.Config, channels []kmslack.SlackChannelSummary) (*slackRoutesPlan, error) {
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		return nil, err
	}
	byName := map[string]kmslack.SlackChannelSummary{}
	byID := map[string]kmslack.SlackChannelSummary{}
	for _, ch := range channels {
		byName[ch.Name] = ch
		byID[ch.ID] = ch
	}
	installList := cfg.Slack.DefaultRoute.NormalizedEmails()
	plan := &slackRoutesPlan{}
	seen := map[string]int{}      // channel id → route index
	staged := map[string]bool{}   // pre-stage slug
	for i := range cfg.Slack.Routes {
		r := &cfg.Slack.Routes[i]
		label := fmt.Sprintf("slack.routes[%d] (%s)", i, strings.TrimSpace(r.Channel))

		var ch kmslack.SlackChannelSummary
		var ok bool
		if name, isName := r.ChannelName(); isName {
			if ch, ok = byName[name]; !ok {
				return nil, fmt.Errorf("%s: no channel named #%s among the channels the bot is a member of (invite the bot, or the channel was renamed); bot channels: %s", label, name, botChannelNames(channels))
			}
		} else if id, isID := r.ChannelID(); isID {
			if ch, ok = byID[id]; !ok {
				return nil, fmt.Errorf("%s: the bot is not a member of channel %s (invite it first)", label, id)
			}
		}
		if j, dup := seen[ch.ID]; dup {
			return nil, fmt.Errorf("%s: resolves to channel %s (#%s), which slack.routes[%d] already names — a channel is listed twice", label, ch.ID, ch.Name, j)
		}
		seen[ch.ID] = i

		view := &config.SlackDefaultRoute{Alias: r.Alias, Profile: r.Profile}
		profileAllow, err := routeProfileInboundAllow(label+".profile", view, cfg.ProfileSearchPaths)
		if err != nil {
			return nil, err
		}
		allow := r.NormalizedEmails()
		source := "route allow_emails"
		if len(allow) == 0 {
			allow, source = installList, "slack.default_route.allow_emails"
		}
		if len(allow) == 0 {
			allow, source = profileAllow, "profile allowEmails"
		}
		if len(allow) == 0 {
			return nil, fmt.Errorf("%s has no allowlist: set allow_emails on the route, slack.default_route.allow_emails, or notification.slack.inbound.allowEmails in %q — a route is deny-by-default", label, r.Profile)
		}
		fmt.Fprintf(os.Stderr, "[info] slack.routes: #%s (%s) → alias %q, %d allowed (%s)\n", ch.Name, ch.ID, r.Alias, len(allow), source)

		slug := SlackDefaultRouteProfileSlug(r.Profile)
		if !staged[slug] {
			staged[slug] = true
			plan.Profiles = append(plan.Profiles, view)
		}
		plan.Entries = append(plan.Entries, slackbridge.RouteEntry{
			Channel: ch.ID, Name: ch.Name, Alias: r.Alias, Profile: slug,
			Allow: allow, MentionOnly: r.MentionRequired(),
		})
	}
	b, err := slackbridge.CanonicalRoutesJSON(plan.Entries)
	if err != nil {
		return nil, fmt.Errorf("slack.routes: encode: %w", err)
	}
	plan.JSON, plan.SHA = b, slackbridge.RoutesSHA256(b)
	return plan, nil
}

func botChannelNames(channels []kmslack.SlackChannelSummary) string {
	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		names = append(names, "#"+ch.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// slackRoutesParam is the SSM path for this install's table.
func slackRoutesParam(cfg *config.Config) string {
	return slackbridge.RoutesParamPath(strings.Trim(cfg.GetSsmPrefix(), "/"))
}

// prepareSlackChannelRoutes is the FAIL-CLOSED apply-time form (full km init,
// km init --slack, km slack init): resolve, pre-stage every distinct route
// profile, write the table, export the hash. No routes ⇒ no Slack call; a
// table left by an earlier config is overwritten with the empty document so a
// removed block cannot keep routing; hash "" when nothing is written.
func prepareSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore, up S3ProfileUploader) error {
	param := slackRoutesParam(cfg)
	writer, canWrite := store.(ssmParamWriter)
	if len(cfg.Slack.Routes) == 0 {
		os.Setenv(envSlackRoutesSHA, "") //nolint:errcheck
		if existing, _ := store.Get(ctx, param, false); existing != "" && canWrite {
			empty, _ := slackbridge.CanonicalRoutesJSON(nil)
			if string(existing) == string(empty) {
				os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256(empty)) //nolint:errcheck
				return nil
			}
			if err := writer.Put(ctx, param, string(empty), false); err != nil {
				return fmt.Errorf("km init: slack.routes removed but the old table at %s could not be emptied: %w", param, err)
			}
			os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256(empty)) //nolint:errcheck
			fmt.Fprintf(os.Stderr, "[info] slack.routes: no routes configured; emptied %s\n", param)
		}
		return nil
	}
	if !canWrite {
		return fmt.Errorf("km init: slack.routes configured but the SSM store is read-only")
	}
	channels, err := lister.ListBotChannels(ctx)
	if err != nil {
		return fmt.Errorf("km init: slack.routes: list the bot's channels (users.conversations): %w", err)
	}
	plan, err := effectiveSlackChannelRoutes(cfg, channels)
	if err != nil {
		return fmt.Errorf("km init: %w", err)
	}
	if up != nil && cfg.ArtifactsBucket != "" {
		for _, view := range plan.Profiles {
			if err := PreStageSlackDefaultRouteProfile(ctx, view, cfg.ArtifactsBucket, cfg.ProfileSearchPaths, up); err != nil {
				return fmt.Errorf("km init: slack.routes: %w", err)
			}
		}
	}
	if err := writer.Put(ctx, param, string(plan.JSON), false); err != nil {
		return fmt.Errorf("km init: slack.routes: write %s: %w", param, err)
	}
	os.Setenv(envSlackRoutesSHA, plan.SHA) //nolint:errcheck
	fmt.Fprintf(os.Stderr, "[info] slack.routes: %d route(s) written to %s (sha256 %s…)\n", len(plan.Entries), param, plan.SHA[:12])
	return nil
}

// exportSlackRoutesHashFromSSM is the BEST-EFFORT plan-time form (km init
// --plan, ExportTerragruntEnvVars callers that never apply the bridge): no
// Slack call, no write; the hash of whatever table is live, "" when none, so
// a plan after a config edit shows no spurious env diff. km doctor reports the
// drift instead.
func exportSlackRoutesHashFromSSM(ctx context.Context, cfg *config.Config, store SSMParamStore) {
	val, err := store.Get(ctx, slackRoutesParam(cfg), false)
	if err != nil || val == "" {
		os.Setenv(envSlackRoutesSHA, "") //nolint:errcheck
		return
	}
	os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256([]byte(val))) //nolint:errcheck
}

// newSlackRouteLister builds the Slack client channel-route resolution needs
// from the bot token in SSM. No token ⇒ error naming km slack init.
func newSlackRouteLister(ctx context.Context, cfg *config.Config, store SSMParamStore) (SlackRouteChannelLister, error) {
	tok, err := store.Get(ctx, cfg.GetSsmPrefix()+"slack/bot-token", true)
	if err != nil || strings.TrimSpace(tok) == "" {
		return nil, fmt.Errorf("slack.routes is configured but %sslack/bot-token is not set — run `km slack init` first (%v)", cfg.GetSsmPrefix(), err)
	}
	return kmslack.NewClient(strings.TrimSpace(tok), nil), nil
}
```

`(*config.SlackDefaultRoute).NormalizedEmails()` is nil-safe (it checks `r == nil`), so `cfg.Slack.DefaultRoute.NormalizedEmails()` is fine when the block is absent.

- [ ] **Step 5: Run the tests**

Run: `cd internal/app/cmd && go test . -run 'SlackChannelRoutes|SlackRoutesHash|DefaultRoute' 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/slack_routes.go internal/app/cmd/slack_routes_test.go internal/app/cmd/init.go
git commit -m "feat(init): resolve slack.routes, pre-stage profiles, write the SSM table

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 5: Wire the table into every bridge-applying path

**Files:**
- Modify: `internal/app/cmd/init.go` — scoped path (~line 429–437), full path (~line 1808–1816), plan path (~line 3466)
- Modify: `internal/app/cmd/slack.go` — `km slack init` (~line 424–433); its deps struct (~line 113–117)
- Test: `internal/app/cmd/slack_routes_wiring_test.go`

**Interfaces:**
- Consumes: Task 4.
- Produces: `prepareSlackRouting(ctx, cfg, awsCfg, up S3ProfileUploader) error` — one helper that runs `prepareSlackDefaultRoute` then `prepareSlackChannelRoutes` with production deps; `slackInitDeps.RouteChannelLister SlackRouteChannelLister` (nil ⇒ built from SSM).

- [ ] **Step 1: Write the failing test**

```go
// internal/app/cmd/slack_routes_wiring_test.go
package cmd

import (
	"os"
	"regexp"
	"testing"
)

// Every path that applies lambda-slack-bridge must prepare the channel routes
// right beside the default route, or the bridge would apply with a stale (or
// missing) table and hash. Source-level guard, name-agnostic over the call
// shape: each prepareSlackDefaultRoute call site is followed within 12 lines
// by a prepareSlackChannelRoutes or prepareSlackRouting call.
func TestEveryBridgeApplyPathPreparesChannelRoutes(t *testing.T) {
	for _, file := range []string{"init.go", "slack.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := regexp.MustCompile(`\n`).Split(string(src), -1)
		for i, l := range lines {
			if !regexp.MustCompile(`^\s*if err := prepareSlackDefaultRoute\(`).MatchString(l) {
				continue
			}
			found := false
			for j := i; j < i+12 && j < len(lines); j++ {
				if regexp.MustCompile(`prepareSlackChannelRoutes(WithAWS)?\(|exportSlackRoutesHashFromSSM\(`).MatchString(lines[j]) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s:%d: prepareSlackDefaultRoute without a channel-routes preparation nearby", file, i+1)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd internal/app/cmd && go test . -run TestEveryBridgeApplyPathPreparesChannelRoutes 2>&1 | tail -5`
Expected: FAIL naming three sites in `init.go` (scoped, full, plan) and one in `slack.go`.

- [ ] **Step 3: Add the production helper and call it at every site**

Update the guard test's inner regex to accept all three forms:
`prepareSlackChannelRoutes(WithAWS)?\(|exportSlackRoutesHashFromSSM\(`.

In `slack_routes.go` add:

```go
// prepareSlackChannelRoutesWithAWS is prepareSlackChannelRoutes with production
// deps: SSM for the table and the bot token, a Slack client built from that
// token (only when routes are configured), S3 for pre-stage. Every command that
// applies lambda-slack-bridge calls it right after prepareSlackDefaultRoute.
func prepareSlackChannelRoutesWithAWS(ctx context.Context, cfg *config.Config, awsCfg aws.Config, up S3ProfileUploader) error {
	store := &productionSSMParamStore{client: ssm.NewFromConfig(awsCfg)}
	var lister SlackRouteChannelLister
	if len(cfg.Slack.Routes) > 0 {
		l, err := newSlackRouteLister(ctx, cfg, store)
		if err != nil {
			return err
		}
		lister = l
	}
	return prepareSlackChannelRoutes(ctx, cfg, lister, store, up)
}
```
(imports: `"github.com/aws/aws-sdk-go-v2/aws"`, `"github.com/aws/aws-sdk-go-v2/service/ssm"`.)

Scoped path (`init.go` ~429): after the existing `prestageSlackDefaultRouteForModule(...)` call add

```go
	// 2026-10-22: channel routes — only a bridge apply reads the table.
	if module == "lambda-slack-bridge" {
		if err := prepareSlackChannelRoutesWithAWS(ctx, cfg, awsCfg, &s3ProfileUploaderClient{inner: s3.NewFromConfig(awsCfg)}); err != nil {
			return fmt.Errorf("km init: %w", err)
		}
	}
```

Full path (`init.go` ~1808): replace the `if cfg.Slack.DefaultRoute != nil { … }` block with

```go
	// 2026-10-16 / 2026-10-22: Slack default route + channel routes. Validate
	// (deny-by-default, profiles carry Slack inbound), pre-stage for cold-create,
	// write the channel table and export its hash. With neither block the call
	// still runs: it empties a table left by an earlier config and exports "".
	if err := prepareSlackDefaultRoute(cfg); err != nil {
		return fmt.Errorf("km init: %w", err)
	}
	if err := prestageSlackDefaultRoute(ctx, cfg, &s3ProfileUploaderClient{inner: s3.NewFromConfig(awsCfg)}); err != nil {
		return err
	}
	if err := prepareSlackChannelRoutesWithAWS(ctx, cfg, awsCfg, &s3ProfileUploaderClient{inner: s3.NewFromConfig(awsCfg)}); err != nil {
		return fmt.Errorf("km init: %w", err)
	}
```
(`prepareSlackDefaultRoute` and `prestageSlackDefaultRoute` are already no-ops when `DefaultRoute` is nil, so dropping the outer `if` changes nothing for them.)

Plan path (`init.go` ~3466): after `prepareSlackDefaultRoute`, the plan must neither resolve nor write:

```go
	// Plan: never resolves or writes the table; the hash of the LIVE table keeps
	// the env diff honest. km doctor reports config-vs-table drift.
	exportSlackRoutesHashFromSSM(ctx, cfg, &productionSSMParamStore{client: ssm.NewFromConfig(awsCfg)})
```

`km slack init` (`slack.go` ~424): after the existing `prestageSlackDefaultRoute` call inside `if d.Cfg != nil { … }`:

```go
			store := d.SSM
			lister := d.RouteChannelLister
			if lister == nil && len(d.Cfg.Slack.Routes) > 0 {
				if l, err := newSlackRouteLister(ctx, d.Cfg, store); err != nil {
					return fmt.Errorf("km slack init: %w", err)
				} else {
					lister = l
				}
			}
			if err := prepareSlackChannelRoutes(ctx, d.Cfg, lister, store, d.RouteProfileUploader); err != nil {
				return fmt.Errorf("km slack init: %w", err)
			}
```
Add `RouteChannelLister SlackRouteChannelLister` to the deps struct beside `RouteProfileUploader` (line ~117) with the comment "nil ⇒ built from the bot token in SSM". Confirm `d.SSM` satisfies `SSMParamStore` and `ssmParamWriter` (it has `Get`/`Put` — used at lines 228 and 261).

- [ ] **Step 4: Run the tests**

Run: `cd internal/app/cmd && go test . 2>&1 | tail -5`
Expected: `ok` apart from the pre-existing `TestBootstrap*`/`TestCluster*`/`TestConfigure*` failures recorded in memory (`project_cmd_suite_pre_existing_failures`); confirm no new FAIL lines.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/slack.go internal/app/cmd/slack_routes.go internal/app/cmd/slack_routes_wiring_test.go
git commit -m "feat(init): prepare channel routes on every bridge-applying path

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 6: Terraform — env, IAM, live feed

**Files:**
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` (after `slack_default_route_profile_allow`, line 213)
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` (env block after `KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW`, line 489; new policy after `ssm_signing_secret`, line 416)
- Modify: `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` (after `slack_default_route_profile_allow`, line 179)
- Test: `pkg/terragrunt/slack_channel_routes_module_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/terragrunt/slack_channel_routes_module_test.go
package terragrunt_test

import (
	"regexp"
	"strings"
	"testing"
)

// The channel route table reaches the bridge as SSM /{prefix}/slack/routes.
// Three things must agree: the env var naming the path, the IAM grant on
// exactly that path, and the hash env var fed from get_env by the live unit.
func TestSlackBridgeModule_ChannelRoutesParamEnvIAMAndHash(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")

	path := `/${var.resource_prefix}/slack/routes`
	if !regexp.MustCompile(`KM_SLACK_ROUTES_PARAM\s*=\s*"` + regexp.QuoteMeta(path) + `"`).MatchString(main) {
		t.Errorf("main.tf must set KM_SLACK_ROUTES_PARAM = %q", path)
	}
	if !regexp.MustCompile(`KM_SLACK_ROUTES_SHA256\s*=\s*var\.slack_routes_sha256`).MatchString(main) {
		t.Error("main.tf must set KM_SLACK_ROUTES_SHA256 = var.slack_routes_sha256")
	}
	if !strings.Contains(vars, `variable "slack_routes_sha256"`) {
		t.Error("variables.tf missing slack_routes_sha256")
	}
	if !regexp.MustCompile(`slack_routes_sha256\s+=\s+get_env\("KM_SLACK_ROUTES_SHA256", ""\)`).MatchString(live) {
		t.Error("live terragrunt.hcl must feed slack_routes_sha256 from get_env(\"KM_SLACK_ROUTES_SHA256\", \"\")")
	}
	i := strings.Index(main, `Sid    = "SSMChannelRoutes"`)
	if i == -1 {
		t.Fatal("SSMChannelRoutes statement not found")
	}
	stmt := main[i:]
	stmt = stmt[:strings.Index(stmt, "}\n")]
	if !strings.Contains(stmt, `"ssm:GetParameter"`) {
		t.Error("SSMChannelRoutes needs ssm:GetParameter")
	}
	if !strings.Contains(stmt, ":parameter"+path) {
		t.Errorf("SSMChannelRoutes must name exactly parameter%s", path)
	}
	if strings.Contains(stmt, "*") {
		t.Error("SSMChannelRoutes must be an exact ARN, no wildcard")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/terragrunt/ -run ChannelRoutes 2>&1 | tail -5`
Expected: FAIL on every assertion.

- [ ] **Step 3: Edit the module, variables and live unit**

`variables.tf`, after line 213:

```hcl
variable "slack_routes_sha256" {
  description = "Hex SHA-256 of the channel route table at SSM /{prefix}/slack/routes (km init --slack writes both). Only a cold-start trigger: a changed table changes this value and the Lambda re-reads the parameter."
  type        = string
  default     = ""
}
```

`main.tf` env block, after `KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW`:

```hcl
      # 2026-10-22 - channel route table: the bridge reads the SSM parameter at
      # cold start; the hash only exists to force that cold start on a change.
      KM_SLACK_ROUTES_PARAM  = "/${var.resource_prefix}/slack/routes"
      KM_SLACK_ROUTES_SHA256 = var.slack_routes_sha256
```

`main.tf`, after the `ssm_signing_secret` policy resource:

```hcl
# 2026-10-22 — channel route table (km init writes it; the bridge reads it at
# cold start). Exact ARN: the bridge needs this one parameter and nothing else
# under /{prefix}/slack/.
resource "aws_iam_role_policy" "ssm_channel_routes" {
  name = "${local.function_name}-ssm-channel-routes"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMChannelRoutes"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/slack/routes"
      }
    ]
  })
}
```

Confirm `var.resource_prefix` exists in `variables.tf` (it is used for `KM_QUOTA_TABLE`).

`terragrunt.hcl`, after line 179:

```hcl
  # 2026-10-22 - channel route table hash. km init / km init --slack export it
  # after writing SSM /{prefix}/slack/routes. Empty = no routes (dormant).
  slack_routes_sha256 = get_env("KM_SLACK_ROUTES_SHA256", "")
```

Run `terraform fmt infra/modules/lambda-slack-bridge/v1.0.0/` and `terragrunt hclfmt infra/live/use1/lambda-slack-bridge/terragrunt.hcl` if the binaries are on PATH; otherwise keep the alignment by hand (adjacent `=` aligned).

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/terragrunt/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add infra/modules/lambda-slack-bridge/v1.0.0/main.tf infra/modules/lambda-slack-bridge/v1.0.0/variables.tf infra/live/use1/lambda-slack-bridge/terragrunt.hcl pkg/terragrunt/slack_channel_routes_module_test.go
git commit -m "feat(infra): slack bridge reads the channel route table from SSM

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 7: Bridge ladder — select the route per channel

**Files:**
- Modify: `pkg/slack/bridge/events_handler.go` (fields ~150; `Handle` 283–410 and 565–660; `routeContinuity`/`routeDecision` 795–818; `continuityBypass` 838–880; `resolveDefaultRoute` 888–915; `routeTarget` 918–985; `senderAllowedForSandbox`/`senderEmailAllowed` 992–1040; `routeAllows` 1046–1057; `deliver` 1187–1200)
- Test: `pkg/slack/bridge/events_handler_channel_routes_test.go`

**Interfaces:**
- Consumes: Task 2 (`DefaultRoute.MentionOnly`).
- Produces: `EventsHandler.Routes map[string]*DefaultRoute`; `routeContinuity.route`, `routeDecision.route *DefaultRoute`; signatures
  `resolveDefaultRoute(ctx, msg, cont, skipMention)` (reads `cont.route`),
  `routeTarget(ctx, msg, cont)` (reads `cont.route`, sets `decision.route`),
  `continuityBypass(ctx, msg, cont)` (reads `cont.route`),
  `routeAllows(route *DefaultRoute, gate emailGate) func(string) bool`,
  `senderAllowedForSandbox(ctx, route, info, userID, channel, gate)`,
  `senderEmailAllowed(ctx, route, info, userID, gate)`,
  `deliver(ctx, route, info, coldCreate, body, threadTS, dedupID)`.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/slack/bridge/events_handler_channel_routes_test.go
package bridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeAliasByName resolves per alias, so two routes can point at two boxes.
type fakeAliasByName struct {
	infos map[string]SandboxRoutingInfo
	calls []string
}

func (f *fakeAliasByName) ResolveAlias(_ context.Context, alias string) (SandboxRoutingInfo, bool, error) {
	f.calls = append(f.calls, alias)
	info, ok := f.infos[alias]
	return info, ok, nil
}

// channelRoutedHandler: CSEC → ir-bot (mention required), COPS → ops-bot
// (mention_only false, kurt only), default route → ir-bot (kurt only).
// U0KURT = kurt@example.com, U0OTHER = other@example.com.
func channelRoutedHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAliasByName, *fakePublisher, *scriptedEmailsByUser) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{}
	alias := &fakeAliasByName{infos: map[string]SandboxRoutingInfo{
		"ir-bot":  {SandboxID: "sb-irbot", QueueURL: "https://sqs.example/irbot.fifo"},
		"ops-bot": {SandboxID: "sb-ops", QueueURL: "https://sqs.example/ops.fifo"},
	}}
	pub := &fakePublisher{}
	emails := &scriptedEmailsByUser{byUser: map[string]string{"U0KURT": "kurt@example.com", "U0OTHER": "other@example.com"}}
	h.DefaultRoute = NewDefaultRoute("ir-bot", "ir-bot", []string{"kurt@example.com"}, nil)
	sec := NewDefaultRoute("ir-bot", "ir-bot", []string{"*@example.com"}, nil)
	ops := NewDefaultRoute("ops-bot", "ops", []string{"kurt@example.com"}, nil)
	ops.MentionOnly = false
	h.Routes = map[string]*DefaultRoute{"CSEC": sec, "COPS": ops}
	h.AliasResolver = alias
	h.CreatePublisher = pub
	h.SenderEmails = emails
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.infos["ir-bot"], "sb-ops": alias.infos["ops-bot"]}}
	return h, sqs, threads, alias, pub, emails
}

// req builds a signed message event: mention=true prefixes "<@UBOT123> "
// (the bot id routedHandler's BotUserID fake answers). routedRequest is the
// helper events_handler_default_route_test.go already defines.
func req(t *testing.T, now time.Time, channel, channelType, user, text, ts, threadTS, eventID string, mention bool) EventsRequest {
	t.Helper()
	if mention {
		text = "<@UBOT123> " + text
	}
	return routedRequest(t, now, channel, channelType, user, text, ts, threadTS, eventID)
}

func TestChannelRoutes_MappedChannelGoesToItsAlias_UnmappedToDefault(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _ := channelRoutedHandler(t, now)
	// COPS: top-level, no mention, other@ is NOT on the ops list ⇒ drop.
	resp := h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0OTHER", "do it", "1.0", "", "CR1", false))
	if resp.StatusCode != 200 || len(sqs.sends) != 0 {
		t.Fatalf("unlisted sender on a mention_only:false channel must be dropped: %+v %d", resp, len(sqs.sends))
	}
	// COPS: top-level, no mention, kurt ⇒ dispatched to sb-ops.
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "do it", "1.1", "", "CR2", false))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/ops.fifo" {
		t.Fatalf("mapped channel must enqueue to its alias's box: %+v", sqs.sends)
	}
	// CSEC: mention required; without it, drop.
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "1.2", "", "CR3", false))
	if len(sqs.sends) != 1 {
		t.Fatal("CSEC without a mention must not dispatch")
	}
	// CSEC: with a mention, other@example.com is allowed by *@example.com.
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "1.3", "", "CR4", true))
	if len(sqs.sends) != 2 || sqs.sends[1].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("CSEC must dispatch to ir-bot: %+v", sqs.sends)
	}
	// CUNMAPPED: default route; other@ is not on the install list ⇒ drop; kurt ⇒ ir-bot.
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0OTHER", "hi", "1.4", "", "CR5", true))
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0KURT", "hi", "1.5", "", "CR6", true))
	if len(sqs.sends) != 3 || sqs.sends[2].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("unmapped channel must use the default route: %+v", sqs.sends)
	}
	if strings.Join(alias.calls, ",") == "" {
		t.Fatal("alias resolver must have been consulted")
	}
}

func TestChannelRoutes_ColdCreateCarriesTheRoutesAliasAndProfile(t *testing.T) {
	now := time.Now()
	h, _, _, alias, pub, _ := channelRoutedHandler(t, now)
	delete(alias.infos, "ops-bot")
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "first", "2.0", "", "CR7", false))
	if len(pub.calls) != 1 || pub.calls[0].alias != "ops-bot" || pub.calls[0].profile != "ops" {
		t.Fatalf("cold-create must name the route's alias and profile slug: %+v", pub.calls)
	}
}

func TestChannelRoutes_DMIgnoresTheTable(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.Routes["D0DM"] = h.Routes["COPS"] // a table entry for a D… id must not apply: a DM takes the default route
	h.Handle(context.Background(), req(t, now, "D0DM", "im", "U0OTHER", "hi", "3.0", "", "CR8", false))
	if len(sqs.sends) != 0 {
		t.Fatal("a DM must be gated by the default route (other@ is not on the install list)")
	}
}

func TestChannelRoutes_ContinuityKeepsThreadOnItsBoxAfterTableChange(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _ := channelRoutedHandler(t, now)
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "start", "4.0", "", "CR9", false))
	if len(sqs.sends) != 1 {
		t.Fatal("setup: first turn must dispatch")
	}
	// Remap COPS to ir-bot; the existing thread (root ts 4.0) must still go to sb-ops.
	h.Routes["COPS"] = h.Routes["CSEC"]
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "follow-up", "4.1", "4.0", "CR10", false))
	if len(sqs.sends) != 2 || sqs.sends[1].url != "https://sqs.example/ops.fifo" {
		t.Fatalf("continuity must win over the new mapping: %+v (threads %+v)", sqs.sends, threads)
	}
}

func TestChannelRoutes_PeerClaimBeatsMapping(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.Relayer = &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}}
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "hi", "5.0", "", "CR11", false))
	if len(sqs.sends) != 0 {
		t.Fatal("a peer that owns the channel must win over a mapping")
	}
}

func TestChannelRoutes_NoDefaultRouteStillRoutesMappedChannels(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.DefaultRoute = nil
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "hi", "6.0", "", "CR12", false))
	if len(sqs.sends) != 1 {
		t.Fatal("routes without a default route must still dispatch")
	}
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0KURT", "hi", "6.1", "", "CR13", true))
	if len(sqs.sends) != 1 {
		t.Fatal("an unmapped channel with no default route is the Phase 96 path: no dispatch")
	}
}
```

`newHandler`, `routedRequest`, `scriptedEmailsByUser`, `fakeByID`, `fakePublisher`, `fakeRelayer`, `PeerClaimResult` and `fakeSQS.sends[i].url` exist in `events_handler_test.go` / `events_handler_default_route_test.go`. `routedHandler` there shows how `BotUserID` is faked to answer `UBOT123`; copy that line into `channelRoutedHandler` (the fifth return of `newHandler` is `sandboxes`, the seventh `reactor`; the `BotUserID` fake is set on `h` the same way `routedHandler` sets it). The thread-continuity test relies on the first dispatch writing the thread row through `fakeThreads` exactly as `TestDefaultRoute_*` continuity tests do; read `fakeThreads` to confirm `LookupSandbox` returns what `Upsert` stored.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./pkg/slack/bridge/ -run ChannelRoutes 2>&1 | tail -5`
Expected: compile error (`Routes` undefined).

- [ ] **Step 3: Implement the refactor**

1. Field, after `DefaultRoute *DefaultRoute` (line ~150):

```go
	// Routes (2026-10-22): channel id → route, from SSM /{prefix}/slack/routes
	// at cold start. Consulted only on the ownership miss, before the default
	// route; a DM (D… id) never matches. nil/empty ⇒ default route only.
	Routes map[string]*DefaultRoute
```

2. In `Handle`, replace the four declarations at line ~286 with:

```go
	routed := false     // info came from a route, not channel ownership
	coldCreate := false // route alias absent: publish a SandboxCreate instead of enqueueing
	rebind := false     // routed thread row must be rewritten (alias moved / orphaned thread), after delivery
	gate := gateOwned   // which email list decides when the box's row carries none
	// The route this message runs against: the channel's own entry, else the
	// default route. Selected before continuity so every rung reads one value.
	route, routeName := h.selectRoute(msg)
```
and add the method:

```go
// selectRoute picks the channel's route or the default route. routeName is the
// log field: the channel id when the table matched, "default" otherwise.
func (h *EventsHandler) selectRoute(msg slackMessageEvent) (*DefaultRoute, string) {
	if msg.ChannelType != "im" {
		if r, ok := h.Routes[msg.Channel]; ok && r != nil {
			return r, msg.Channel
		}
	}
	return h.DefaultRoute, "default"
}
```

3. Line 297: `if route != nil && msg.ThreadTS != "" && …` and set `cont.route = route` immediately after `var cont routeContinuity`. Line 378: `if route == nil {`. Line 388 unchanged in shape (`resolveDefaultRoute(ctx, msg, cont, skipMention)`), add after the `routed = true` line: `h.log().Info("events: route: selected", "route", routeName, "channel", msg.Channel, "alias", route.Alias, "cold_create", coldCreate)`.

4. `routeContinuity` gains `route *DefaultRoute`; `routeDecision` gains `route *DefaultRoute`. In `continuityBypass`, replace both `h.DefaultRoute.Alias` with `cont.route.Alias`. In `routeTarget`, replace every `h.DefaultRoute` with `cont.route`, and set `route: cont.route` on every returned `routeDecision{…}`. In `resolveDefaultRoute`, the mention condition becomes `if !skipMention && requiresMention(msg.ChannelType) && cont.route.MentionOnly {`, and the gate call becomes `h.senderAllowedForSandbox(ctx, cont.route, decision.info, msg.User, msg.Channel, decision.gate)`.

5. `routeAllows(route *DefaultRoute, gate emailGate)`: body uses `route` instead of `h.DefaultRoute`. `senderEmailAllowed(ctx, route *DefaultRoute, info, userID, gate)` passes `route` to `routeAllows`; `senderAllowedForSandbox(ctx, route, info, userID, channel, gate)` passes it on. Callers: line 434 → `h.senderAllowedForSandbox(ctx, nil, info, msg.User, msg.Channel, gateOwned)`; lines 574/579 → `h.senderEmailAllowed(cctx, route, info, u, …)` (the `route` from step 2 is in scope); line 911 → as in step 4; the two-lists test at `events_handler_two_lists_test.go:135/138` → `h.routeAllows(h.DefaultRoute, gateRouteCreate)` etc.

6. `deliver(ctx, route *DefaultRoute, info, coldCreate, body, threadTS, dedupID)`: the cold-create branch uses `route.Alias`/`route.Profile` and errors when `route == nil`; log line gains `"route"`. Callers at 631/656 pass `route`.

7. Logging: the one `events: route: selected` Info line from step 3 carries `route` (channel id or `default`), `channel`, `alias` and `cold_create`; the existing drop lines keep their text. One grep on `route: selected` answers which route a turn took.

- [ ] **Step 4: Run the whole package**

Run: `go test ./pkg/slack/bridge/ 2>&1 | tail -5`
Expected: `ok`; every pre-existing default-route, two-lists, DM and allow-emails test still passes (the default route's `MentionOnly` is true, so behaviour is unchanged).

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/events_handler.go pkg/slack/bridge/events_handler_channel_routes_test.go pkg/slack/bridge/events_handler_two_lists_test.go
git commit -m "feat(slack-bridge): route ladder selects a per-channel route before the default

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 8: Lambda wiring — read the table at cold start

**Files:**
- Modify: `cmd/km-slack-bridge/main.go` (`wireDefaultRoute` 783–826; call site ~370)
- Test: `cmd/km-slack-bridge/channel_routes_wiring_test.go`

**Interfaces:**
- Consumes: Task 2, Task 7.
- Produces:
  - `type routesParamReader interface { GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) }`
  - `func wireChannelRoutes(ctx context.Context, h *bridge.EventsHandler, reader routesParamReader, path string, d defaultRouteDeps)`
  - `func wireRouteDeps(h *bridge.EventsHandler, d defaultRouteDeps)` (AliasResolver + CreatePublisher; shared with `wireDefaultRoute`).

- [ ] **Step 1: Write the failing tests**

```go
// cmd/km-slack-bridge/channel_routes_wiring_test.go
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakeParam struct {
	value string
	err   error
	path  string
}

func (f *fakeParam) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.path = aws.ToString(in.Name)
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(f.value)}}, nil
}

func TestWireChannelRoutes_TableLoadedAndDepsWired(t *testing.T) {
	h := &bridge.EventsHandler{}
	p := &fakeParam{value: `{"v":1,"routes":[{"channel":"C1","alias":"ir-bot","profile":"ir-bot","allow":["*@example.com"],"mention_only":false}]}`}
	wireChannelRoutes(context.Background(), h, p, "/km/slack/routes", defaultRouteDeps{})
	if p.path != "/km/slack/routes" {
		t.Errorf("read %q", p.path)
	}
	r := h.Routes["C1"]
	if r == nil || r.Alias != "ir-bot" || r.MentionOnly || !r.Allows("x@example.com") {
		t.Fatalf("table: %+v", h.Routes)
	}
	if h.AliasResolver == nil || h.CreatePublisher == nil {
		t.Fatal("routes need the alias resolver and the create publisher even with no default route")
	}
}

func TestWireChannelRoutes_AbsentOrBrokenLeavesTableEmpty(t *testing.T) {
	for _, p := range []*fakeParam{
		{err: &ssmtypes.ParameterNotFound{}},
		{err: errors.New("boom")},
		{value: `{"v":9,"routes":[]}`},
		{value: `not json`},
	} {
		h := &bridge.EventsHandler{}
		wireChannelRoutes(context.Background(), h, p, "/km/slack/routes", defaultRouteDeps{})
		if len(h.Routes) != 0 {
			t.Errorf("%+v: table must be empty", p)
		}
		if h.AliasResolver != nil {
			t.Errorf("%+v: no routes ⇒ no route deps wired here (the default route wires its own)", p)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/km-slack-bridge/ -run WireChannelRoutes 2>&1 | tail -3`
Expected: `wireChannelRoutes` undefined.

- [ ] **Step 3: Implement**

In `main.go`, extract from `wireDefaultRoute` the two lines that set `AliasResolver` and `CreatePublisher` into:

```go
// wireRouteDeps wires what any route (default or channel) needs to resolve an
// alias and cold-create. Idempotent.
func wireRouteDeps(h *bridge.EventsHandler, d defaultRouteDeps) {
	if h.AliasResolver == nil {
		h.AliasResolver = &bridge.DDBAliasResolver{Client: d.ddb, TableName: d.sandboxesTable}
	}
	if h.CreatePublisher == nil {
		h.CreatePublisher = &bridge.EventBridgeAdapter{Client: d.eventBridge, ArtifactBucket: d.artifactBucket}
	}
}
```
and call `wireRouteDeps(h, d)` where those two assignments were.

Add:

```go
// routesParamReader is the one SSM call the channel route table needs.
type routesParamReader interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// wireChannelRoutes reads the channel route table once per cold start. An
// absent parameter is an empty table (Info); a read or decode error logs once
// at WARN and leaves the table empty, so the default route keeps working and
// no event ever fails on it. KM_SLACK_ROUTES_SHA256 is logged for correlation
// with km init; it is not verified (the parameter is the truth).
func wireChannelRoutes(ctx context.Context, h *bridge.EventsHandler, reader routesParamReader, path string, d defaultRouteDeps) {
	out, err := reader.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(path)})
	if err != nil {
		var nf *ssmtypes.ParameterNotFound
		if errors.As(err, &nf) {
			slog.Info("km-slack-bridge: no channel route table", "param", path)
		} else {
			slog.Warn("km-slack-bridge: channel route table unreadable; channel routes OFF", "param", path, "err", err)
		}
		return
	}
	raw := []byte(aws.ToString(out.Parameter.Value))
	doc, err := bridge.ParseRoutesDocument(raw)
	if err != nil {
		slog.Warn("km-slack-bridge: channel route table invalid; channel routes OFF", "param", path, "err", err)
		return
	}
	table := bridge.BuildRouteTable(doc, func(msg string, kv ...any) { slog.Warn(msg, kv...) })
	if len(table) == 0 {
		slog.Info("km-slack-bridge: channel route table empty", "param", path)
		return
	}
	h.Routes = table
	wireRouteDeps(h, d)
	slog.Info("km-slack-bridge: channel routes enabled", "count", len(table),
		"sha256", bridge.RoutesSHA256(raw), "env_sha256", os.Getenv("KM_SLACK_ROUTES_SHA256"))
}
```

Call site, right after `wireDefaultRoute(...)` in `main`:

```go
	// 2026-10-22: channel route table (SSM, read at cold start). The hash env
	// var only exists so km init --slack cold-starts this function on a change.
	wireChannelRoutes(context.Background(), eventsHandler, initSSMC,
		envOr("KM_SLACK_ROUTES_PARAM", bridge.RoutesParamPath(prefix)),
		defaultRouteDeps{ddb: initDDB, sandboxesTable: sandboxesTable, eventBridge: initEventBridge, artifactBucket: artifactsBucket})
```
`prefix`, `initSSMC`, `initDDB`, `initEventBridge`, `artifactsBucket`, `sandboxesTable` are the locals already used by `wireDefaultRoute`'s call; use the exact names in scope at that point. Imports: `errors`, `os`, `ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"` as needed.

Also update the file header comment block (lines ~17–20) with `KM_SLACK_ROUTES_PARAM` and `KM_SLACK_ROUTES_SHA256`.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/km-slack-bridge/ 2>&1 | tail -3`
Expected: `ok` (the existing `TestWireDefaultRoute_*` still pass with `wireRouteDeps`).

- [ ] **Step 5: Commit**

```bash
git add cmd/km-slack-bridge/main.go cmd/km-slack-bridge/channel_routes_wiring_test.go
git commit -m "feat(slack-bridge): load the channel route table from SSM at cold start

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 9: `km doctor` — Slack channel routes group

**Files:**
- Create: `internal/app/cmd/doctor_slack_routes.go`
- Modify: `internal/app/cmd/doctor.go` (`appConfigAdapter` ~line 345: add `GetSlackRoutes`; deps struct ~line 553: add `SlackChannelLister`; wiring after the default-route block ~line 4725; production deps ~line 5390)
- Test: `internal/app/cmd/doctor_slack_routes_test.go`

**Interfaces:**
- Consumes: Task 4 (`effectiveSlackChannelRoutes`, `SlackRouteChannelLister`, `slackRoutesParam`).
- Produces: `func checkSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore) []CheckResult` — one SKIPPED result when `cfg == nil || len(cfg.Slack.Routes) == 0`; else exactly three results named `Slack channel routes profiles`, `Slack channel routes channels`, `Slack channel routes table`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/app/cmd/doctor_slack_routes_test.go
package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestCheckSlackChannelRoutes_SkippedWhenAbsent(t *testing.T) {
	rs := checkSlackChannelRoutes(context.Background(), &config.Config{}, nil, nil)
	if len(rs) != 1 || rs[0].Status != CheckSkipped {
		t.Fatalf("want one SKIPPED, got %+v", rs)
	}
}

func TestCheckSlackChannelRoutes_GreenAndDrift(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"k@example.com"}}}, nil)
	plan, err := effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	ssm := &fakeRoutesSSM{m: map[string]string{"/km/slack/routes": string(plan.JSON)}}
	rs := checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if len(rs) != 3 {
		t.Fatalf("want 3 results, got %+v", rs)
	}
	for _, name := range []string{"Slack channel routes profiles", "Slack channel routes channels", "Slack channel routes table"} {
		if st, msg := statusOf(rs, name); st != CheckOK {
			t.Errorf("%s: %s (%s)", name, st, msg)
		}
	}
	// Drift: the live table differs from what the config produces now.
	ssm.m["/km/slack/routes"] = `{"v":1,"routes":[]}`
	rs = checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if st, msg := statusOf(rs, "Slack channel routes table"); st != CheckWarn || !strings.Contains(msg, "km init --slack") {
		t.Errorf("drift must WARN naming km init --slack: %s %s", st, msg)
	}
	// Absent table.
	delete(ssm.m, "/km/slack/routes")
	rs = checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if st, _ := statusOf(rs, "Slack channel routes table"); st != CheckWarn {
		t.Error("absent table must WARN")
	}
}

func TestCheckSlackChannelRoutes_UnknownChannelAndBadProfile(t *testing.T) {
	dir := t.TempDir()
	noIn := writeProfile(t, dir, "noin.yaml", routeProfileNoInbound)
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#nope", Alias: "a", Profile: noIn, AllowEmails: []string{"k@example.com"}}}, nil)
	rs := checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, &fakeRoutesSSM{m: map[string]string{}})
	if st, msg := statusOf(rs, "Slack channel routes profiles"); st != CheckWarn || !strings.Contains(msg, "inbound") {
		t.Errorf("profile: %s %s", st, msg)
	}
	if st, msg := statusOf(rs, "Slack channel routes channels"); st != CheckWarn || !strings.Contains(msg, "#nope") {
		t.Errorf("channels: %s %s", st, msg)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd internal/app/cmd && go test . -run CheckSlackChannelRoutes 2>&1 | tail -3`
Expected: undefined `checkSlackChannelRoutes`.

- [ ] **Step 3: Implement the check**

```go
// internal/app/cmd/doctor_slack_routes.go
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// checkSlackChannelRoutes validates slack.routes. Read-only: one
// users.conversations listing, one SSM read, no writes. One SKIPPED result
// when the block is absent; otherwise three results (profiles, channels,
// table), evaluated once.
func checkSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore) []CheckResult {
	if cfg == nil || len(cfg.Slack.Routes) == 0 {
		return []CheckResult{{Name: "Slack channel routes", Status: CheckSkipped, Message: "slack.routes not set — no channel routes"}}
	}
	remediation := "Fix km-config.yaml slack.routes (and the profiles), then `km init --slack`."
	profiles := CheckResult{Name: "Slack channel routes profiles", Status: CheckOK}
	channels := CheckResult{Name: "Slack channel routes channels", Status: CheckOK}
	table := CheckResult{Name: "Slack channel routes table", Status: CheckOK}

	// 1. Profiles: shape + each route's profile resolves with Slack inbound.
	var profileErrs []string
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		profileErrs = append(profileErrs, err.Error())
	}
	for i := range cfg.Slack.Routes {
		r := &cfg.Slack.Routes[i]
		view := &config.SlackDefaultRoute{Alias: r.Alias, Profile: r.Profile}
		if _, err := routeProfileInboundAllow(fmt.Sprintf("slack.routes[%d].profile", i), view, cfg.ProfileSearchPaths); err != nil {
			profileErrs = append(profileErrs, err.Error())
		}
	}
	if len(profileErrs) > 0 {
		profiles.Status, profiles.Message, profiles.Remediation = CheckWarn, strings.Join(profileErrs, "; "), remediation
	} else {
		profiles.Message = fmt.Sprintf("%d route(s), every profile resolves with Slack inbound enabled", len(cfg.Slack.Routes))
	}

	// 2. Channels: every route resolves among the bot's channels; 3. Table:
	// the live parameter equals what the config produces now.
	var list []kmslack.SlackChannelSummary
	if lister == nil {
		channels.Status, channels.Message = CheckSkipped, "no Slack client (bot token not configured)"
		table.Status, table.Message = CheckSkipped, "not evaluated without a Slack client"
		return []CheckResult{profiles, channels, table}
	}
	list, err := lister.ListBotChannels(ctx)
	if err != nil {
		channels.Status, channels.Message = CheckWarn, "users.conversations failed: "+err.Error()
		table.Status, table.Message = CheckSkipped, "not evaluated: channel listing failed"
		return []CheckResult{profiles, channels, table}
	}
	plan, err := effectiveSlackChannelRoutes(cfg, list)
	if err != nil {
		channels.Status, channels.Message, channels.Remediation = CheckWarn, err.Error(), remediation
		table.Status, table.Message = CheckSkipped, "not evaluated: routes do not resolve"
		return []CheckResult{profiles, channels, table}
	}
	names := make([]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		names = append(names, fmt.Sprintf("#%s→%s", e.Name, e.Alias))
	}
	channels.Message = strings.Join(names, ", ")

	live, gerr := store.Get(ctx, slackRoutesParam(cfg), false)
	switch {
	case gerr != nil || live == "":
		table.Status, table.Message = CheckWarn, slackRoutesParam(cfg)+" is absent — the bridge has no channel routes"
		table.Remediation = "Run `km init --slack`."
	case slackbridge.RoutesSHA256([]byte(live)) != plan.SHA:
		table.Status, table.Message = CheckWarn, "the live table differs from km-config.yaml slack.routes (drift)"
		table.Remediation = "Run `km init --slack` to publish the current routes."
	default:
		table.Message = fmt.Sprintf("%s matches km-config.yaml (sha256 %s…)", slackRoutesParam(cfg), plan.SHA[:12])
	}
	return []CheckResult{profiles, channels, table}
}
```

Note: when `effectiveSlackChannelRoutes` fails on an unknown channel, the profiles result was already computed independently above, so the test's "both WARN" expectation holds (the profile check runs before the channel check and does not depend on it).

- [ ] **Step 4: Wire into `km doctor`**

In `doctor.go`:
- `appConfigAdapter`: add `func (a *appConfigAdapter) GetSlackRoutesConfig() *appcfg.Config { return a.cfg }` (the check needs the whole config for search paths and the SSM prefix).
- Deps struct (~line 553): add `SlackChannelLister SlackRouteChannelLister // users.conversations for the channel-routes group; nil ⇒ channels/table SKIPPED`.
- Production deps (~line 5390, beside `deps.SlackEmailLookup = slackClientForDoctor`): `deps.SlackChannelLister = slackClientForDoctor`.
- After the default-route registration block (~line 4725), mirroring its once-evaluated three-closure shape:

```go
	// 2026-10-22 — Slack channel routes. Absent: one SKIPPED, no calls.
	var routesCfg *appcfg.Config
	if rc, ok := cfg.(interface{ GetSlackRoutesConfig() *appcfg.Config }); ok {
		routesCfg = rc.GetSlackRoutesConfig()
	}
	if routesCfg == nil || len(routesCfg.Slack.Routes) == 0 {
		checks = append(checks, func(ctx context.Context) CheckResult {
			return checkSlackChannelRoutes(ctx, nil, nil, nil)[0]
		})
	} else {
		var chOnce sync.Once
		var chResults []CheckResult
		evalCh := func(ctx context.Context) []CheckResult {
			chOnce.Do(func() {
				chResults = checkSlackChannelRoutes(ctx, routesCfg, deps.SlackChannelLister, ssmStore)
			})
			return chResults
		}
		for i := 0; i < 3; i++ {
			idx := i
			checks = append(checks, func(ctx context.Context) CheckResult { return evalCh(ctx)[idx] })
		}
	}
```
`ssmStore` must be the `SSMParamStore` the Slack token check in the same function already receives; find its name at the `checkSlackTokenValidity` registration and use that identifier. If the doctor's SSM store type does not implement `SSMParamStore` (`Get(ctx, name, withDecryption)`), wrap it in the same way the Slack token check does.

- [ ] **Step 5: Run the tests**

Run: `cd internal/app/cmd && go test . -run 'CheckSlack|Doctor' 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/doctor_slack_routes.go internal/app/cmd/doctor_slack_routes_test.go internal/app/cmd/doctor.go
git commit -m "feat(doctor): Slack channel routes group (profiles, channels, table drift)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 10: Docs

**Files:**
- Modify: `docs/slack-notifications.md` (new section "Channel routes" directly after the "Default route" section; add a row to the "Where to look"-style index if the file has one)
- Modify: `docs/slack-default-route.md` (ladder: add the `table[channel]` rung between peer claim and default route; §11 follow-ups: mark "per-channel routing" done if listed)
- Modify: `OPERATOR-GUIDE.md` (km-config.yaml reference: `slack.routes`)
- Modify: `CLAUDE.md` (one dated bullet at the top of the phase list; "Where to look" table row)

- [ ] **Step 1: `docs/slack-notifications.md` — "Channel routes" section**

Write, with generic placeholders only (`example.com`, `C0AB12CD3`):

```markdown
### Channel routes (2026-10-22)

Map known channels to klankers by name. Unlisted channels keep using
`slack.default_route`, then the Phase 96 behaviour.

```yaml
slack:
  routes:
    - channel: "#security-alerts"      # "#name" or a channel id (C…)
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails: ["*@example.com"]  # optional
      mention_only: false              # optional; default true
    - channel: "#platform-eng"
      alias: platform-bot
      profile: profiles/platform.yaml
  default_route: {alias: ir-bot, profile: profiles/ir-bot.yaml, allow_emails: [ops@example.com]}
```

**Resolution.** `km init` lists the channels the bot is a member of
(`users.conversations`) and matches each route by name or id. A name the bot
cannot see is an error listing the bot's channels, so a renamed channel or a
missing invite fails the init, never routes silently. Ids are stable across
renames. Two routes resolving to one channel is an error. Several routes may
name one alias.

**Who may trigger.** First non-empty wins: the route's `allow_emails`, then
`slack.default_route.allow_emails`, then the route profile's
`notification.slack.inbound.allowEmails`. All empty fails `km init`. The box's
own row list still wins once it exists.

**Mention.** `mention_only: false` lets any allowed sender's top-level post
start a turn; thread follow-ups keep the box's `threadBypass` rule. DMs never
need a mention.

**Precedence.** Owned `#sb-` channel → routed-thread continuity → a peer that
claims the channel → `routes` → `default_route` → Phase 96.

**Transport.** The resolved table is one JSON SSM parameter,
`/{prefix}/slack/routes`; its SHA-256 lands in the bridge env
(`KM_SLACK_ROUTES_SHA256`) so a change cold-starts the Lambda.

**Deploy.** First time: `make build` → `make build-lambdas` →
`km init --dry-run=false`. Every later change to `routes:`: `km init --slack`.
No sandbox recreate. `km doctor` group **Slack channel routes** reports the
profiles, the channel resolution and drift between the config and the live
table.

**Troubleshooting.**
| Symptom | Cause | Fix |
|---|---|---|
| `km init` fails "no channel named #x among the channels the bot is a member of" | bot not invited, or the channel was renamed | invite the bot or update `channel:` |
| mention in a mapped channel goes to the default alias | table not published, or old bridge | `km doctor` (table drift) → `km init --slack`; first deploy needs the full sequence |
| bridge log `channel route table invalid; channel routes OFF` | hand-edited parameter | `km init --slack` rewrites it |
| `km-slack-bridge: channel routes enabled count=N sha256=…` missing after `km init --slack` | the hash did not change (same table) | nothing to do; the table is live |
```

- [ ] **Step 2: `docs/slack-default-route.md`**

In the ladder description (§ with steps 1–6), change step 3 to: "route selection: the channel's own entry in `slack.routes` when the table has one, else `slack.default_route`; neither ⇒ Phase 96". Add a line under the design decisions: "Channel routes (2026-10-22) are a table of default routes keyed by channel id; the ladder is unchanged, only the route it runs against is selected per message. See `docs/slack-notifications.md` § Channel routes."

- [ ] **Step 3: `OPERATOR-GUIDE.md`**

In the `km-config.yaml` reference beside `slack.default_route`, add `slack.routes` with the YAML above (short form) and the deploy line.

- [ ] **Step 4: `CLAUDE.md`**

At the top of the dated bullets (above "Thread session state leaves the sandbox role"), add:

```markdown
**Slack channel routes — map named channels to klankers (2026-10-22):**
- **`slack.routes:` beside `slack.default_route`**: `channel` (`#name` or `C…`), `alias`, `profile`, optional `allow_emails` (else the install list, else the profile's `allowEmails`; all empty fails `km init`), optional `mention_only: false`. `km init` resolves names from the bot's own channel list (`users.conversations`, never `conversations.list`), pre-stages each profile, writes `/{prefix}/slack/routes` (canonical JSON) and exports `KM_SLACK_ROUTES_SHA256` so the bridge cold-starts and re-reads. Ladder unchanged: owned → continuity → peer claim → `routes[channel]` → `default_route` → Phase 96. Several channels may share one alias; a DM ignores the table.
- **Deploy = `make build` → `make build-lambdas` → `km init --dry-run=false` once; then `km init --slack` per change. No recreate.** Half-deploys are quiet: old bridge ignores the parameter; new bridge with no parameter is byte-identical. `km doctor` "Slack channel routes" reports drift. See `docs/slack-notifications.md` § Channel routes.
```

Add to the "Where to look" table: `| Map named Slack channels to specific klankers — slack.routes, name resolution from the bot's channels, the allow-list fall-back, mention_only, the SSM table + hash, km init --slack | docs/slack-notifications.md § Channel routes |`.

- [ ] **Step 5: Commit**

```bash
git add docs/slack-notifications.md docs/slack-default-route.md OPERATOR-GUIDE.md CLAUDE.md
git commit -m "docs: Slack channel routes (slack.routes)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

---

### Task 11: Full suite, build, deploy, live UAT (controller-run, not a subagent)

**Files:** none new. Spec §8–§9.

- [ ] **Step 1: Whole suite**

Run: `go test ./... 2>&1 | grep -E '^(FAIL|ok|---)' | grep -v '^ok' ; echo "exit=$?"`
Expected: no `FAIL` lines beyond the recorded pre-existing `TestBootstrap*`/`TestCluster*`/`TestConfigure*` cases in `internal/app/cmd`.

- [ ] **Step 2: Build and deploy from the main tree**

```bash
make build && make build-lambdas
AWS_PROFILE=klanker-application ./km init --dry-run=false > scratchpad/km-init-routes.log 2>&1   # log to a FILE, never | tail
tail -3 scratchpad/km-init-routes.log
```
Before this, add two routes to `km-config.yaml` under `slack:` (two channels the bot is already a member of; one with `mention_only: false`; aliases `ir-bot` and a second lean inbound profile). Expect the `[info] slack.routes:` lines and the written parameter line in the log.

- [ ] **Step 3: Live UAT (spec §9)**

Use `scratchpad/slack-uat.sh` (synthetic HMAC driver) and real Slack posts:
1. Mention in channel A ⇒ reply in the originating thread from alias A's box.
2. Mention in channel B ⇒ reply from alias B's box (cold-create on first use).
3. Channel B (`mention_only: false`): top-level post without a mention dispatches; the same from a synthetic unlisted user id is dropped (no 👀, no reply; `events: route: sender not allowlisted` in the bridge log).
4. Thread follow-up in channel A without a mention continues on A's box.
5. Mention in an unmapped channel ⇒ `ir-bot` via the default route.
6. Swap B's alias in `km-config.yaml`, `km init --slack`, confirm `channel routes enabled … sha256=` with the new hash in the bridge log, mention in B ⇒ the new alias's box.
7. Rename channel B in Slack, `km init --slack` with the old name ⇒ error listing the bot's channels; update the config ⇒ clean.
8. `km doctor`: green at every step; the table result WARNs between the step-6 edit and its apply.
Record go/no-go in spec §10 "Live UAT" with the sandbox ids and log lines.

- [ ] **Step 4: Rewrite subagent trailers and open the PR**

```bash
git checkout -- VERSION   # filter-branch needs a clean tree
git filter-branch -f --msg-filter 'sed -e "s/Co-Authored-By: Claude .*/Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>/"' main..HEAD
```
Then open the PR with the body ending in the required attribution lines.
