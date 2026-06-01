package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// representative fixtures, each exercising distinct v1alpha1 features:
//   learn_v2        notify + slack + inbound + invites + inlined settings.json + agent.default
//   codex           cli.agent: codex, no inlined settings.json, dead agent block
//   learn_v2_codex  codex default + inlined settings.json + slack inbound
//   locked          iam (no secret paths), second configFiles entry preserved
//   dc34            full notify/email/slack/archive
//   goose           inlined settings.json with trustedDirectories ONLY (no autoApprove)
//   ao              minimal — only identity->iam + dead agent block
//   builtin_hardened a pkg/profile builtin
var fixtures = []string{
	"learn_v2",
	"codex",
	"learn_v2_codex",
	"locked",
	"dc34",
	"goose",
	"ao",
	"builtin_hardened",
}

func loadProfile(t *testing.T, raw []byte) *profile.SandboxProfile {
	t.Helper()
	if errs := profile.Validate(raw); len(errs) != 0 {
		for _, e := range errs {
			t.Errorf("validation error %s: %s", e.Path, e.Message)
		}
		t.Fatalf("migrated profile failed v1alpha2 validation")
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse migrated profile: %v", err)
	}
	return p
}

func TestMigrate(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			oldRaw, err := os.ReadFile(filepath.Join("testdata", "v1alpha1_"+name+".yaml"))
			if err != nil {
				t.Fatalf("read v1alpha1 fixture: %v", err)
			}
			wantRaw, err := os.ReadFile(filepath.Join("testdata", "v1alpha2_"+name+".yaml"))
			if err != nil {
				t.Fatalf("read v1alpha2 fixture: %v", err)
			}

			gotRaw, changed, err := Migrate(oldRaw)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if !changed {
				t.Fatalf("Migrate reported no change on a v1alpha1 input")
			}

			// (a) migrated output parses + passes v1alpha2 validation.
			got := loadProfile(t, gotRaw)
			// the hand-migrated in-repo profile is our ground truth.
			want := loadProfile(t, wantRaw)

			// apiVersion bumped.
			if got.APIVersion != want.APIVersion {
				t.Errorf("apiVersion: got %q want %q", got.APIVersion, want.APIVersion)
			}
			if got.APIVersion != "klankermaker.ai/v1alpha2" {
				t.Errorf("apiVersion not v1alpha2: %q", got.APIVersion)
			}

			// (b) semantic equivalence with the hand-migrated profile.
			assertIAMEqual(t, got, want)
			assertNotificationEqual(t, got, want)
			assertAgentEqual(t, got, want)
			assertClaudeToolsEqual(t, got, want)

			// the old dead agent block + inlined settings.json must be gone.
			if _, ok := got.Spec.Execution.ConfigFiles[settingsJSONKey]; ok {
				t.Errorf("inlined %s was not removed from migrated profile", settingsJSONKey)
			}
		})
	}
}

func assertIAMEqual(t *testing.T, got, want *profile.SandboxProfile) {
	t.Helper()
	g, w := got.Spec.IAM, want.Spec.IAM
	if g.RoleSessionDuration != w.RoleSessionDuration {
		t.Errorf("iam.roleSessionDuration: got %q want %q", g.RoleSessionDuration, w.RoleSessionDuration)
	}
	if !reflect.DeepEqual(sortedCopy(g.AllowedRegions), sortedCopy(w.AllowedRegions)) {
		t.Errorf("iam.allowedRegions: got %v want %v", g.AllowedRegions, w.AllowedRegions)
	}
	if !reflect.DeepEqual(sortedCopy(g.AllowedSecretPaths), sortedCopy(w.AllowedSecretPaths)) {
		t.Errorf("iam.allowedSecretPaths: got %v want %v", g.AllowedSecretPaths, w.AllowedSecretPaths)
	}
}

func assertNotificationEqual(t *testing.T, got, want *profile.SandboxProfile) {
	t.Helper()
	g, w := got.Spec.Notification, want.Spec.Notification
	if (g == nil) != (w == nil) {
		t.Fatalf("notification presence mismatch: got nil=%v want nil=%v", g == nil, w == nil)
	}
	if g == nil {
		return
	}
	// Events
	assertEventsEqual(t, g.Events, w.Events)
	// Email
	if (g.Email == nil) != (w.Email == nil) {
		t.Errorf("notification.email presence mismatch: got nil=%v want nil=%v", g.Email == nil, w.Email == nil)
	} else if g.Email != nil {
		if !boolPtrEq(g.Email.Enabled, w.Email.Enabled) {
			t.Errorf("notification.email.enabled: got %v want %v", g.Email.Enabled, w.Email.Enabled)
		}
		if g.Email.Address != w.Email.Address {
			t.Errorf("notification.email.address: got %q want %q", g.Email.Address, w.Email.Address)
		}
	}
	// Slack
	assertSlackEqual(t, g.Slack, w.Slack)
}

func assertEventsEqual(t *testing.T, g, w *profile.NotificationEventsSpec) {
	t.Helper()
	if (g == nil) != (w == nil) {
		t.Errorf("notification.events presence mismatch: got nil=%v want nil=%v", g == nil, w == nil)
		return
	}
	if g == nil {
		return
	}
	if !boolPtrEq(g.OnPermission, w.OnPermission) {
		t.Errorf("events.onPermission: got %v want %v", g.OnPermission, w.OnPermission)
	}
	if !boolPtrEq(g.OnIdle, w.OnIdle) {
		t.Errorf("events.onIdle: got %v want %v", g.OnIdle, w.OnIdle)
	}
	if !intPtrEq(g.CooldownSeconds, w.CooldownSeconds) {
		t.Errorf("events.cooldownSeconds: got %v want %v", g.CooldownSeconds, w.CooldownSeconds)
	}
}

func assertSlackEqual(t *testing.T, g, w *profile.NotificationSlackSpec) {
	t.Helper()
	if (g == nil) != (w == nil) {
		t.Errorf("notification.slack presence mismatch: got nil=%v want nil=%v", g == nil, w == nil)
		return
	}
	if g == nil {
		return
	}
	if !boolPtrEq(g.Enabled, w.Enabled) {
		t.Errorf("slack.enabled: got %v want %v", g.Enabled, w.Enabled)
	}
	if !boolPtrEq(g.PerSandbox, w.PerSandbox) {
		t.Errorf("slack.perSandbox: got %v want %v", g.PerSandbox, w.PerSandbox)
	}
	if g.ChannelOverride != w.ChannelOverride {
		t.Errorf("slack.channelOverride: got %q want %q", g.ChannelOverride, w.ChannelOverride)
	}
	if !boolPtrEq(g.ArchiveOnDestroy, w.ArchiveOnDestroy) {
		t.Errorf("slack.archiveOnDestroy: got %v want %v", g.ArchiveOnDestroy, w.ArchiveOnDestroy)
	}
	// inbound
	if (g.Inbound == nil) != (w.Inbound == nil) {
		t.Errorf("slack.inbound presence mismatch: got nil=%v want nil=%v", g.Inbound == nil, w.Inbound == nil)
	} else if g.Inbound != nil {
		if !boolPtrEq(g.Inbound.Enabled, w.Inbound.Enabled) {
			t.Errorf("slack.inbound.enabled: got %v want %v", g.Inbound.Enabled, w.Inbound.Enabled)
		}
		if !boolPtrEq(g.Inbound.MentionOnly, w.Inbound.MentionOnly) {
			t.Errorf("slack.inbound.mentionOnly: got %v want %v", g.Inbound.MentionOnly, w.Inbound.MentionOnly)
		}
		if !boolPtrEq(g.Inbound.ReactAlways, w.Inbound.ReactAlways) {
			t.Errorf("slack.inbound.reactAlways: got %v want %v", g.Inbound.ReactAlways, w.Inbound.ReactAlways)
		}
	}
	// transcript
	if (g.Transcript == nil) != (w.Transcript == nil) {
		t.Errorf("slack.transcript presence mismatch: got nil=%v want nil=%v", g.Transcript == nil, w.Transcript == nil)
	} else if g.Transcript != nil && !boolPtrEq(g.Transcript.Enabled, w.Transcript.Enabled) {
		t.Errorf("slack.transcript.enabled: got %v want %v", g.Transcript.Enabled, w.Transcript.Enabled)
	}
	// invites
	if (g.Invites == nil) != (w.Invites == nil) {
		t.Errorf("slack.invites presence mismatch: got nil=%v want nil=%v", g.Invites == nil, w.Invites == nil)
	} else if g.Invites != nil {
		if !reflect.DeepEqual(sortedCopy(g.Invites.Emails), sortedCopy(w.Invites.Emails)) {
			t.Errorf("slack.invites.emails: got %v want %v", g.Invites.Emails, w.Invites.Emails)
		}
		if !boolPtrEq(g.Invites.UseConnect, w.Invites.UseConnect) {
			t.Errorf("slack.invites.useConnect: got %v want %v", g.Invites.UseConnect, w.Invites.UseConnect)
		}
	}
}

func assertAgentEqual(t *testing.T, got, want *profile.SandboxProfile) {
	t.Helper()
	g, w := got.Spec.Agent, want.Spec.Agent
	if (g == nil) != (w == nil) {
		t.Fatalf("agent presence mismatch: got nil=%v want nil=%v", g == nil, w == nil)
	}
	if g == nil {
		return
	}
	// default is "" ~ "claude"; normalize.
	gd, wd := g.Default, w.Default
	if gd == "" {
		gd = "claude"
	}
	if wd == "" {
		wd = "claude"
	}
	if gd != wd {
		t.Errorf("agent.default: got %q want %q", g.Default, w.Default)
	}
	// claude.args
	var ga, wa []string
	if g.Claude != nil {
		ga = g.Claude.Args
	}
	if w.Claude != nil {
		wa = w.Claude.Args
	}
	if !reflect.DeepEqual(ga, wa) {
		t.Errorf("agent.claude.args: got %v want %v", ga, wa)
	}
	// codex.args
	var gc, wc []string
	if g.Codex != nil {
		gc = g.Codex.Args
	}
	if w.Codex != nil {
		wc = w.Codex.Args
	}
	if !reflect.DeepEqual(gc, wc) {
		t.Errorf("agent.codex.args: got %v want %v", gc, wc)
	}
}

// assertClaudeToolsEqual is the SECURITY-CRITICAL check: the migrated profile
// must grant the identical effective Claude tool allow/deny/trustedDirectories
// sets as the hand-migrated profile.
func assertClaudeToolsEqual(t *testing.T, got, want *profile.SandboxProfile) {
	t.Helper()
	var gAllow, gDeny, gTrust []string
	var wAllow, wDeny, wTrust []string
	if got.Spec.Agent != nil && got.Spec.Agent.Claude != nil {
		gAllow = got.Spec.Agent.Claude.Tools.AutoApprove
		gDeny = got.Spec.Agent.Claude.Tools.Deny
		gTrust = got.Spec.Agent.Claude.TrustedDirectories
	}
	if want.Spec.Agent != nil && want.Spec.Agent.Claude != nil {
		wAllow = want.Spec.Agent.Claude.Tools.AutoApprove
		wDeny = want.Spec.Agent.Claude.Tools.Deny
		wTrust = want.Spec.Agent.Claude.TrustedDirectories
	}
	if !reflect.DeepEqual(sortedCopy(gAllow), sortedCopy(wAllow)) {
		t.Errorf("agent.claude.tools.autoApprove set: got %v want %v", gAllow, wAllow)
	}
	if !reflect.DeepEqual(sortedCopy(gDeny), sortedCopy(wDeny)) {
		t.Errorf("agent.claude.tools.deny set: got %v want %v", gDeny, wDeny)
	}
	if !reflect.DeepEqual(sortedCopy(gTrust), sortedCopy(wTrust)) {
		t.Errorf("agent.claude.trustedDirectories set: got %v want %v", gTrust, wTrust)
	}
}

// TestIdempotent: migrating an already-v1alpha2 profile is a no-op pass-through.
func TestIdempotent(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			oldRaw, err := os.ReadFile(filepath.Join("testdata", "v1alpha1_"+name+".yaml"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			once, _, err := Migrate(oldRaw)
			if err != nil {
				t.Fatalf("first migrate: %v", err)
			}
			twice, changed, err := Migrate(once)
			if err != nil {
				t.Fatalf("second migrate: %v", err)
			}
			if changed {
				t.Errorf("second migrate reported changed=true on already-v1alpha2 input")
			}
			if string(once) != string(twice) {
				t.Errorf("migrate is not idempotent: output differs on re-run")
			}
		})
	}
}

// TestRejectsNonProfile verifies a clear error on unrecognizable input.
func TestRejectsNonProfile(t *testing.T) {
	cases := map[string]string{
		"not yaml mapping": "- just\n- a\n- list\n",
		"missing kind":     "apiVersion: klankermaker.ai/v1alpha1\nspec: {}\n",
		"wrong kind":       "apiVersion: klankermaker.ai/v1alpha1\nkind: Pod\nspec: {}\n",
		"unknown version":  "apiVersion: klankermaker.ai/v2\nkind: SandboxProfile\nspec: {}\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Migrate([]byte(in)); err == nil {
				t.Errorf("expected error for %q input, got nil", name)
			}
		})
	}
}

// --- helpers ---

func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func boolPtrEq(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
