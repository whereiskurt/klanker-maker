package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

const aeHead = "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: t\nspec:\n  notification:\n    slack:\n      enabled: true\n      perSandbox: true\n      inbound:\n"

// aeErrsAt returns the non-"missing properties" errors whose Path is exactly path.
// The minimal fixture omits required top-level spec sections, so those errors
// are expected and ignored.
func aeErrsAt(errs []profile.ValidationError, path string, warning bool) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range errs {
		if e.Path == path && e.IsWarning == warning {
			out = append(out, e)
		}
	}
	return out
}

func aeUnexpected(errs []profile.ValidationError) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range errs {
		if e.IsWarning || strings.Contains(e.Message, "missing properties") {
			continue
		}
		out = append(out, e)
	}
	return out
}

const aePath = "spec.notification.slack.inbound.allowEmails"
const rqPath = "spec.notification.slack.inbound.reactQuietSeconds"

func TestAllowEmails_ValidThroughYAML(t *testing.T) {
	raw := []byte(aeHead + "        enabled: true\n        allowEmails: [kurt@example.com, \"*@example.com\"]\n        reactQuietSeconds: 180\n")
	if bad := aeUnexpected(profile.Validate(raw)); len(bad) > 0 {
		t.Fatalf("valid allowEmails/reactQuietSeconds rejected: %+v", bad)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	in := p.Spec.Notification.Slack.Inbound
	if len(in.AllowEmails) != 2 || in.AllowEmails[1] != "*@example.com" {
		t.Fatalf("AllowEmails = %v", in.AllowEmails)
	}
	if in.ReactQuietSeconds == nil || *in.ReactQuietSeconds != 180 {
		t.Fatalf("ReactQuietSeconds = %v", in.ReactQuietSeconds)
	}
	q, err := profile.Parse([]byte(aeHead + "        enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Spec.Notification.Slack.Inbound.AllowEmails != nil || q.Spec.Notification.Slack.Inbound.ReactQuietSeconds != nil {
		t.Fatal("omitted fields must be nil")
	}
}

func TestAllowEmails_EmptyListIsAnErrorThatSaysOmit(t *testing.T) {
	raw := []byte(aeHead + "        enabled: true\n        allowEmails: []\n")
	errs := profile.Validate(raw)
	got := aeErrsAt(errs, aePath, false)
	if len(got) == 0 {
		t.Fatalf("want error at %s, got %+v", aePath, errs)
	}
	found := false
	for _, e := range got {
		if strings.Contains(e.Message, "omit the field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a message containing \"omit the field\", got %+v", got)
	}
}

func TestAllowEmails_BadEntryIsAnError(t *testing.T) {
	raw := []byte(aeHead + "        enabled: true\n        allowEmails: [\"k*@x.com\"]\n")
	errs := profile.Validate(raw)
	if len(aeErrsAt(errs, aePath, false)) == 0 && len(aeErrsAt(errs, aePath+"[0]", false)) == 0 {
		t.Fatalf("want error at %s, got %+v", aePath, errs)
	}
}

func TestReactQuietSeconds_NegativeIsAnError(t *testing.T) {
	raw := []byte(aeHead + "        enabled: true\n        reactQuietSeconds: -1\n")
	errs := profile.ValidateSchema(raw)
	if len(aeErrsAt(errs, rqPath, false)) == 0 {
		t.Fatalf("want schema error at %s, got %+v", rqPath, errs)
	}
	// 0 is allowed (minimum 0).
	raw0 := []byte(aeHead + "        enabled: true\n        reactQuietSeconds: 0\n")
	if bad := aeUnexpected(profile.Validate(raw0)); len(bad) > 0 {
		t.Fatalf("0 must be valid: %+v", bad)
	}
}

func TestAllowEmailsAndQuiet_WarnWithoutInboundEnabled(t *testing.T) {
	cases := []struct {
		name, inbound, path string
	}{
		{"allowEmails", "        allowEmails: [kurt@example.com]\n", aePath},
		{"reactQuietSeconds", "        reactQuietSeconds: 180\n", rqPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.Validate([]byte(aeHead + tc.inbound))
			if len(aeErrsAt(errs, tc.path, true)) == 0 {
				t.Fatalf("want warning at %s, got %+v", tc.path, errs)
			}
			// With inbound.enabled the warning is gone.
			errs = profile.Validate([]byte(aeHead + "        enabled: true\n" + tc.inbound))
			if w := aeErrsAt(errs, tc.path, true); len(w) > 0 {
				t.Fatalf("unexpected warning: %+v", w)
			}
		})
	}
}

// TestValidateInboundEmailEntry_MirrorsBridge mirrors pkg/slack/bridge's
// emailEntryCases (and internal/app/config's TestValidateRouteEmailEntry_MirrorsBridge)
// verbatim: pkg/profile cannot import the bridge, so the duplicated regexes
// are held in lockstep by this shared table. Change all three together.
func TestValidateInboundEmailEntry_MirrorsBridge(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"kurt@example.com", true},
		{"Kurt@Example.COM", true},
		{" *@example.com ", true},
		{"*@corp.example", true},
		{"*", false},
		{"*@", false},
		{"*@*", false},
		{"k*@example.com", false},
		{"*@example", false},
		{"kurt@", false},
		{"@example.com", false},
		{"*@exa mple.com", false},
		{"nope", false},
		{"x@y.z", false},
		{"*@.example.com", false},
		{"*@-.com", false},
		{"*@a..com", false},
	}
	for _, tc := range cases {
		p, err := profile.Parse([]byte(aeHead + "        enabled: true\n"))
		if err != nil {
			t.Fatal(err)
		}
		p.Spec.Notification.Slack.Inbound.AllowEmails = []string{tc.in}
		bad := aeErrsAt(profile.ValidateSemantic(p), aePath, false)
		if (len(bad) == 0) != tc.ok {
			t.Errorf("%q: ok=%v, errs=%+v", tc.in, tc.ok, bad)
		}
	}
}
