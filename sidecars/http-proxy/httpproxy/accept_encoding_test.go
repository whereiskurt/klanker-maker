package httpproxy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// The Anthropic Python SDK (httpx) signs Accept-Encoding into its SigV4
// signature. goproxy drops a client's Accept-Encoding by default and Go's
// transport then adds its own "gzip", so AWS sees a different value from the
// one signed and answers 403 "signature does not match". These tests pin the
// narrow fix: a SigV4 request that signed accept-encoding keeps it verbatim,
// every other request behaves exactly as before.

const fakeSigV4AE = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261105/us-east-1/bedrock/aws4_request, " +
	"SignedHeaders=accept-encoding;content-type;host;x-amz-date, Signature=deadbeef"

const fakeSigV4NoAE = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261105/us-east-1/bedrock/aws4_request, " +
	"SignedHeaders=content-type;host;x-amz-date, Signature=deadbeef"

// aeRecorder is a target server that records the Accept-Encoding it received.
type aeRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (a *aeRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = r.Header.Values("Accept-Encoding")
		a.mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	})
}

func (a *aeRecorder) got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

// sendThroughProxy sends one GET to target via a fresh plain-HTTP proxy
// allowing the target host, with the given Authorization and Accept-Encoding.
func sendThroughProxy(t *testing.T, authz, ae string) []string {
	t.Helper()
	rec := &aeRecorder{}
	target := httptest.NewServer(rec.handler())
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)

	proxy := httpproxy.NewProxy([]string{targetURL.Hostname()}, "sb-test")
	_, proxyAddr := startProxyServer(t, proxy)

	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{Transport: &http.Transport{
		Proxy:              http.ProxyURL(proxyURL),
		DisableCompression: true, // send exactly the header the test sets
	}}
	req, _ := http.NewRequest(http.MethodGet, target.URL, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	if ae != "" {
		req.Header.Set("Accept-Encoding", ae)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	return rec.got()
}

func TestAcceptEncoding_SigV4SignedIsPreserved(t *testing.T) {
	got := sendThroughProxy(t, fakeSigV4AE, "gzip, deflate")
	if len(got) != 1 || got[0] != "gzip, deflate" {
		t.Fatalf("upstream Accept-Encoding = %q, want exactly [\"gzip, deflate\"]", got)
	}
}

func TestAcceptEncoding_UnsignedIsDroppedAsBefore(t *testing.T) {
	// Pre-fix behaviour: the client's value is removed and Go's transport
	// supplies its own "gzip" (and transparently decompresses the reply).
	got := sendThroughProxy(t, "", "gzip, deflate")
	if len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("upstream Accept-Encoding = %q, want transport-managed [\"gzip\"]", got)
	}
}

func TestAcceptEncoding_SigV4WithoutSignedAEIsDroppedAsBefore(t *testing.T) {
	got := sendThroughProxy(t, fakeSigV4NoAE, "gzip, deflate")
	if len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("upstream Accept-Encoding = %q, want transport-managed [\"gzip\"]", got)
	}
}

func TestAcceptEncoding_BearerAuthIsDroppedAsBefore(t *testing.T) {
	got := sendThroughProxy(t, "Bearer accept-encoding", "gzip, deflate")
	if len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("upstream Accept-Encoding = %q, want transport-managed [\"gzip\"]", got)
	}
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}

const bedrockInvokeJSON = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],` +
	`"usage":{"input_tokens":120,"output_tokens":45}}`

// skRecorder is a BudgetAPI stub safe for the metering goroutine; it signals
// done on the first UpdateItem so the test need not sleep.
type skRecorder struct {
	inner captureModelIDStub
	mu    sync.Mutex
	done  chan struct{}
	once  sync.Once
}

func (s *skRecorder) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	s.mu.Lock()
	out, err := s.inner.UpdateItem(ctx, in, opts...)
	s.mu.Unlock()
	s.once.Do(func() { close(s.done) })
	return out, err
}

func (s *skRecorder) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return s.inner.GetItem(ctx, in, opts...)
}

func newSKRecorder() *skRecorder { return &skRecorder{done: make(chan struct{})} }

func (s *skRecorder) sk(t *testing.T) string {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.capturedSK
}

// Bedrock metering must still count tokens when a preserved Accept-Encoding
// makes upstream answer gzip, and the client must receive the compressed bytes
// untouched (it decompresses them itself). Drives the MITM (https.go) path.
func TestAcceptEncoding_BedrockGzipResponseMeteredAndPassedThrough(t *testing.T) {
	compressed := gzipBytes(t, bedrockInvokeJSON)
	var upstreamAE string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAE = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed)
	}))
	defer target.Close()
	targetAddr := target.Listener.Addr().String()

	stub := newSKRecorder()
	proxy := httpproxy.NewProxy([]string{".amazonaws.com"}, "sb-test",
		httpproxy.WithBudgetEnforcement(stub, "km-budgets", map[string]kmaws.BedrockModelRate{}, nil))
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test upstream
	}
	_, proxyAddr := startProxyServer(t, proxy)
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{Transport: &http.Transport{
		Proxy:              http.ProxyURL(proxyURL),
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // goproxy MITM cert
		DisableCompression: true,
	}}

	const model = "us.anthropic.claude-sonnet-4-6"
	req, _ := http.NewRequest(http.MethodPost,
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/"+model+"/invoke", strings.NewReader(`{}`))
	req.Header.Set("Authorization", fakeSigV4AE)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if upstreamAE != "gzip, deflate" {
		t.Errorf("upstream Accept-Encoding = %q, want %q", upstreamAE, "gzip, deflate")
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("client Content-Encoding = %q, want gzip", got)
	}
	if !bytes.Equal(body, compressed) {
		t.Errorf("client body was altered in transit (%d bytes, want %d compressed bytes)", len(body), len(compressed))
	}
	if want := "BUDGET#ai#" + httpproxy.ExtractModelID("/model/"+model+"/invoke"); stub.sk(t) != want {
		t.Errorf("metered SK = %q, want %q (gzip body must be decoded for metering)", stub.sk(t), want)
	}
}

// TestAcceptEncoding_RealSigV4SurvivesProxy signs a request with the real
// aws-sdk-go-v2 v4 signer (which signs Accept-Encoding) and has the upstream
// re-derive the signature from what it received. Any change to a signed
// header in transit fails the test — the property AWS actually enforces.
func TestAcceptEncoding_RealSigV4SurvivesProxy(t *testing.T) {
	creds := aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}
	signer := v4.NewSigner()
	const region, service = "us-east-1", "bedrock"

	var verifyErr string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyErr = verifySigV4(r, signer, creds, service, region)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer target.Close()
	targetAddr := target.Listener.Addr().String()

	proxy := httpproxy.NewProxy([]string{".amazonaws.com"}, "sb-test",
		httpproxy.WithBudgetEnforcement(&captureModelIDStub{}, "km-budgets", map[string]kmaws.BedrockModelRate{}, nil))
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test upstream
	}
	_, proxyAddr := startProxyServer(t, proxy)
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{Transport: &http.Transport{
		Proxy:              http.ProxyURL(proxyURL),
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // goproxy MITM cert
		DisableCompression: true,
	}}

	payload := []byte(`{"anthropic_version":"bedrock-2023-05-31","max_tokens":1}`)
	req, _ := http.NewRequest(http.MethodPost,
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/us.anthropic.claude-sonnet-4-6/invoke",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	sum := sha256.Sum256(payload)
	if err := signer.SignHTTP(context.Background(), creds, req, hex.EncodeToString(sum[:]), service, region, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(req.Header.Get("Authorization"), "accept-encoding") {
		t.Fatalf("precondition: signer did not sign accept-encoding: %s", req.Header.Get("Authorization"))
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if verifyErr != "" {
		t.Fatalf("upstream signature verification failed: %s", verifyErr)
	}
}

// verifySigV4 rebuilds the request from only the headers the client signed
// (the proxy may legitimately add unsigned ones such as traceparent), re-signs
// it at the client's X-Amz-Date and compares the resulting Authorization.
func verifySigV4(r *http.Request, signer *v4.Signer, creds aws.Credentials, service, region string) string {
	authz := r.Header.Get("Authorization")
	i := strings.Index(authz, "SignedHeaders=")
	if i < 0 {
		return "no SignedHeaders in " + authz
	}
	list := authz[i+len("SignedHeaders="):]
	if j := strings.IndexByte(list, ','); j >= 0 {
		list = list[:j]
	}
	ts, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return "bad X-Amz-Date: " + err.Error()
	}
	body, _ := io.ReadAll(r.Body)
	u := *r.URL
	u.Scheme, u.Host = "https", r.Host
	re, _ := http.NewRequest(r.Method, u.String(), bytes.NewReader(body))
	re.Host = r.Host
	for _, h := range strings.Split(list, ";") {
		if h == "host" {
			continue
		}
		for _, v := range r.Header.Values(h) {
			re.Header.Add(h, v)
		}
	}
	sum := sha256.Sum256(body)
	if err := signer.SignHTTP(context.Background(), creds, re, hex.EncodeToString(sum[:]), service, region, ts); err != nil {
		return "re-sign: " + err.Error()
	}
	if got := re.Header.Get("Authorization"); got != authz {
		return "signature mismatch:\n received " + authz + "\n computed " + got
	}
	return ""
}

func (s *skRecorder) Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
