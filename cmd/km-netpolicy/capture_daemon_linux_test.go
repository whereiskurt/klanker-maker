//go:build linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

// captureOpts is a daemon opts with no S3 wiring, so uploadCapture fails fast
// on the missing bucket env instead of reaching AWS.
func captureOpts(t *testing.T) opts {
	t.Helper()
	var out, errb bytes.Buffer
	return opts{captureDir: t.TempDir(), stdout: &out, stderr: &errb}
}

// finished builds the state a capture is left in when its loop returns on its
// OWN bound — the deadline or --max-size — rather than on `capture stop`.
func finished(path string) *captureState {
	done := make(chan struct{})
	close(done)
	return &captureState{active: true, path: path, stop: make(chan struct{}), done: done}
}

// A capture that hit its own duration or size bound used to leave active=true
// forever: nothing on that path cleared it. `status` kept reporting "capturing
// to …" indefinitely and `start` kept refusing with "a capture is already
// running". For a diagnostic tool a status that lies is worse than usual.
func TestCaptureState_SelfCompletedCaptureIsCollected(t *testing.T) {
	o := captureOpts(t)
	st := finished("/tmp/km-capture-test.pcap")

	st.mu.Lock()
	st.collectLocked(o)
	st.mu.Unlock()

	if st.active {
		t.Error("a finished capture must clear active, or start stays blocked forever")
	}
	got := st.status().Msg
	if strings.Contains(got, "capturing to") {
		t.Errorf("status must not claim a finished capture is still running: %q", got)
	}
	if !strings.Contains(got, "idle") || !strings.Contains(got, "km-capture-test.pcap") {
		t.Errorf("status must report the finished capture's file: %q", got)
	}
}

// The self-completion goroutine and an explicit `capture stop` can arrive at
// the same moment. Collection must happen exactly once — a second upload of the
// same file would be wasted work, and a second clear would corrupt the reported
// result.
func TestCaptureState_CollectIsIdempotent(t *testing.T) {
	o := captureOpts(t)
	st := finished("/tmp/km-capture-test.pcap")

	st.mu.Lock()
	st.collectLocked(o)
	first := st.lastMsg
	st.collectLocked(o)
	second := st.lastMsg
	st.mu.Unlock()

	if first != second {
		t.Errorf("a second collect changed the result: %q -> %q", first, second)
	}
}

// `capture stop` on a capture that already finished on its own must report
// where the file went, not a bare "no capture is running" that reads as though
// the capture had been lost.
func TestCaptureState_StopAfterSelfCompletionReportsTheFile(t *testing.T) {
	o := captureOpts(t)
	st := finished("/tmp/km-capture-test.pcap")

	st.mu.Lock()
	st.collectLocked(o)
	st.mu.Unlock()

	resp := st.stopCapture(o)
	if !resp.OK {
		t.Errorf("stop after self-completion must not report failure: %q", resp.Msg)
	}
	if !strings.Contains(resp.Msg, "km-capture-test.pcap") {
		t.Errorf("stop must name the finished capture: %q", resp.Msg)
	}
}

// stopCapture must not close an already-closed stop channel: a loop that
// returned on its own bound has closed done without ever reading stop, and a
// double close panics the whole daemon.
func TestCaptureState_StopDoesNotDoubleCloseAFinishedLoop(t *testing.T) {
	o := captureOpts(t)
	st := finished("/tmp/km-capture-test.pcap")

	resp := st.stopCapture(o) // must not panic
	if !resp.OK {
		t.Errorf("stop on a finished-but-uncollected capture must succeed: %q", resp.Msg)
	}
	if st.active {
		t.Error("stop must clear active")
	}
}

// Nothing has ever run: status is plainly idle and stop says so.
func TestCaptureState_IdleFromTheStart(t *testing.T) {
	o := captureOpts(t)
	st := &captureState{}
	if got := st.status().Msg; got != "idle" {
		t.Errorf("status = %q, want idle", got)
	}
	if resp := st.stopCapture(o); resp.OK {
		t.Errorf("stop with nothing running must report failure: %q", resp.Msg)
	}
}
