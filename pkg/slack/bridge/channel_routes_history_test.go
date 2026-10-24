package bridge

import (
	"strings"
	"testing"
)

func TestCanonicalRoutesDocument_HistoryLessIsByteIdenticalToLegacy(t *testing.T) {
	routes := []RouteEntry{{Channel: "C1", Name: "sec", Alias: "a", Profile: "p", Allow: []string{"x@example.com"}, MentionOnly: true}}
	legacy, err := CanonicalRoutesJSON(routes)
	if err != nil {
		t.Fatal(err)
	}
	withNil, err := CanonicalRoutesDocument(routes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacy) != string(withNil) {
		t.Fatalf("a document with no patterns and no history must not change bytes:\n%s\n%s", legacy, withNil)
	}
	for _, f := range []string{`"pattern"`, `"history"`, `"patterns"`} {
		if strings.Contains(string(withNil), f) {
			t.Errorf("zero-valued %s serialised: %s", f, withNil)
		}
	}
}

func TestCanonicalRoutesDocument_PatternsSortedAndRoundTrip(t *testing.T) {
	h := &RouteHistory{Query: true, Mirror: true, BackfillSeconds: 604800, RetentionSeconds: 604800, MaxMessages: 200}
	pats := []PatternEntry{
		{Glob: "sec-*", Alias: "sec", Profile: "p", Allow: []string{"x@example.com"}, MentionOnly: true, History: &RouteHistory{Query: true}},
		{Glob: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"b@example.com", "a@example.com"}, MentionOnly: true, History: h},
	}
	routes := []RouteEntry{{Channel: "C9", Name: "inc_1", Pattern: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, MentionOnly: true, History: h}}
	b, err := CanonicalRoutesDocument(routes, pats)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Index(s, `"glob":"sec-*"`) > strings.Index(s, `"glob":"inc_*"`) {
		t.Errorf("patterns must keep config order: %s", s)
	}
	if !strings.Contains(s, `"allow":["a@example.com","b@example.com"]`) {
		t.Errorf("pattern allow lists must be sorted: %s", s)
	}
	doc, err := ParseRoutesDocument(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Patterns) != 2 || doc.Patterns[0].Glob != "sec-*" || doc.Patterns[1].Glob != "inc_*" || doc.Patterns[1].History.MaxMessages != 200 {
		t.Fatalf("round trip lost patterns: %+v", doc.Patterns)
	}
	if doc.Routes[0].Pattern != "inc_*" || doc.Routes[0].History == nil || !doc.Routes[0].History.Mirror {
		t.Fatalf("round trip lost route history: %+v", doc.Routes[0])
	}
}

func TestCanonicalRoutesDocument_SpecificGlobStaysAheadOfGeneral(t *testing.T) {
	pats := []PatternEntry{
		{Glob: "inc_sec_*", Alias: "a", Profile: "p", Allow: []string{"x@example.com"}},
		{Glob: "inc_*", Alias: "b", Profile: "p", Allow: []string{"x@example.com"}},
	}
	b, err := CanonicalRoutesDocument(nil, pats)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseRoutesDocument(b)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Patterns[0].Glob != "inc_sec_*" || doc.Patterns[1].Glob != "inc_*" {
		t.Fatalf("order not preserved: %+v", doc.Patterns)
	}
}

func TestParseRoutesDocument_RejectsBadPattern(t *testing.T) {
	for _, body := range []string{
		`{"v":1,"routes":[],"patterns":[{"glob":"","alias":"a","profile":"p","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[],"patterns":[{"glob":"inc_*","alias":"","profile":"p","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[],"patterns":[{"glob":"inc_*","alias":"a","profile":"p","allow":[]}]}`,
		`{"v":1,"routes":[],"patterns":[{"glob":"inc_*","alias":"a","profile":"p","allow":["x@example.com"],"history":{"mirror":true}}]}`,
		`{"v":1,"routes":[],"patterns":[{"glob":"inc_[a]","alias":"a","profile":"p","allow":["x@example.com"]}]}`,
		`{"v":1,"routes":[],"patterns":[{"glob":"plain","alias":"a","profile":"p","allow":["x@example.com"]}]}`,
	} {
		if _, err := ParseRoutesDocument([]byte(body)); err == nil {
			t.Errorf("want error for %s", body)
		}
	}
}

func TestMatchChannelGlob(t *testing.T) {
	yes := [][2]string{{"inc_*", "inc_2026_10_24"}, {"inc_*", "inc_"}, {"sec-?", "sec-1"}, {"*", "anything"}, {"INC_*", "inc_x"}, {"inc_[a-c]*", "inc_[a-c]x"}}
	no := [][2]string{{"inc_*", "incident"}, {"sec-?", "sec-12"}, {"inc_*", "xinc_1"}, {"inc_*", ""}, {"inc_[a-c]*", "inc_a"}}
	for _, c := range yes {
		if !MatchChannelGlob(c[0], c[1]) {
			t.Errorf("MatchChannelGlob(%q,%q) = false", c[0], c[1])
		}
	}
	for _, c := range no {
		if MatchChannelGlob(c[0], c[1]) {
			t.Errorf("MatchChannelGlob(%q,%q) = true", c[0], c[1])
		}
	}
}

func TestBuildRouteTable_CarriesHistoryAndPattern(t *testing.T) {
	h := &RouteHistory{Query: true, Mirror: true, MaxMessages: 50, RetentionSeconds: 3600}
	doc := &RoutesDocument{V: 1,
		Routes:   []RouteEntry{{Channel: "C1", Name: "inc_1", Pattern: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"x@example.com"}, MentionOnly: true, History: h}},
		Patterns: []PatternEntry{{Glob: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"x@example.com"}, MentionOnly: true, History: h}},
	}
	table := BuildRouteTable(doc, func(string, ...any) {})
	r := table["C1"]
	if r == nil || r.History == nil || !r.History.Mirror || r.History.MaxMessages != 50 || r.Pattern != "inc_*" {
		t.Fatalf("table entry = %+v", r)
	}
	pats := BuildPatternTable(doc, func(string, ...any) {})
	if len(pats) != 1 || pats[0].Glob != "inc_*" || pats[0].Route == nil || pats[0].Route.History == nil {
		t.Fatalf("pattern table = %+v", pats)
	}
	e := RouteFromPattern(pats[0], "C7", "inc_7")
	if e.Channel != "C7" || e.Name != "inc_7" || e.Pattern != "inc_*" || e.Alias != "ir" || e.History == nil || e.History.MaxMessages != 50 {
		t.Fatalf("RouteFromPattern = %+v", e)
	}
}
