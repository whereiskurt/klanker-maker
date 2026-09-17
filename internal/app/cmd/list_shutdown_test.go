package cmd

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type fakeShutdownLister struct{ records []kmaws.SandboxRecord }

func (f *fakeShutdownLister) ListSandboxes(context.Context, bool) ([]kmaws.SandboxRecord, error) {
	return f.records, nil
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// A sandbox goes away at whichever comes first: TTL expiry or the idle reaper.
// Showing TTL alone is actively misleading — a box with 19h of TTL left can be
// reaped in 2h by the idle timer, which is what the operator actually needs to
// plan around.
func TestShutdownLabel(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	cases := []struct {
		name string
		rec  kmaws.SandboxRecord
		want string
	}{
		{
			name: "idle sooner than ttl -> idle wins and says so",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(19 * time.Hour), IdleRemaining: "2h0m0s remaining",
			},
			want: "2h idle",
		},
		{
			name: "ttl sooner than idle -> ttl wins",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(30 * time.Minute), IdleRemaining: "2h0m0s remaining",
			},
			want: "30m ttl",
		},
		{
			name: "no idle timeout configured -> ttl alone",
			rec:  kmaws.SandboxRecord{Status: "running", TTLExpiry: at(90 * time.Minute)},
			want: "1h30m ttl",
		},
		{
			// A stopped or paused box is not being idle-reaped -- it is already
			// stopped. Only the TTL still runs.
			name: "paused -> idle does not apply",
			rec: kmaws.SandboxRecord{
				Status: "paused", TTLExpiry: at(19 * time.Hour), IdleRemaining: "2h0m0s remaining",
			},
			want: "19h ttl",
		},
		{
			name: "expired ttl",
			rec:  kmaws.SandboxRecord{Status: "running", TTLExpiry: at(-time.Minute)},
			want: "expired",
		},
		{
			name: "idle imminent",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(5 * time.Hour), IdleRemaining: "imminent",
			},
			want: "imminent idle",
		},
		{
			name: "nothing known",
			rec:  kmaws.SandboxRecord{Status: "running"},
			want: "-",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shutdownLabel(tc.rec); got != tc.want {
				t.Errorf("shutdownLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A profile with spec.lifecycle.ttl: 86000h (a "never expire" idiom) used to
// render as "85999h42m ttl" — 14 characters in an 11-wide column, pushing UP
// and AUTH off their headers on every row. Past a day the hours stop being
// information and start being noise: roll to days, and drop the trailing unit
// that is under 1% of the value, the same shape formatUptime already uses.
func TestCompactDuration_LargeValuesRollToDays(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{46 * time.Minute, "46m"},
		{90 * time.Minute, "1h30m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{24 * time.Hour, "1d"},
		{25*time.Hour + 30*time.Minute, "1d1h"}, // minutes dropped past a day
		{6*24*time.Hour + 23*time.Hour, "6d23h"},
		{7 * 24 * time.Hour, "7d"},
		{364 * 24 * time.Hour, "364d"}, // hours dropped past a week
		{365 * 24 * time.Hour, "1y"},
		{400 * 24 * time.Hour, "1y35d"},
		{(2*365 + 364) * 24 * time.Hour, "2y364d"}, // days kept under 3y
		{(3*365 + 200) * 24 * time.Hour, "∞"},      // 3y+ is "effectively never"
		{86000 * time.Hour, "∞"},                   // the "never expire" idiom
		{10 * 365 * 24 * time.Hour, "∞"},
	}
	for _, tc := range cases {
		if got := compactDuration(tc.d); got != tc.want {
			t.Errorf("compactDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
		if got := compactDuration(tc.d); visualWidth(got) > 7 {
			t.Errorf("compactDuration(%v) = %q is %d columns; the SHUTDOWN column is 11 wide and needs room for \" ttl\"", tc.d, got, visualWidth(got))
		}
	}
}

// "∞" is three bytes, one rune, one column. Go's %-11s pads by rune, so the
// SHUTDOWN cell stays aligned as-is — this pins that, so a future switch to a
// byte-counting pad (or a two-column glyph) shows up as a shifted UP column
// rather than as a row that looks off in someone's terminal.
func TestListCmd_InfiniteTTLKeepsColumnsAligned(t *testing.T) {
	forever := time.Now().Add(86000 * time.Hour)
	soon := time.Now().Add(19 * time.Hour)
	lister := &fakeShutdownLister{records: []kmaws.SandboxRecord{
		{SandboxID: "always-f51f581b", Alias: "kphtest2", Profile: "always.ir", Status: "paused", TTLExpiry: &forever, CreatedAt: time.Now().Add(-30 * 24 * time.Hour)},
		{SandboxID: "learn-ccd011a0", Alias: "learn1", Profile: "learn", Status: "running", TTLExpiry: &soon, CreatedAt: time.Now().Add(-2 * time.Hour)},
	}}
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	root.AddCommand(NewListCmdWithLister(cfg, lister))
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("list: %v\n%s", err, buf.String())
	}
	out := stripANSI(buf.String())
	var header, inf, fin string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "SHUTDOWN"):
			header = l
		case strings.Contains(l, "∞ ttl"):
			inf = l
		case strings.Contains(l, "19h ttl"):
			fin = l
		}
	}
	if header == "" || inf == "" || fin == "" {
		t.Fatalf("missing rows:\n%s", out)
	}
	// Column where the UP cell begins = visual width of everything before the
	// SHUTDOWN cell plus the padded cell and its separator.
	upStart := func(l, cell string) int {
		i := strings.Index(l, cell)
		rest := l[i+len(cell):]
		return visualWidth(l[:i]) + visualWidth(cell) + len(rest) - len(strings.TrimLeft(rest, " "))
	}
	upHeader := visualWidth(header[:strings.Index(header, "UP")])
	if got := upStart(inf, "∞ ttl"); got != upHeader {
		t.Errorf("UP cell on the ∞ row starts at column %d, header at %d:\n%s", got, upHeader, out)
	}
	if got := upStart(fin, "19h ttl"); got != upHeader {
		t.Errorf("UP cell on the 19h row starts at column %d, header at %d:\n%s", got, upHeader, out)
	}
}

// --wide's TTL column shows "∞" on the same 3y rung; --json keeps the numeric
// TTLRemaining string so nothing machine-readable changes shape.
func TestListCmd_WideShowsInfinityButJSONStaysNumeric(t *testing.T) {
	forever := time.Now().Add(86000 * time.Hour)
	rec := kmaws.SandboxRecord{SandboxID: "always-f51f581b", Alias: "kphtest2", Profile: "always.ir", Status: "paused",
		TTLExpiry: &forever, TTLRemaining: "9y", CreatedAt: time.Now()}
	run := func(args ...string) string {
		root := &cobra.Command{Use: "km"}
		root.AddCommand(NewListCmdWithLister(&config.Config{}, &fakeShutdownLister{records: []kmaws.SandboxRecord{rec}}))
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(append([]string{"list"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("list %v: %v\n%s", args, err, buf.String())
		}
		return stripANSI(buf.String())
	}
	if out := run("--wide"); !strings.Contains(out, " ∞ ") {
		t.Errorf("--wide TTL column should show ∞:\n%s", out)
	}
	if out := run("--json"); !strings.Contains(out, `"ttl_remaining":"9y"`) || strings.Contains(out, "∞") {
		t.Errorf("--json must keep the numeric ttl_remaining:\n%s", out)
	}
}
