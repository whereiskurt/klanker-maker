package httpproxy

// Internal (same-package) test file for pidresolve.go's PID-attribution
// logic. lookupPID is tested against fake pidLookupMap implementations
// rather than real pinned BPF maps — there is no kernel or root available
// in unit tests, exactly like transparent_internal_test.go's rationale for
// factoring out matchInterceptForRequest.

import (
	"errors"
	"net/http"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// fakeLookupMap is a minimal pidLookupMap stand-in. hit maps a key's fmt.Sprint
// representation to a canned output value; anything else misses.
type fakeLookupMap struct {
	// onLookup, if set, fully controls behavior for this call.
	onLookup func(key, valueOut interface{}) error
}

func (f *fakeLookupMap) Lookup(key, valueOut interface{}) error {
	return f.onLookup(key, valueOut)
}

func TestLookupPID_Hit(t *testing.T) {
	portToSock := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		port := key.(*uint16)
		if *port != 54321 {
			t.Fatalf("unexpected port key: %d", *port)
		}
		out := valueOut.(*uint64)
		*out = 0xdeadbeef
		return nil
	}}
	sockToPid := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		cookie := key.(*uint64)
		if *cookie != 0xdeadbeef {
			t.Fatalf("unexpected cookie key: %x", *cookie)
		}
		out := valueOut.(*uint32)
		*out = 4242
		return nil
	}}

	got := lookupPID(portToSock, sockToPid, 54321)
	if got != 4242 {
		t.Fatalf("lookupPID() = %d, want 4242", got)
	}
}

func TestLookupPID_MissAtFirstHop(t *testing.T) {
	portToSock := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		return errors.New("key does not exist")
	}}
	sockToPid := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		t.Fatal("sockToPid.Lookup must not be called when the first hop misses")
		return nil
	}}

	got := lookupPID(portToSock, sockToPid, 1)
	if got != 0 {
		t.Fatalf("lookupPID() = %d, want 0 on a first-hop miss", got)
	}
}

func TestLookupPID_MissAtSecondHop(t *testing.T) {
	portToSock := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		out := valueOut.(*uint64)
		*out = 99
		return nil
	}}
	sockToPid := &fakeLookupMap{onLookup: func(key, valueOut interface{}) error {
		return errors.New("key does not exist")
	}}

	got := lookupPID(portToSock, sockToPid, 1)
	if got != 0 {
		t.Fatalf("lookupPID() = %d, want 0 on a second-hop miss", got)
	}
}

func TestLookupPID_NilMaps(t *testing.T) {
	if got := lookupPID(nil, nil, 1); got != 0 {
		t.Fatalf("lookupPID() with nil maps = %d, want 0", got)
	}
}

func TestPIDResolver_Resolve_LoadFailureReturnsZero(t *testing.T) {
	// A resolver over a directory with no pinned maps must fail soft: no
	// panic, no error returned to the caller, just an unresolved PID.
	r := newPIDResolver("nonexistent-sandbox-id-for-test")
	if got := r.resolve(12345); got != 0 {
		t.Fatalf("resolve() with no pinned maps = %d, want 0", got)
	}
	// A second call must not re-attempt the load or log again (loadOnce);
	// calling it twice here mainly guards against a panic on repeat use.
	if got := r.resolve(12345); got != 0 {
		t.Fatalf("resolve() second call = %d, want 0", got)
	}
}

func TestPIDResolver_ResolveFromRemoteAddr(t *testing.T) {
	r := newPIDResolver("nonexistent-sandbox-id-for-test")

	cases := []string{"", "not-a-valid-addr", "127.0.0.1:notaport", "127.0.0.1:54321"}
	for _, addr := range cases {
		if got := r.resolveFromRemoteAddr(addr); got != 0 {
			t.Fatalf("resolveFromRemoteAddr(%q) = %d, want 0 (no BPF maps pinned in test)", addr, got)
		}
	}
}

func TestPIDResolver_NilReceiverIsSafe(t *testing.T) {
	var r *pidResolver
	if got := r.resolve(1); got != 0 {
		t.Fatalf("nil resolver.resolve() = %d, want 0", got)
	}
	if got := r.resolveFromRemoteAddr("127.0.0.1:1"); got != 0 {
		t.Fatalf("nil resolver.resolveFromRemoteAddr() = %d, want 0", got)
	}
}

func TestCommForPID(t *testing.T) {
	if got := commForPID(0); got != "" {
		t.Fatalf("commForPID(0) = %q, want empty", got)
	}
	if got := commForPID(-1); got != "" {
		t.Fatalf("commForPID(-1) = %q, want empty", got)
	}
	// A PID vanishingly unlikely to exist must fail soft, not error out.
	if got := commForPID(1 << 30); got != "" {
		t.Fatalf("commForPID(huge pid) = %q, want empty", got)
	}
}

// TestRecordFlow_StillWritesWhenPIDResolutionFails pins the requirement that
// a flow record is written regardless of whether PID attribution succeeds —
// resolution failing (no pinned maps, as in every unit test environment)
// must never suppress the record itself.
func TestRecordFlow_StillWritesWhenPIDResolutionFails(t *testing.T) {
	dir := t.TempDir()
	flows := flowlog.NewWriter(flowlog.FileFor(dir, flowlog.SrcHTTP), flowlog.DefaultMaxBytes)
	resolver := newPIDResolver("nonexistent-sandbox-id-for-test")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	ctx := &goproxy.ProxyCtx{Req: req}

	recordFlow(flows, resolver, flowlog.VerdictAllow, "example.com:443", ctx)
	flows.Close()

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Host != "example.com" || recs[0].Verdict != flowlog.VerdictAllow {
		t.Fatalf("unexpected record: %+v", recs[0])
	}
	if recs[0].PID != 0 || recs[0].Comm != "" {
		t.Fatalf("expected no PID/Comm attribution (no pinned maps in test), got %+v", recs[0])
	}
}

// TestRecordFlow_NilCtxAndResolverAreSafe pins that a nil ctx or nil resolver
// (both real call shapes — WithSESMITM passes a nil resolver) never panics
// and still writes the base record.
func TestRecordFlow_NilCtxAndResolverAreSafe(t *testing.T) {
	dir := t.TempDir()
	flows := flowlog.NewWriter(flowlog.FileFor(dir, flowlog.SrcHTTP), flowlog.DefaultMaxBytes)

	recordFlow(flows, nil, flowlog.VerdictDeny, "evil.example.com", nil)

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	ctx := &goproxy.ProxyCtx{Req: req} // RemoteAddr left empty
	recordFlow(flows, newPIDResolver("nonexistent-sandbox-id-for-test"), flowlog.VerdictAllow, "example.com", ctx)
	flows.Close()

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
}
