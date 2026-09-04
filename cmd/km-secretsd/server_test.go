package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// mu guards events: Handle runs one goroutine per connection (see Serve),
// and the concurrency tests below deliberately drive several at once against
// one recordingAudit.
type recordingAudit struct {
	mu     sync.Mutex
	events []map[string]any
}

func (r *recordingAudit) Emit(t string, d map[string]any) error {
	e := map[string]any{"event_type": t}
	for k, v := range d {
		e[k] = v
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func serverWith(t *testing.T, yaml string, g secrets.Grants) (*Server, *recordingAudit) {
	t.Helper()
	stubDecrypt(t, yaml, nil)
	aud := &recordingAudit{}
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	return &Server{CiphertextPath: p, Grants: g, Audit: aud}, aud
}

// roundTrip runs one request through Handle over a socketpair.
func roundTrip(t *testing.T, s *Server, req secrets.UnsealRequest, uid, pid uint32) secrets.UnsealResponse {
	t.Helper()
	c1, c2 := net.Pipe()
	go func() {
		defer c2.Close()
		s.Handle(c2, uid, pid)
	}()
	defer c1.Close()
	if err := json.NewEncoder(c1).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(c1).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandle_ReturnsGrantedKeysOnly(t *testing.T) {
	s, _ := serverWith(t, "A: 1\nB: 2\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if _, ok := resp.Values["B"]; ok {
		t.Error("returned B, which claude was never granted")
	}
	if string(resp.Values["A"]) != "1" {
		t.Errorf("A = %q", resp.Values["A"])
	}
}

func TestHandle_UnknownConsumerRefused(t *testing.T) {
	s, _ := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "codex"}, 1000, 4242)

	if resp.Error == "" {
		t.Fatal("an ungranted identity was served")
	}
	if len(resp.Values) != 0 {
		t.Error("values returned alongside an error")
	}
}

func TestHandle_AuditsEveryUnsealWithNamesNotValues(t *testing.T) {
	s, aud := serverWith(t, "A: supersecret\n", nil)
	roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	ev := aud.events[0]
	if ev["event_type"] != "secret_unseal" {
		t.Errorf("event_type = %v", ev["event_type"])
	}
	if ev["uid"] != uint32(1000) || ev["pid"] != uint32(4242) {
		t.Errorf("peer credentials not recorded: %+v", ev)
	}
	blob, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("audit event is unmarshalable: %v", err)
	}
	if contains(string(blob), "supersecret") {
		t.Error("audit event contains a secret VALUE")
	}
}

func TestHandle_AuditsRefusals(t *testing.T) {
	// A refused request is the more interesting security event, not the less.
	s, aud := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	roundTrip(t, s, secrets.UnsealRequest{As: "nobody"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	if aud.events[0]["event_type"] != "secret_unseal_refused" {
		t.Errorf("event_type = %v", aud.events[0]["event_type"])
	}
}

func TestHandle_DecryptFailureIsRefusalNotEmptySuccess(t *testing.T) {
	stubDecrypt(t, "", errDecryptStub)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: &recordingAudit{}}
	resp := roundTrip(t, s, secrets.UnsealRequest{}, 1000, 1)

	if resp.Error == "" {
		t.Fatal("a KMS failure was reported as success with no keys")
	}
}

// --- Important 1: I/O deadlines -------------------------------------------

func TestHandle_ReadDeadlineUnblocksOnSilentPeer(t *testing.T) {
	// A peer that connects and never writes a request must not pin Handle,
	// its goroutine, and the decrypted bundle forever.
	s, _ := serverWith(t, "A: 1\n", nil)
	s.RequestTimeout = 50 * time.Millisecond

	c1, c2 := net.Pipe()
	defer c1.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handle(c2, 1000, 4242)
	}()
	// Deliberately never write anything on c1.

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not return for a silent peer; read deadline not enforced")
	}
}

func TestHandle_WriteDeadlineUnblocksOnStalledReader(t *testing.T) {
	// The write-side stall is the security-relevant one: it is the path where
	// bundle.Zero() and the resp.Values zeroing loop would otherwise never
	// run, leaving decrypted plaintext resident for as long as the peer
	// chooses to withhold its read.
	s, _ := serverWith(t, "A: 1\n", nil)
	s.RequestTimeout = 50 * time.Millisecond

	c1, c2 := net.Pipe()
	defer c1.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handle(c2, 1000, 4242)
	}()

	if err := json.NewEncoder(c1).Encode(secrets.UnsealRequest{As: "claude"}); err != nil {
		t.Fatal(err)
	}
	// Deliberately never read the response back on c1.

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not return for a stalled reader; write deadline not enforced")
	}
}

// --- Important 3: bounded concurrent decrypts ------------------------------

func TestHandle_ConcurrentDecryptsNeverExceedTheBound(t *testing.T) {
	// Each LoadBundle is a live kms:Decrypt call. Fire more requests than
	// MaxConcurrentDecrypts at once and prove the number actually inside
	// decryptFile at any moment never exceeds the bound — and that every
	// request still eventually succeeds (waiting on the semaphore is
	// correct; only exceeding RequestTimeout should refuse).
	prevDecrypt := decryptFile
	var active, maxActive int32
	release := make(chan struct{})
	decryptFile = func(path, format string) ([]byte, error) {
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&active, -1)
		return []byte("A: 1\n"), nil
	}
	t.Cleanup(func() { decryptFile = prevDecrypt })

	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		CiphertextPath:        p,
		Audit:                 &recordingAudit{},
		MaxConcurrentDecrypts: 2,
		RequestTimeout:        2 * time.Second,
	}

	const n = 5
	var wg sync.WaitGroup
	responses := make([]secrets.UnsealResponse, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = roundTrip(t, s, secrets.UnsealRequest{}, 1000, uint32(i))
		}(i)
	}

	time.Sleep(100 * time.Millisecond) // let goroutines pile up against the semaphore
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got > 2 {
		t.Errorf("observed %d concurrent decrypts, want <= MaxConcurrentDecrypts (2)", got)
	}
	for i, r := range responses {
		if r.Error != "" {
			t.Errorf("response %d unexpectedly refused: %s", i, r.Error)
		}
	}
}

func TestHandle_RefusesWhenDecryptSlotUnavailableWithinTimeout(t *testing.T) {
	// A request that cannot acquire a decrypt slot before its own
	// RequestTimeout must be refused (and audited), not queued indefinitely.
	prevDecrypt := decryptFile
	started := make(chan struct{}, 1)
	block := make(chan struct{}) // deliberately never closed
	decryptFile = func(path, format string) ([]byte, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return []byte("A: 1\n"), nil // unreachable in this test
	}
	t.Cleanup(func() { decryptFile = prevDecrypt })

	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	aud := &recordingAudit{}
	s := &Server{
		CiphertextPath:        p,
		Audit:                 aud,
		MaxConcurrentDecrypts: 1,
		RequestTimeout:        50 * time.Millisecond,
	}

	// The first request occupies the sole decrypt slot and never releases it
	// within this test — only the SECOND request's outcome matters here, so
	// the first's goroutine is left running (harmlessly, for the life of the
	// test binary) rather than synchronized to completion.
	c1a, c1b := net.Pipe()
	defer c1a.Close()
	go s.Handle(c1b, 1000, 1)
	if err := json.NewEncoder(c1a).Encode(secrets.UnsealRequest{}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never entered decryptFile")
	}

	resp := roundTrip(t, s, secrets.UnsealRequest{}, 1000, 2)
	if resp.Error == "" {
		t.Fatal("a second concurrent request was served while the only decrypt slot was held")
	}

	var sawRefusal bool
	for _, ev := range aud.events {
		if ev["event_type"] == "secret_unseal_refused" {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Error("the refused-for-concurrency request was never audited")
	}
}

// --- Important 2: Serve's accept loop --------------------------------------

// fakeListener lets tests control exactly what Accept returns, without real
// fd exhaustion or a real socket.
type fakeListener struct {
	mu        sync.Mutex
	err       error
	calls     int
	realConns chan net.Conn
	closed    bool
}

func (f *fakeListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	f.calls++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	c, ok := <-f.realConns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (f *fakeListener) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.realConns)
	}
	return nil
}

func (f *fakeListener) Addr() net.Addr { return fakeAddr{} }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

func TestServe_ReturnsNilOnContextCancellation(t *testing.T) {
	s := &Server{Audit: &recordingAudit{}}
	fl := &fakeListener{realConns: make(chan net.Conn)}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, fl) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v, want nil after context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

func TestServe_TerminalAcceptErrorReturnsErrorWithoutCtxCancel(t *testing.T) {
	// A listener that is genuinely dead (net.ErrClosed) while ctx was never
	// cancelled must surface as an error so systemd restarts the daemon,
	// rather than Serve returning nil or looping forever.
	s := &Server{Audit: &recordingAudit{}}
	fl := &fakeListener{err: net.ErrClosed, realConns: make(chan net.Conn)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, fl) }()

	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve() = %v, want an error wrapping net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after a terminal accept error")
	}
}

func TestServe_TransientAcceptErrorsBackOffInsteadOfSpinning(t *testing.T) {
	// A persistent-but-non-terminal accept error (the EMFILE/ENFILE shape)
	// must not spin the loop at full speed forever and silently. Over a
	// 100ms window, a busy spin would rack up tens of thousands of Accept
	// calls; backoff should keep this well under a hundred.
	s := &Server{Audit: &recordingAudit{}}
	fl := &fakeListener{err: errors.New("too many open files"), realConns: make(chan net.Conn)}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, fl) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v, want nil after context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}

	fl.mu.Lock()
	calls := fl.calls
	fl.mu.Unlock()
	if calls == 0 {
		t.Fatal("Accept was never called")
	}
	if calls > 200 {
		t.Errorf("Accept called %d times in ~100ms; accept-error backoff is not throttling the loop", calls)
	}
}

func TestServe_RecoversAfterTransientAcceptErrors(t *testing.T) {
	s, _ := serverWith(t, "A: 1\n", nil)
	fl := &fakeListener{err: errors.New("transient"), realConns: make(chan net.Conn, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, fl) }()

	time.Sleep(20 * time.Millisecond)
	fl.mu.Lock()
	fl.err = nil
	fl.mu.Unlock()

	c1, c2 := net.Pipe()
	fl.realConns <- c2

	if err := json.NewEncoder(c1).Encode(secrets.UnsealRequest{As: "claude"}); err != nil {
		t.Fatal(err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(c1).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error after recovery: %s", resp.Error)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v, want nil after context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}
