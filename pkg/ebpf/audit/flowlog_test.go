//go:build linux

package audit

import (
	"net"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// newTestConsumer builds a Consumer with no ring buffer reader — handleEvent
// is exercised directly, so the real ringbuf.Reader (which needs a live BPF
// map) is never touched.
func newTestConsumer(flows *flowlog.Writer, nameFn flowlog.NameFn) *Consumer {
	return &Consumer{
		sandboxID: "sb-test",
		logger:    zerolog.Nop(),
		flows:     flows,
		nameFn:    nameFn,
	}
}

func TestHandleEvent_RecordsOneFlowPerEvent(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcEBPF)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	c := newTestConsumer(w, nil)
	c.handleEvent(bpfEvent{Pid: 1, DstIP: ipToUint32(t, "1.2.3.4"), DstPort: 443, Action: actionAllow})
	c.handleEvent(bpfEvent{Pid: 2, DstIP: ipToUint32(t, "5.6.7.8"), DstPort: 80, Action: actionDeny})

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one record per event (2), got %d:\n%s", len(lines), body)
	}
}

// TestHandleEvent_Connect4PortIsByteSwapped pins the live-observed
// regression: a curl to github.com:443 recorded dst_ip=0x0372528C and
// dst_port=0xBB01=47873 in the ring buffer. Both fields must decode to the
// true destination (140.82.114.3:443), not the byte-reversed one this
// bridge shipped with.
func TestHandleEvent_Connect4PortIsByteSwapped(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcEBPF)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	c := newTestConsumer(w, nil)
	c.handleEvent(bpfEvent{
		Pid:     33061,
		DstIP:   0x0372528C, // wire bytes for 140.82.114.3
		DstPort: 0xBB01,     // raw, unconverted user_port for 443
		Action:  actionRedirect,
		Layer:   layerConnect4,
	})

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	got := string(body)
	for _, want := range []string{`"addr":"140.82.114.3"`, `"port":443`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in:\n%s", want, got)
		}
	}
}

// TestHandleEvent_Sendmsg4PortNotDoubleSwapped guards the corollary bug a
// blanket "swap every connect4/sendmsg4 port" fix would introduce: sendmsg4
// converts its port to host order on the BPF side before emitting, so its
// dst_port (always 53 — sendmsg4 only fires for DNS) must reach the flow
// record unswapped.
func TestHandleEvent_Sendmsg4PortNotDoubleSwapped(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcEBPF)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	c := newTestConsumer(w, nil)
	c.handleEvent(bpfEvent{
		Pid:     100,
		DstIP:   ipToUint32(t, "8.8.8.8"),
		DstPort: 53,
		Action:  actionRedirect,
		Layer:   layerSendmsg4,
	})

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	if !strings.Contains(string(body), `"port":53`) {
		t.Errorf("want unswapped port 53 in:\n%s", body)
	}
}

func TestHandleEvent_VerdictMapping(t *testing.T) {
	cases := []struct {
		action  uint8
		verdict string
	}{
		{actionDeny, flowlog.VerdictDeny},
		{actionAllow, flowlog.VerdictAllow},
		{actionRedirect, flowlog.VerdictRedirect},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		path := flowlog.FileFor(dir, flowlog.SrcEBPF)
		w := flowlog.NewWriter(path, 1<<20)

		c := newTestConsumer(w, nil)
		c.handleEvent(bpfEvent{DstIP: ipToUint32(t, "1.1.1.1"), Action: tc.action})
		w.Close()

		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("action=%d: no flow file written: %v", tc.action, err)
		}
		want := `"verdict":"` + tc.verdict + `"`
		if !strings.Contains(string(body), want) {
			t.Errorf("action=%d: want %s in:\n%s", tc.action, want, body)
		}
	}
}

func TestHandleEvent_CarriesAddrPortPIDComm(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcEBPF)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	c := newTestConsumer(w, nil)
	var comm [16]byte
	copy(comm[:], "curl")
	c.handleEvent(bpfEvent{Pid: 4242, DstIP: ipToUint32(t, "9.9.9.9"), DstPort: 8080, Action: actionAllow, Comm: comm})

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	got := string(body)
	for _, want := range []string{`"addr":"9.9.9.9"`, `"port":8080`, `"pid":4242`, `"comm":"curl"`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in:\n%s", want, got)
		}
	}
}

func TestHandleEvent_NilWriterIsSafe(t *testing.T) {
	// Flow recording must never be load-bearing: a nil writer must not panic
	// and must not block the (would-be) ring buffer drain.
	c := newTestConsumer(nil, nil)
	c.handleEvent(bpfEvent{Pid: 1, DstIP: ipToUint32(t, "1.2.3.4"), Action: actionAllow})
}

func TestHandleEvent_NameFnResolvesHost(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcEBPF)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	c := newTestConsumer(w, func(addr string) string {
		if addr == "1.2.3.4" {
			return "api.github.com"
		}
		return ""
	})
	c.handleEvent(bpfEvent{DstIP: ipToUint32(t, "1.2.3.4"), Action: actionAllow})

	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `"host":"api.github.com"`) {
		t.Errorf("want resolved host in:\n%s", body)
	}
}

func TestHandleEvent_NilOrEmptyNameFnLeavesHostEmpty(t *testing.T) {
	// A wrong hostname is worse than an absent one — an operator pins against
	// this data. Neither a nil NameFn nor one that returns "" may fabricate a
	// placeholder host.
	cases := []struct {
		name   string
		nameFn flowlog.NameFn
	}{
		{"nil NameFn", nil},
		{"NameFn returns empty", func(string) string { return "" }},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		path := flowlog.FileFor(dir, flowlog.SrcEBPF)
		w := flowlog.NewWriter(path, 1<<20)

		c := newTestConsumer(w, tc.nameFn)
		c.handleEvent(bpfEvent{DstIP: ipToUint32(t, "1.2.3.4"), Action: actionAllow})
		w.Close()

		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: no flow file written: %v", tc.name, err)
		}
		if strings.Contains(string(body), `"host"`) {
			t.Errorf("%s: want no host field (empty, omitempty) in:\n%s", tc.name, body)
		}
	}
}

// ipToUint32 converts a dotted-quad string into the bpfEvent.DstIP value a
// LittleEndian binary.Read produces for those wire-order octets — the
// correct inverse of the FIXED uint32ToIP (binary.LittleEndian.PutUint32).
//
// The previous version of this helper built the uint32 MSB-first
// (ip[0]<<24 | ip[1]<<16 | ip[2]<<8 | ip[3]), documented as mirroring
// uint32ToIP's inverse — which it did, but uint32ToIP itself was wrong (see
// its doc comment), so every test built on this helper exercised the bug's
// own byte order rather than the kernel's, and passed anyway.
func ipToUint32(t *testing.T, s string) uint32 {
	t.Helper()
	ip := net.ParseIP(s).To4()
	if ip == nil {
		t.Fatalf("invalid test IP: %s", s)
	}
	return uint32(ip[0]) | uint32(ip[1])<<8 | uint32(ip[2])<<16 | uint32(ip[3])<<24
}
