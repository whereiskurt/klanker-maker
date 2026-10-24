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

// RoutesLoaded is true only when the table was read: parsed, or legitimately
// absent (no routes configured). An unreadable or invalid parameter is false,
// so BackfillStep retries rather than ending in-flight backfills.
func TestWireChannelRoutes_RoutesLoaded(t *testing.T) {
	cases := []struct {
		name string
		p    *fakeParam
		want bool
	}{
		{"parsed", &fakeParam{value: `{"v":1,"routes":[{"channel":"C1","alias":"a","profile":"p","allow":["*@example.com"],"mention_only":true}]}`}, true},
		{"parsed empty", &fakeParam{value: `{"v":1,"routes":[]}`}, true},
		{"absent", &fakeParam{err: &ssmtypes.ParameterNotFound{}}, true},
		{"unreadable", &fakeParam{err: errors.New("boom")}, false},
		{"bad version", &fakeParam{value: `{"v":9,"routes":[]}`}, false},
		{"not json", &fakeParam{value: `not json`}, false},
	}
	for _, c := range cases {
		h := &bridge.EventsHandler{}
		wireChannelRoutes(context.Background(), h, c.p, "/km/slack/routes", defaultRouteDeps{})
		if h.RoutesLoaded != c.want {
			t.Errorf("%s: RoutesLoaded = %v, want %v", c.name, h.RoutesLoaded, c.want)
		}
	}
}
