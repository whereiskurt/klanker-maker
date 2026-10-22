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
