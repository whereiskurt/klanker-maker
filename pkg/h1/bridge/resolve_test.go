package bridge_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// TestResolve covers the handle→multi-target mapping, the no-match drop, the
// dormant-events case, and the alias-default derivation (the HackerOne analog of
// pkg/github/bridge resolve_test, adapted for multi-target fanout).
func TestResolve(t *testing.T) {
	entries := []bridge.ProgramEntry{
		{
			Handle: "acme-corp",
			Targets: []bridge.Target{
				{Alias: "h1-acme-triage", Profile: "h1-triage"},
				{Alias: "h1-acme-dupe", Profile: "h1-triage"},
			},
			Allow:  []string{"alice", "bob"},
			Events: map[string]bridge.EventEntry{"report_created": {Prompt: "triage {{report_id}}"}},
			Commands: map[string]bridge.CommandEntry{
				"dupe": {Description: "dup check", Prompt: "find dupes of {{args}}"},
			},
		},
		{
			// Program with NO events => auto-triage dormant; commands still present.
			Handle:   "dormant-prog",
			Targets:  []bridge.Target{{Alias: "h1-dormant", Profile: "h1-triage"}},
			Allow:    []string{"carol"},
			Commands: map[string]bridge.CommandEntry{"x": {Prompt: "do x"}},
		},
		{
			// Target with no Alias => defaults to "h1-{handle}".
			Handle:  "no-alias-prog",
			Targets: []bridge.Target{{Profile: "explicit-profile"}},
			Allow:   []string{"dave"},
		},
	}
	const defaultProfile = "h1-default"

	t.Run("known handle returns multi-target config", func(t *testing.T) {
		targets, allow, events, commands, ok := bridge.Resolve("acme-corp", entries, defaultProfile)
		if !ok {
			t.Fatal("Resolve(acme-corp) ok=false, want true")
		}
		if len(targets) != 2 {
			t.Fatalf("targets len=%d, want 2", len(targets))
		}
		if targets[0].Alias != "h1-acme-triage" || targets[1].Alias != "h1-acme-dupe" {
			t.Errorf("targets aliases=%+v", targets)
		}
		if len(allow) != 2 || allow[0] != "alice" || allow[1] != "bob" {
			t.Errorf("allow=%v want [alice bob]", allow)
		}
		if _, has := events["report_created"]; !has {
			t.Errorf("events missing report_created: %+v", events)
		}
		if _, has := commands["dupe"]; !has {
			t.Errorf("commands missing dupe: %+v", commands)
		}
	})

	t.Run("unknown handle drops", func(t *testing.T) {
		_, _, _, _, ok := bridge.Resolve("nope", entries, defaultProfile)
		if ok {
			t.Error("Resolve(nope) ok=true, want false (handler 200-drops)")
		}
	})

	t.Run("empty events => auto-triage dormant, commands present", func(t *testing.T) {
		_, _, events, commands, ok := bridge.Resolve("dormant-prog", entries, defaultProfile)
		if !ok {
			t.Fatal("Resolve(dormant-prog) ok=false, want true")
		}
		if len(events) != 0 {
			t.Errorf("events=%+v, want empty (dormant)", events)
		}
		if _, has := commands["x"]; !has {
			t.Errorf("commands missing x: %+v", commands)
		}
	})

	t.Run("target alias defaults to h1-{handle}", func(t *testing.T) {
		targets, _, _, _, ok := bridge.Resolve("no-alias-prog", entries, defaultProfile)
		if !ok {
			t.Fatal("Resolve(no-alias-prog) ok=false, want true")
		}
		if len(targets) != 1 {
			t.Fatalf("targets len=%d, want 1", len(targets))
		}
		if targets[0].Alias != "h1-no-alias-prog" {
			t.Errorf("alias=%q, want h1-no-alias-prog (defaulted)", targets[0].Alias)
		}
		// Explicit profile preserved.
		if targets[0].Profile != "explicit-profile" {
			t.Errorf("profile=%q, want explicit-profile", targets[0].Profile)
		}
	})

	t.Run("target profile defaults to defaultProfile", func(t *testing.T) {
		entriesWithEmptyProfile := []bridge.ProgramEntry{
			{Handle: "p", Targets: []bridge.Target{{Alias: "a"}}},
		}
		targets, _, _, _, ok := bridge.Resolve("p", entriesWithEmptyProfile, "fallback-prof")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if targets[0].Profile != "fallback-prof" {
			t.Errorf("profile=%q, want fallback-prof (defaulted)", targets[0].Profile)
		}
	})
}

// TestResolve_EmptyEntries verifies no-match when entries is nil.
func TestResolve_EmptyEntries(t *testing.T) {
	_, _, _, _, ok := bridge.Resolve("acme-corp", nil, "default")
	if ok {
		t.Error("expected no match for empty entries")
	}
}

// TestContainsHandle verifies the literal handle substring match used to gate the
// comment-keyword trigger (HackerOne has no bot user to @-mention).
func TestContainsHandle(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		handle  string
		want    bool
	}{
		{"present", "hey @km please triage this", "@km", true},
		{"absent", "just a normal comment", "@km", false},
		{"case-insensitive", "Hey @KM look here", "@km", true},
		{"per-program override handle", "ping @acmebot now", "@acmebot", true},
		{"override absent", "ping @km now", "@acmebot", false},
		{"empty handle never matches", "anything", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridge.ContainsHandle(tc.body, tc.handle); got != tc.want {
				t.Errorf("ContainsHandle(%q, %q)=%v want %v", tc.body, tc.handle, got, tc.want)
			}
		})
	}
}

// TestExtractBody verifies the prompt is the comment minus the handle token
// (mirror of ExtractMentionBody).
func TestExtractBody(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		handle string
		want   string
	}{
		{"strips handle and trims", "@km triage this report", "@km", "triage this report"},
		{"case-insensitive strip", "@KM  do the thing  ", "@km", "do the thing"},
		{"no handle returns full trimmed", "  just text  ", "@km", "just text"},
		{"text before handle dropped", "please @km triage now", "@km", "triage now"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridge.ExtractBody(tc.body, tc.handle); got != tc.want {
				t.Errorf("ExtractBody(%q, %q)=%q want %q", tc.body, tc.handle, got, tc.want)
			}
		})
	}
}
