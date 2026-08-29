//go:build linux && amd64

package exec

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// The Go struct must stay the same size as the C one, or every field after the
// first mismatch decodes as garbage while still looking like a plausible record.
func TestRawHdrSize(t *testing.T) {
	if got, want := binary.Size(rawHdr{}), 56; got != want {
		t.Fatalf("rawHdr is %d bytes, C struct exec_hdr is %d — they have drifted", got, want)
	}
}

func TestTracer_CapturesAnExecWithArgv(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("loading BPF programs requires root")
	}
	tr, err := NewTracer()
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	defer tr.Close()

	// Give the tracepoints a moment to be live before generating the event.
	time.Sleep(200 * time.Millisecond)
	marker := "km-exec-capture-probe"
	if err := exec.Command("/bin/echo", marker).Run(); err != nil {
		t.Fatalf("probe command: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case r := <-tr.Events():
			if r.Kind != execlog.KindExec {
				continue
			}
			if len(r.Args) >= 2 && r.Args[1] == marker {
				if r.PID == 0 || r.UID != 0 {
					t.Errorf("record has implausible identity: %+v", r)
				}
				if got := time.Since(r.TS); got >= 30*time.Second {
					t.Errorf("boot-time conversion is off by %v: %v", got, r.TS)
				}
				return
			}
		case <-deadline:
			t.Fatal("never saw the probe exec; tracepoints attached but produced nothing")
		}
	}
}
