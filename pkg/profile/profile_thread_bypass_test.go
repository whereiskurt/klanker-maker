package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"gopkg.in/yaml.v3"
)

const tbHead = "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: t\nspec:\n  notification:\n    slack:\n      enabled: true\n      perSandbox: true\n      inbound:\n        enabled: true\n"

func TestThreadBypass_SchemaAcceptsAndPointerSemantics(t *testing.T) {
	raw := []byte(tbHead + "        mentionOnly: true\n        threadBypass: false\n")
	// The minimal fixture omits required top-level spec sections, so those
	// "missing properties" errors are expected; only errors about the new key
	// (e.g. additionalProperties rejection) are failures.
	for _, e := range profile.ValidateSchema(raw) {
		if !strings.Contains(e.Message, "missing properties") {
			t.Fatalf("schema must accept threadBypass: %+v", e)
		}
	}
	var p profile.SandboxProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	tb := p.Spec.Notification.Slack.Inbound.ThreadBypass
	if tb == nil || *tb {
		t.Fatalf("want &false, got %v", tb)
	}
	var q profile.SandboxProfile
	yaml.Unmarshal([]byte(tbHead), &q)
	if q.Spec.Notification.Slack.Inbound.ThreadBypass != nil {
		t.Fatal("omitted must be nil (bypass on)")
	}
}

func TestThreadBypass_SemanticWarnings(t *testing.T) {
	warnAt := func(errs []profile.ValidationError) string {
		for _, e := range errs {
			if e.Path == "spec.notification.slack.inbound.threadBypass" && e.IsWarning {
				return e.Message
			}
		}
		return ""
	}
	cases := []struct {
		name, inbound, wantSubstr string
		wantWarn                  bool
	}{
		{"strict + mentionOnly true: no warning", "        mentionOnly: true\n        threadBypass: false\n", "", false},
		{"strict + mentionOnly false: no-op warning", "        mentionOnly: false\n        threadBypass: false\n", "has no effect", true},
		{"strict + mentionOnly unset: install-level note", "        threadBypass: false\n", "install-level", true},
		{"bypass true: nothing", "        threadBypass: true\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p profile.SandboxProfile
			if err := yaml.Unmarshal([]byte(tbHead+tc.inbound), &p); err != nil {
				t.Fatal(err)
			}
			got := warnAt(profile.ValidateSemantic(&p))
			if tc.wantWarn && (got == "" || !strings.Contains(got, tc.wantSubstr)) {
				t.Fatalf("want warning containing %q, got %q", tc.wantSubstr, got)
			}
			if !tc.wantWarn && got != "" {
				t.Fatalf("unexpected warning %q", got)
			}
		})
	}
}
