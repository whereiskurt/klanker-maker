package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// slackTSAgo renders a Slack ts ("seconds.micros") d before now.
func slackTSAgo(now time.Time, d time.Duration) string {
	return fmt.Sprintf("%d.000100", now.Add(-d).Unix())
}

// quietHandler: owned channel C1, an engaged thread C1|1.0, ReactAlways on,
// install quiet period 180 s, logs captured at Info.
func quietHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeSandboxes, *fakeReactor, *bytes.Buffer) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, reactor := newHandler(now)
	h.ReactAlways = true
	h.ReactQuietSeconds = 180
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	var logs bytes.Buffer
	h.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return h, sqs, threads, sandboxes, reactor, &logs
}

func threadReply(t *testing.T, now time.Time, eventID string) EventsRequest {
	t.Helper()
	return strictEvent(t, now, "follow-up", fmt.Sprintf("%d.000500", now.Unix()), "1.0", "U0KURT", eventID)
}

func TestReactQuiet_ThreadReply_Window(t *testing.T) {
	// Pin the sub-second part: slackTSAgo truncates to the second and adds 100 µs,
	// so a now in the first 100 µs of a second would read 59 s, not 60.
	now := time.Unix(time.Now().Unix(), 500_000_000)
	cases := []struct {
		name      string
		boundary  string // "" = no row
		err       error
		wantReact bool
	}{
		{"boundary 60s ago → skipped", slackTSAgo(now, 60*time.Second), nil, false},
		{"boundary 300s ago → reacts", slackTSAgo(now, 300*time.Second), nil, true},
		{"no row → reacts", "", nil, true},
		{"boundary read error → reacts", "", errors.New("ddb throttled"), true},
		{"unparseable boundary → reacts", "not-a-ts", nil, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, threads, _, reactor, logs := quietHandler(t, now)
			if tc.boundary != "" {
				threads.boundary = map[string]string{"C1|1.0": tc.boundary}
			}
			threads.boundaryErr = tc.err
			h.Handle(context.Background(), threadReply(t, now, fmt.Sprintf("QW-%d", i)))
			if len(sqs.sends) != 1 {
				t.Fatalf("the turn must dispatch regardless; sends=%d", len(sqs.sends))
			}
			if got := len(reactor.snapshot()) == 1; got != tc.wantReact {
				t.Fatalf("react=%v want %v; reactions=%+v", got, tc.wantReact, reactor.snapshot())
			}
			skipped := strings.Contains(logs.String(), "events: reaction skipped (quiet period)")
			if skipped == tc.wantReact {
				t.Fatalf("skip log present=%v, want %v; logs:\n%s", skipped, !tc.wantReact, logs.String())
			}
			if skipped {
				for _, k := range []string{"channel=C1", "thread_ts=1.0", "sandbox=sb-abc123", "since_last_dispatch_s=60", "quiet_s=180"} {
					if !strings.Contains(logs.String(), k) {
						t.Fatalf("skip log missing %q:\n%s", k, logs.String())
					}
				}
			}
			if threads.boundaryCalls != 1 {
				t.Fatalf("DispatchBoundary calls=%d, want 1", threads.boundaryCalls)
			}
		})
	}
}

// The real store advances last_dispatch_ts to THIS message on delivery, before
// the reaction step. The window must be measured from the PREVIOUS dispatch.
func TestReactQuiet_MeasuredFromPreviousDispatch_NotThisOne(t *testing.T) {
	now := time.Now()
	h, _, threads, _, reactor, _ := quietHandler(t, now)
	threads.markAdvancesBoundary = true
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, 300*time.Second)}
	h.Handle(context.Background(), threadReply(t, now, "QP-1"))
	if len(threads.marks) != 1 {
		t.Fatalf("marks=%+v", threads.marks)
	}
	if len(reactor.snapshot()) != 1 {
		t.Fatal("a reply 300 s after the previous dispatch must react; the boundary was read after this message advanced it")
	}
}

func TestReactQuiet_TopLevelAlwaysReacts(t *testing.T) {
	now := time.Now()
	h, _, threads, _, reactor, _ := quietHandler(t, now)
	ts := fmt.Sprintf("%d.000500", now.Unix())
	threads.boundary = map[string]string{"C1|" + ts: slackTSAgo(now, time.Second)}
	h.Handle(context.Background(), strictEvent(t, now, "new topic", ts, "", "U0KURT", "QT-1"))
	if len(reactor.snapshot()) != 1 {
		t.Fatal("a top-level message always reacts")
	}
	if threads.boundaryCalls != 0 {
		t.Fatalf("top-level must not read the boundary; calls=%d", threads.boundaryCalls)
	}
}

func TestReactQuiet_ReactAlwaysFalseWins(t *testing.T) {
	now := time.Now()
	off := false
	on := true

	h, _, threads, _, reactor, _ := quietHandler(t, now)
	h.ReactAlways = false
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, time.Hour)}
	h.Handle(context.Background(), threadReply(t, now, "QF-1"))
	if len(reactor.snapshot()) != 0 {
		t.Fatal("install react_always=false silences a thread reply regardless of the window")
	}
	if threads.boundaryCalls != 0 {
		t.Fatalf("react_always=false needs no boundary read; calls=%d", threads.boundaryCalls)
	}

	h, _, threads, sandboxes, reactor, _ := quietHandler(t, now)
	sandboxes.info.ReactAlways = &off
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, time.Hour)}
	h.Handle(context.Background(), threadReply(t, now, "QF-2"))
	if len(reactor.snapshot()) != 0 {
		t.Fatal("row react_always=false silences a thread reply regardless of the window")
	}

	// Row true over install false re-enables the quiet-period rule.
	h, _, threads, sandboxes, reactor, _ = quietHandler(t, now)
	h.ReactAlways = false
	sandboxes.info.ReactAlways = &on
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, 60*time.Second)}
	h.Handle(context.Background(), threadReply(t, now, "QF-3"))
	if len(reactor.snapshot()) != 0 {
		t.Fatal("row react_always=true still honours the quiet period")
	}
}

func TestReactQuiet_PerSandboxWins(t *testing.T) {
	now := time.Now()
	zero, six := 0, 600

	h, _, threads, sandboxes, reactor, _ := quietHandler(t, now)
	sandboxes.info.ReactQuietSeconds = &zero
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, 60*time.Second)}
	h.Handle(context.Background(), threadReply(t, now, "QS-1"))
	if len(reactor.snapshot()) != 1 {
		t.Fatal("row quiet=0 disables the window over install 180")
	}
	if threads.boundaryCalls != 0 {
		t.Fatalf("a disabled window needs no boundary read; calls=%d", threads.boundaryCalls)
	}

	h, _, threads, sandboxes, reactor, _ = quietHandler(t, now)
	h.ReactQuietSeconds = 0
	sandboxes.info.ReactQuietSeconds = &six
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, 300*time.Second)}
	h.Handle(context.Background(), threadReply(t, now, "QS-2"))
	if len(reactor.snapshot()) != 0 {
		t.Fatal("row quiet=600 over install 0 skips a reply 300 s after the last dispatch")
	}
}

// Strict mode reads the boundary for the context window too; one read serves both.
func TestReactQuiet_StrictContext_SingleBoundaryRead(t *testing.T) {
	now := time.Now()
	strict := false
	h, sqs, threads, sandboxes, reactor, _ := quietHandler(t, now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &strict
	threads.boundary = map[string]string{"C1|1.0": slackTSAgo(now, 60*time.Second)}
	h.ThreadReader = &fakeReader{}
	h.Handle(context.Background(), strictEvent(t, now, "<@UBOT123> go", fmt.Sprintf("%d.000500", now.Unix()), "1.0", "U0KURT", "QC-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("sends=%d", len(sqs.sends))
	}
	if threads.boundaryCalls != 1 {
		t.Fatalf("DispatchBoundary calls=%d, want 1 per event", threads.boundaryCalls)
	}
	if len(reactor.snapshot()) != 0 {
		t.Fatal("strict reply inside the window must not react")
	}
}

func TestReactQuiet_RoutedContinuityThread(t *testing.T) {
	now := time.Now()
	run := func(t *testing.T, ago time.Duration, rowQuiet *int, id string) (*fakeThreads, *fakeReactor, *fakeSQS) {
		h, sqs, threads, alias, _, reactor, _ := routedHandler(t, now)
		h.ReactAlways = true
		h.ReactQuietSeconds = 180
		row := alias.info
		row.ReactQuietSeconds = rowQuiet
		h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": row}}
		threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
		threads.boundary = map[string]string{"CFOREIGN|1.0": slackTSAgo(now, ago)}
		threads.markAdvancesBoundary = true
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "follow-up", fmt.Sprintf("%d.000500", now.Unix()), "1.0", id))
		return threads, reactor, sqs
	}

	threads, reactor, sqs := run(t, 60*time.Second, nil, "QR-1")
	if len(sqs.sends) != 1 || len(reactor.snapshot()) != 0 {
		t.Fatalf("routed reply inside the window: sends=%d reactions=%d", len(sqs.sends), len(reactor.snapshot()))
	}
	if threads.boundaryCalls > 1 {
		t.Fatalf("DispatchBoundary calls=%d, want at most 1", threads.boundaryCalls)
	}

	_, reactor, _ = run(t, 300*time.Second, nil, "QR-2")
	if len(reactor.snapshot()) != 1 {
		t.Fatal("routed reply after the window must react")
	}

	zero := 0
	_, reactor, _ = run(t, 60*time.Second, &zero, "QR-3")
	if len(reactor.snapshot()) != 1 {
		t.Fatal("the routed row's quiet=0 must win over install 180")
	}
}
