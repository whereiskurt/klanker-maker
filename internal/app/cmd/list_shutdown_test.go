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

// computeTTLRemaining's "expired" is seven characters in a %-6s TTL column, so
// the one expired row pushed IDLE, UP and 💬 a column to the right of every
// other row's. The wide table renders "exp."; --json keeps "expired" and the
// narrow SHUTDOWN column (11 wide) is untouched.
func TestListCmd_WideExpiredTTLStaysInColumn(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	soon := time.Now().Add(400 * 24 * time.Hour)
	recs := []kmaws.SandboxRecord{
		{SandboxID: "learn-cf07fb24", Alias: "ezra-v1", Profile: "learn", Region: "us-east-1", Status: "stopped", TTLExpiry: &past, TTLRemaining: "expired", CreatedAt: time.Now()},
		{SandboxID: "always-11dbf1f5", Alias: "hackerone-v1", Profile: "hackerone.v1", Region: "us-east-1", Status: "running", TTLExpiry: &soon, TTLRemaining: "400d", IdleRemaining: "4h0m0s remaining", CreatedAt: time.Now()},
	}
	run := func(args ...string) string {
		root := &cobra.Command{Use: "km"}
		root.AddCommand(NewListCmdWithLister(&config.Config{}, &fakeShutdownLister{records: recs}))
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(append([]string{"list"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("list %v: %v\n%s", args, err, buf.String())
		}
		return buf.String()
	}
	out := stripANSI(run("--wide"))
	if strings.Contains(out, "expired") {
		t.Errorf("wide table must render exp., not expired:\n%s", out)
	}
	var header, expiredRow, normalRow string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "SANDBOX ID"):
			header = l
		case strings.Contains(l, "ezra-v1"):
			expiredRow = l
		case strings.Contains(l, "hackerone-v1"):
			normalRow = l
		}
	}
	if header == "" || expiredRow == "" || normalRow == "" {
		t.Fatalf("missing lines in:\n%s", out)
	}
	// Rune column of a cell. The TTL/IDLE cells sit after the STATUS icon;
	// the icons in these two rows (⏹ + VS16, 🟢) are each padded by visual
	// width to the same 10 columns, so rune offsets past STATUS differ by a
	// constant per row — compare each row's IDLE against its own TTL instead
	// of against the header.
	col := func(line, cell string) int {
		i := strings.Index(line, cell)
		if i < 0 {
			t.Fatalf("no %q in %q", cell, line)
		}
		return len([]rune(line[:i]))
	}
	// The TTL cell is %-6s + one separator: IDLE begins 7 runes after TTL on
	// EVERY row. "expired" (7 runes) broke that; "exp." keeps it.
	const ttlToIdle = 7
	if got := col(normalRow, "4h") - col(normalRow, "400d"); got != ttlToIdle {
		t.Errorf("normal row: IDLE is %d runes after TTL, want %d\n%s", got, ttlToIdle, out)
	}
	expIdle := strings.Index(expiredRow[strings.Index(expiredRow, "exp.")+len("exp."):], "-")
	if got := len("exp.") + expIdle; got != ttlToIdle {
		t.Errorf("expired row: IDLE is %d runes after TTL, want %d\n%s", got, ttlToIdle, out)
	}
	if j := run("--json"); !strings.Contains(j, `"ttl_remaining":"expired"`) {
		t.Errorf("--json must keep ttl_remaining=expired:\n%s", j)
	}
}

// The IDLE column (--wide) and km status printed IdleRemaining raw — Go's
// Duration.String(), so a large idleTimeout read "86000h0m0s". The stored
// string must stay a parseable duration (shutdownLabel re-parses it, and it
// reaches --json), so the fix is at the display: the same ladder as TTL,
// ∞ from three years.
func TestListCmd_WideIdleColumnUsesTheLadder(t *testing.T) {
	soon := time.Now().Add(400 * 24 * time.Hour)
	recs := []kmaws.SandboxRecord{
		{SandboxID: "a-000000001", Alias: "huge", Status: "running", TTLExpiry: &soon, IdleRemaining: "86000h0m0s remaining", CreatedAt: time.Now()},
		{SandboxID: "a-000000002", Alias: "days", Status: "running", TTLExpiry: &soon, IdleRemaining: "167h0m0s remaining", CreatedAt: time.Now()},
		{SandboxID: "a-000000003", Alias: "soon", Status: "running", TTLExpiry: &soon, IdleRemaining: "23m10s remaining", CreatedAt: time.Now()},
		{SandboxID: "a-000000004", Alias: "now", Status: "running", TTLExpiry: &soon, IdleRemaining: "imminent", CreatedAt: time.Now()},
	}
	root := &cobra.Command{Use: "km"}
	root.AddCommand(NewListCmdWithLister(&config.Config{}, &fakeShutdownLister{records: recs}))
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"list", "--wide"})
	if err := root.Execute(); err != nil {
		t.Fatalf("list --wide: %v\n%s", err, buf.String())
	}
	out := stripANSI(buf.String())
	for alias, want := range map[string]string{"huge": " ∞ ", "days": " 6d23h ", "soon": " 23m ", "now": " imminent "} {
		var line string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, alias) {
				line = l
			}
		}
		if !strings.Contains(line, want) {
			t.Errorf("%s: IDLE cell should be %q; row:\n%s", alias, strings.TrimSpace(want), line)
		}
	}
	if strings.Contains(out, "0m0s") {
		t.Errorf("raw Duration.String() leaked into --wide:\n%s", out)
	}
	// --json keeps the parseable string.
	root = &cobra.Command{Use: "km"}
	root.AddCommand(NewListCmdWithLister(&config.Config{}, &fakeShutdownLister{records: recs[:1]}))
	buf.Reset()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"list", "--json"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), `"idle_remaining":"86000h0m0s remaining"`) {
		t.Errorf("--json must keep the parseable idle_remaining:\n%s", buf.String())
	}
}

func TestIdleLabelForDisplay(t *testing.T) {
	for in, want := range map[string]string{
		"86000h0m0s remaining": "∞",
		"167h0m0s remaining":   "6d23h",
		"2h0m0s remaining":     "2h",
		"23m10s remaining":     "23m",
		"45s remaining":        "<1m",
		"imminent":             "imminent",
		"":                     "-",
		"garbage":              "garbage", // unparseable: show what we have
	} {
		if got := idleLabelForDisplay(in); got != want {
			t.Errorf("idleLabelForDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}

// km status's Idle line: the ladder, with the urgency colour kept.
func TestIdleStatusForDisplay(t *testing.T) {
	cases := map[string]string{
		"86000h0m0s remaining":                         "∞ remaining",
		ansiGreen + "86000h0m0s remaining" + ansiReset: ansiGreen + "∞ remaining" + ansiReset,
		ansiRed + "3m2s remaining" + ansiReset:         ansiRed + "3m remaining" + ansiReset,
		"imminent":                                     "imminent",
		ansiRed + "imminent" + ansiReset:               ansiRed + "imminent" + ansiReset,
		"":                                             "",
	}
	for in, want := range cases {
		if got := idleStatusForDisplay(in); got != want {
			t.Errorf("idleStatusForDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}
