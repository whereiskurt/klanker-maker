package httpproxy_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	httpproxy "github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// mustMarshalIntercepts JSON-marshals ics, failing the test on error. Used to
// simulate the compiler side of the KM_MITM_INTERCEPTS transport before
// exercising ParseIntercepts (the sidecar side) on the encoded result.
func mustMarshalIntercepts(t *testing.T, ics []httpproxy.Intercept) string {
	t.Helper()
	b, err := json.Marshal(ics)
	if err != nil {
		t.Fatalf("json.Marshal(%+v) failed: %v", ics, err)
	}
	return string(b)
}

// readAllBody reads and returns resp.Body as a string, closing it afterward.
func readAllBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body failed: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// MatchesHost
// ---------------------------------------------------------------------------

func TestMatchesHost_LeadingDotMatchesApex(t *testing.T) {
	if !httpproxy.MatchesHost("google.com", []string{".google.com"}) {
		t.Error("leading-dot pattern must match the bare apex")
	}
}

func TestMatchesHost_LeadingDotMatchesSubdomain(t *testing.T) {
	if !httpproxy.MatchesHost("www.google.com", []string{".google.com"}) {
		t.Error("leading-dot pattern must match a subdomain")
	}
}

func TestMatchesHost_PortStrippedCaseInsensitive(t *testing.T) {
	if !httpproxy.MatchesHost("GOOGLE.COM:443", []string{".google.com"}) {
		t.Error("port must be stripped and match must be case-insensitive")
	}
}

// This is the single most important assertion in this plan: the live bug in
// today's unanchored regex (`^(www\.)?google\.com`) matches an
// attacker-controlled suffix appended to the declared host. MatchesHost must
// NOT reproduce that bug.
func TestMatchesHost_SuffixExtensionDoesNotMatch(t *testing.T) {
	if httpproxy.MatchesHost("google.com.evil.example", []string{".google.com"}) {
		t.Error("a suffix-extended lookalike host must NOT match — this is the bug fix this plan exists for")
	}
}

func TestMatchesHost_UnrelatedHostDoesNotMatch(t *testing.T) {
	if httpproxy.MatchesHost("notgoogle.com", []string{".google.com"}) {
		t.Error("an unrelated host sharing a suffix substring must not match")
	}
}

func TestMatchesHost_WildcardNotHonoured(t *testing.T) {
	if httpproxy.MatchesHost("anything.example", []string{"*"}) {
		t.Error(`"*" must NOT be honoured as a match-all for intercepts`)
	}
}

func TestMatchesHost_BareEntryIsExact(t *testing.T) {
	if !httpproxy.MatchesHost("google.com", []string{"google.com"}) {
		t.Error("a bare entry must match its own exact host")
	}
	if httpproxy.MatchesHost("www.google.com", []string{"google.com"}) {
		t.Error("a bare entry must NOT match a subdomain")
	}
}

// ---------------------------------------------------------------------------
// MatchIntercept
// ---------------------------------------------------------------------------

func TestMatchIntercept_FirstMatchWins(t *testing.T) {
	ics := []httpproxy.Intercept{
		{Name: "first", Hosts: []string{"api.example.com"}, Redirect: "https://one.example/"},
		{Name: "second", Hosts: []string{"api.example.com"}, Redirect: "https://two.example/"},
	}
	got := httpproxy.MatchIntercept("api.example.com", ics)
	if got == nil || got.Name != "first" {
		t.Fatalf("MatchIntercept = %+v, want the first declared match", got)
	}
}

// ---------------------------------------------------------------------------
// ParseIntercepts
// ---------------------------------------------------------------------------

func encodeIntercepts(t *testing.T, jsonBody string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString([]byte(jsonBody))
}

func TestParseIntercepts_RoundTripRedirectAndRespond(t *testing.T) {
	body := `[
		{"name":"rickroll","hosts":[".google.com"],"redirect":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{"name":"chaos","hosts":["api.example.com"],"respond":{"status":503,"contentType":"text/plain","body":"maintenance window"}}
	]`
	ics, err := httpproxy.ParseIntercepts(encodeIntercepts(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ics) != 2 {
		t.Fatalf("got %d intercepts, want 2", len(ics))
	}
	if ics[0].Name != "rickroll" || ics[0].Redirect != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("intercept 0 = %+v, redirect fields did not round-trip", ics[0])
	}
	if len(ics[0].Hosts) != 1 || ics[0].Hosts[0] != ".google.com" {
		t.Errorf("intercept 0 hosts = %+v, want [.google.com]", ics[0].Hosts)
	}
	if ics[1].Name != "chaos" || ics[1].Respond == nil {
		t.Fatalf("intercept 1 = %+v, respond fields did not round-trip", ics[1])
	}
	if ics[1].Respond.Status != 503 || ics[1].Respond.ContentType != "text/plain" || ics[1].Respond.Body != "maintenance window" {
		t.Errorf("intercept 1 respond = %+v, fields did not round-trip exactly", ics[1].Respond)
	}
}

// A respond body with interior spaces AND an embedded newline must survive
// the base64+JSON round trip byte for byte. A body round-trip with no
// whitespace in it proves nothing about the transport this design chose
// base64 for (systemd splits an unquoted Environment= on whitespace).
func TestParseIntercepts_RespondBodyWithSpacesAndNewlineSurvives(t *testing.T) {
	wantBody := "line one has spaces\nline two also has spaces\n"
	ics := []httpproxy.Intercept{
		{
			Name:  "spacey",
			Hosts: []string{"api.example.com"},
			Respond: &httpproxy.Respond{
				Status: 200,
				Body:   wantBody,
			},
		},
	}
	// Round-trip through JSON marshal (simulating the compiler side) then
	// through ParseIntercepts (the sidecar side).
	jsonBody := mustMarshalIntercepts(t, ics)
	got, err := httpproxy.ParseIntercepts(encodeIntercepts(t, jsonBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d intercepts, want 1", len(got))
	}
	if got[0].Respond == nil || got[0].Respond.Body != wantBody {
		t.Errorf("respond body = %q, want %q (byte for byte)", got[0].Respond.Body, wantBody)
	}
}

func TestParseIntercepts_GarbageBase64ReturnsErrorNoPanic(t *testing.T) {
	ics, err := httpproxy.ParseIntercepts("!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected an error for garbage base64, got nil")
	}
	if ics != nil {
		t.Errorf("expected nil slice on decode failure, got %+v", ics)
	}
}

func TestParseIntercepts_ValidBase64InvalidJSONReturnsError(t *testing.T) {
	notJSON := base64.StdEncoding.EncodeToString([]byte("this is not json"))
	ics, err := httpproxy.ParseIntercepts(notJSON)
	if err == nil {
		t.Fatal("expected an error for valid base64 / invalid JSON, got nil")
	}
	if ics != nil {
		t.Errorf("expected nil slice on unmarshal failure, got %+v", ics)
	}
}

func TestParseIntercepts_EmptyStringReturnsNilNoError(t *testing.T) {
	ics, err := httpproxy.ParseIntercepts("")
	if err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
	if ics != nil {
		t.Errorf("expected nil slice for empty string, got %+v", ics)
	}
}

func TestParseIntercepts_DropsUnsafeEntriesKeepsWellFormed(t *testing.T) {
	body := `[
		{"name":"good","hosts":["api.example.com"],"redirect":"https://good.example/"},
		{"name":"zero-hosts","hosts":[],"redirect":"https://x.example/"},
		{"name":"neither-action","hosts":["x.example.com"]},
		{"name":"both-actions","hosts":["y.example.com"],"redirect":"https://y.example/","respond":{"status":200,"body":"x"}},
		{"name":"good2","hosts":["b.example.com"],"respond":{"status":200,"body":"ok"}}
	]`
	ics, err := httpproxy.ParseIntercepts(encodeIntercepts(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ics) != 2 {
		t.Fatalf("got %d intercepts, want 2 (only the well-formed entries): %+v", len(ics), ics)
	}
	names := map[string]bool{ics[0].Name: true, ics[1].Name: true}
	if !names["good"] || !names["good2"] {
		t.Errorf("expected good and good2 to survive, got %+v", ics)
	}
}

// ---------------------------------------------------------------------------
// InterceptResponse
// ---------------------------------------------------------------------------

func TestInterceptResponse_Redirect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://google.com/", nil)
	ic := &httpproxy.Intercept{Name: "rickroll", Hosts: []string{".google.com"}, Redirect: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}
	resp := httpproxy.InterceptResponse(req, ic)
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}
	if got := resp.Header.Get("Location"); got != ic.Redirect {
		t.Errorf("Location = %q, want %q", got, ic.Redirect)
	}
}

func TestInterceptResponse_RespondWithContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	ic := &httpproxy.Intercept{
		Name:  "chaos",
		Hosts: []string{"api.example.com"},
		Respond: &httpproxy.Respond{
			Status:      503,
			ContentType: "application/json",
			Body:        `{"error":"maintenance"}`,
		},
	}
	resp := httpproxy.InterceptResponse(req, ic)
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := readAllBody(t, resp)
	if body != `{"error":"maintenance"}` {
		t.Errorf("body = %q, want the exact declared body", body)
	}
}

func TestInterceptResponse_RespondDefaultsContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	ic := &httpproxy.Intercept{
		Name:  "chaos",
		Hosts: []string{"api.example.com"},
		Respond: &httpproxy.Respond{
			Status: 200,
			Body:   "hello",
		},
	}
	resp := httpproxy.InterceptResponse(req, ic)
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain default", ct)
	}
}
