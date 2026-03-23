package github_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/github"
)

// testPKCS1Key is a 2048-bit RSA private key in PKCS#1 PEM format (-----BEGIN RSA PRIVATE KEY-----).
// Generated with: openssl genrsa -traditional 2048
const testPKCS1Key = `-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA0JBz385TWMQyU9SPc1kzyVf+uPSk2ugL3WbItJjpbbAW1/Gm
RbVgzsV/OQvFXI/kdBWTXPqLf4jb1cvEds2y/me5G7Xw2NfUKhT5DIdqUPa0/mFo
J5IP0znkjOUuKirJZ7grlz8+Fz8Jb+qLSubfJi6EeGq8FrSSz5tdCGrOjWd+be8e
a62phWl7grBgEP1Mt3AiIExPcIiiJ/VrrOf3PA97v5BCCWyfNNsIzADbfjcrDoae
XHlGNEmWvhlYWuguyq3xp5JKDKEYi7i701n0BlOT7m0Va3gRMXSsq96duMyJA49g
xWaXx5bn+rjBWiR0WzMw6r/KkkY2ZLdzCTCwCwIDAQABAoIBABI+OGyAqyiuDKrp
gly4FkAL2uOJvqvJNVR5659karKl/vGHmSAq09tySgUO4wYMLhL7Wib9YVtX+Ma0
KqyrpHb9UqM6YGVPR6cgq4ItjB6l3cIMeiRP8eNrgnLSjx2wHWrdoK57oS4+XNB1
xxZXqHg6BGtWBrrDs73GHLaiRQv4M2ph8BYmwb3eqb/4zGsQHKB6LUL8adycJ9/C
hioUbpooOdUh28BkFgURuSvGiDnZprgSIYuN414khk0wiB0SgeLnj11rt+MTOqMu
LepyubiSGBDCfDuooWIPwa/qzc55uneoZhChmSafWFyfY3kVPMxxiVJKbJDynBjC
jgerdAkCgYEA/lA43vXwo3fdqWQKf6ciLNFzqb4BIj2JRP4QXYnY2YMc41aAjGtn
ndw3aGYVntQ2yGd/QGp0cxk07tZkNuk+Kc0KyWANSJ86MO9IXohEkV8NLlgfsGd1
R7juJG9cQC+9A0/yUd8B15qaWbPq33rahmiZJ4B5FZi2S0V3ygajRgkCgYEA0fKO
jHIbE9L7XskJfYwyQ/pQcDRPj0+fTldQ7d393sBNLxG/Q4zsRNbgIH+nq3y/F8Bs
YJ3XlwR9HNTkTEQboWLEWSh02RxX2Hnnh3HF6NYhDpQFw5gaDUCnDd3blNTX1jO+
ByaJeBDrXP+QaCMAsjt30TLpfY94VlQd12Y06nMCgYEA5zKPQUNdbX8/aQul779F
9nDEMgCmjzZaYPqIbkEvfy8PSC4P15idLopRZPvJlAdhdneA3FLrYuf7k1Yc7T7G
YMIjmEdWTDtVb79Zj3davr4nAYbj6D9mA7o/5afHuiKsNyKrLXsL9bJ7uCk00c6i
c6cL9Tl62wNmVq/k4yl9reECgYEAiUKavenQGCrlGzg2kzV4m4bo1iLtLRXyYkal
644qb1qsW6yvrltREUSmrbioB278hGvSr2wiymIt5g6t38rbgazQEDZqBpQIPsic
fan9qVdtr1lJV3J2/dkaHu/AotJw9mNHxucEE1KEfn27jMntp5lHoac2jlehZleC
VxREXk8CgYEA29MEOeTqHN2qintyI0FttQWibO9VdkJCPqVfLVP+G+D+DYaIFDkZ
gf7VBFrYp3PxWNItUWTrieXtoBh3NGIwR0Pnl9Q9q7+/zyZesYlawLtTJJK/POun
pN2+lbPJngzdhELx85AcznjgAAp+H5yG9QG7TehOhQLnoWkvFo6zP/8=
-----END RSA PRIVATE KEY-----`

// testPKCS8Key is a 2048-bit RSA private key in PKCS#8 PEM format (-----BEGIN PRIVATE KEY-----).
const testPKCS8Key = `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDAFZGTf+ncxdgi
a60r1GJAA1+pH9p6AVGFXziHxEQAfWB/HbrV5dVb5CkP+tHKTCQPC+05iNsXlpNg
/+k1YC1KSTQy6Mb/e3RMXToW0P1a0A/MfBTsFK10m0eXLn4m2tBC441iMAzQNA2Y
bnhBPVAfyCYKuNpLQSE9zne/P6uJS2E2kNIhEKuYt/i/s1tQKCCX+0bH/UEvx1/y
80ypuNFVf8oKzdm07Y/csQtrDZ5V9Nml2XntvwhmU3WCcpwRV4J0E2QXad4i3spL
271G2QRQfcK5sEo2ofrnLmK18tzm/6P7S1iG6kIR24KAqdh9mzzKR4IwZo732vUe
xQ+PthYdAgMBAAECggEAB3jvVFseCGoj6dbhgjp9ZfsbnhMwpx1lA/i71MBEWvaF
qfxIl+icf7olBMufnKyQnIs8u8fAqi6/5f6enmZy7JhjcPWQREETuaLIzzlrGbzN
WImdML/vLYGa690xqCZMAsYiambvIKxduQfXIsaVVt8s567gQxwyBFsDWovKCwmz
6gZ+kNlA62Gtqv8nCr5AK7QhRaGaIKPuOhz/0rAkUmbTb4rBTij48UxsaB2ivryL
aE5J2cS0NLGRYaW6eOvZsoWcCJNLd5PxWqMmswEjaiON3oIcGj+CaL8qK9zNLPOL
ODfCEW5Mw6nLQ7575HojtAUh1Ec21oe2ntsdvNscCwKBgQDfHvfeDutNuNqfewi9
9MuSJk7QOHERHDT9tLBsoW7sQu4wjpHmS1vtG1wc8A04bSkGzp6LkcERgds4H8Ei
fVHm37NjBRYQAb8BrSpe6+gWdLLW11dnDEISLlfFov3Vyhc3L2RABCmPYj1jLLto
c8P/VfquKNEoTpaGAB4VEO3PowKBgQDcY8Tf2okzCQYPjGDok2mEqJGa++eWOJ4y
CobCiu2QrAiNwBfrFjL8v0lp7tKGGqv6hLjhzVn+lZGWE+i9V1c7C+JvAQYILiL0
FxYMQL/8ioMtJUYZZvZCkJ2wMyRRZG3nxlUEnDhrZbVs14vepWAiLSzDBNFiq58w
9TVnPnvfPwKBgHA8fMU4Tgd30Ine8yPS60BmgsjdS4sm3EUvSnwqrMiuVnEYlq35
BJH+bFSmMJBM4RFqiHh+5lbvMp5F4vp9feCccPmDinic2D94o1LCaqo5I+lMw8uz
b90DcOWbOwL7OLhq34wQS/OzoFuuGcvOSC6+Sm6nW6dh+PgJQRipvmbvAoGBAJZm
L5BkXoTJf61Uqz2Me9HgB52wktZdRPf5XwWcMYstG5lAohH4UEtTbxIvvNNvmDWa
JWFS9jtabsPwSkAMPqc48Qm3tRoYAhp5Nr6d4WbCT8qbST9EmIHMlxALlplE5Avr
uVEwGwCPpEPmxLjoOraYBZgAzbN8U2Lhs9QFPBuZAoGBAKkXDfxRaNmN2oG+6o65
Xmfu6yYhkOf1auAORRJiZwMKWaoQdFNzpKXL77Ehld+Ma1L9P5qTNRTUvXwSmFpe
05qjZHF2d61kGi2j+TSvT73oLdZACi46ryZMJYl+l8M7BlDZsjLHW6lLI0aE96GJ
qqreqthpUdB+jHzV62pD6++0
-----END PRIVATE KEY-----`

// testECKey is an EC private key in PKCS#8 PEM format — used to verify non-RSA rejection.
const testECKey = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgZpPlaNfnLENE5SRr
T69mpbwznU/5mFyVnbFf66WGCoWhRANCAATiBRJ7FWtb2hKkgE+cgH3azDNpzrHq
4fOPexrtcg/6XL0P5WRPE/s1BzdWtesaz6b7mnf8PZF+cMMXdRb+ANn3
-----END PRIVATE KEY-----`

// decodeJWTPayload decodes the middle segment of a JWT token.
func decodeJWTPayload(tok string) (map[string]interface{}, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected 3 JWT parts, got %d", len(parts))
	}
	payload := parts[1]
	// Add padding for base64url decoding.
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64url decode: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return claims, nil
}

// ============================================================
// GenerateGitHubAppJWT tests
// ============================================================

func TestGenerateGitHubAppJWT_PKCS1(t *testing.T) {
	tok, err := github.GenerateGitHubAppJWT("app-client-id-123", []byte(testPKCS1Key))
	if err != nil {
		t.Fatalf("GenerateGitHubAppJWT with PKCS#1 key: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty JWT token")
	}
	// A JWT has exactly two dots separating three base64url parts.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with 3 parts, got %d: %q", len(parts), tok)
	}
}

func TestGenerateGitHubAppJWT_PKCS8(t *testing.T) {
	tok, err := github.GenerateGitHubAppJWT("app-client-id-456", []byte(testPKCS8Key))
	if err != nil {
		t.Fatalf("GenerateGitHubAppJWT with PKCS#8 key: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty JWT token")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with 3 parts, got %d", len(parts))
	}
}

func TestGenerateGitHubAppJWT_NonRSAKey(t *testing.T) {
	_, err := github.GenerateGitHubAppJWT("app-id", []byte(testECKey))
	if err == nil {
		t.Fatal("expected error for non-RSA key, got nil")
	}
}

func TestGenerateGitHubAppJWT_InvalidPEM(t *testing.T) {
	_, err := github.GenerateGitHubAppJWT("app-id", []byte("not a valid pem"))
	if err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
}

func TestGenerateGitHubAppJWT_Claims(t *testing.T) {
	tok, err := github.GenerateGitHubAppJWT("my-app-client-id", []byte(testPKCS1Key))
	if err != nil {
		t.Fatalf("GenerateGitHubAppJWT: %v", err)
	}

	claims, err := decodeJWTPayload(tok)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if claims["iss"] != "my-app-client-id" {
		t.Errorf("expected iss=my-app-client-id, got %v", claims["iss"])
	}

	now := float64(time.Now().Unix())
	iat, ok := claims["iat"].(float64)
	if !ok {
		t.Fatal("iat claim missing or not a number")
	}
	// iat should be ~60 seconds in the past (within 10 seconds tolerance).
	if iat > now-50 || iat < now-70 {
		t.Errorf("iat %v not in expected range (now-70, now-50); now=%v", iat, now)
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatal("exp claim missing or not a number")
	}
	// exp should be ~10 minutes in the future.
	if exp < now+550 || exp > now+650 {
		t.Errorf("exp %v not in expected range (now+550, now+650); now=%v", exp, now)
	}
}

// ============================================================
// CompilePermissions tests
// ============================================================

func TestCompilePermissions_Clone(t *testing.T) {
	perms := github.CompilePermissions([]string{"clone"})
	if perms["contents"] != "read" {
		t.Errorf("expected contents=read for clone, got %v", perms["contents"])
	}
}

func TestCompilePermissions_Fetch(t *testing.T) {
	perms := github.CompilePermissions([]string{"fetch"})
	if perms["contents"] != "read" {
		t.Errorf("expected contents=read for fetch, got %v", perms["contents"])
	}
}

func TestCompilePermissions_Push(t *testing.T) {
	perms := github.CompilePermissions([]string{"push"})
	if perms["contents"] != "write" {
		t.Errorf("expected contents=write for push, got %v", perms["contents"])
	}
}

func TestCompilePermissions_ClonePush_WriteSupersedes(t *testing.T) {
	perms := github.CompilePermissions([]string{"clone", "push"})
	if perms["contents"] != "write" {
		t.Errorf("expected contents=write for clone+push, got %v", perms["contents"])
	}
}

func TestCompilePermissions_CloneFetch(t *testing.T) {
	perms := github.CompilePermissions([]string{"clone", "fetch"})
	if perms["contents"] != "read" {
		t.Errorf("expected contents=read for clone+fetch, got %v", perms["contents"])
	}
}

// ============================================================
// ExchangeForInstallationToken tests (httptest stub)
// ============================================================

func TestExchangeForInstallationToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/app/installations/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing or invalid Authorization header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("unexpected Accept: %s", r.Header.Get("Accept"))
		}
		if r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("unexpected X-GitHub-Api-Version: %s", r.Header.Get("X-GitHub-Api-Version"))
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if _, ok := body["repositories"]; !ok {
			t.Errorf("missing repositories in request body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_test_token_abc123","expires_at":"2026-03-23T04:00:00Z"}`)
	}))
	defer server.Close()

	github.GitHubAPIBaseURL = server.URL

	tok, err := github.ExchangeForInstallationToken(
		context.Background(),
		"eyJmakeJWT",
		"12345678",
		[]string{"org/my-repo", "org/other-repo"},
		map[string]string{"contents": "read"},
	)
	if err != nil {
		t.Fatalf("ExchangeForInstallationToken: %v", err)
	}
	if tok != "ghs_test_token_abc123" {
		t.Errorf("expected token ghs_test_token_abc123, got %q", tok)
	}
}

func TestExchangeForInstallationToken_NonCreatedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	defer server.Close()

	github.GitHubAPIBaseURL = server.URL

	_, err := github.ExchangeForInstallationToken(
		context.Background(),
		"bad-jwt",
		"12345678",
		[]string{"my-repo"},
		map[string]string{"contents": "read"},
	)
	if err == nil {
		t.Fatal("expected error for non-201 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to contain 401, got: %v", err)
	}
}

func TestExchangeForInstallationToken_RepoShortNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		repos, ok := body["repositories"].([]interface{})
		if !ok {
			t.Errorf("repositories not an array: %T", body["repositories"])
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, repo := range repos {
			name, _ := repo.(string)
			if strings.Contains(name, "/") {
				t.Errorf("repo name should be short (no org prefix), got: %q", name)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"tok","expires_at":"2026-03-23T04:00:00Z"}`)
	}))
	defer server.Close()

	github.GitHubAPIBaseURL = server.URL

	_, err := github.ExchangeForInstallationToken(
		context.Background(),
		"jwt",
		"999",
		[]string{"org/repo-one", "org/repo-two"},
		map[string]string{"contents": "read"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============================================================
// WriteTokenToSSM tests
// ============================================================

func TestWriteTokenToSSM_Success(t *testing.T) {
	mock := &github.MockSSMClient{}
	err := github.WriteTokenToSSM(context.Background(), mock, "sb-abc123", "ghs_token", "arn:aws:kms:us-east-1:123:key/abc", true)
	if err != nil {
		t.Fatalf("WriteTokenToSSM: %v", err)
	}
	if mock.PutParameterCallCount != 1 {
		t.Errorf("expected 1 PutParameter call, got %d", mock.PutParameterCallCount)
	}
	if mock.LastName != "/sandbox/sb-abc123/github-token" {
		t.Errorf("expected path /sandbox/sb-abc123/github-token, got %q", mock.LastName)
	}
	if mock.LastValue != "ghs_token" {
		t.Errorf("expected value ghs_token, got %q", mock.LastValue)
	}
}

func TestWriteTokenToSSM_Error(t *testing.T) {
	mock := &github.MockSSMClient{Err: fmt.Errorf("ssm unavailable")}
	err := github.WriteTokenToSSM(context.Background(), mock, "sb-xyz", "tok", "arn:aws:kms:us-east-1:123:key/xyz", true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================
// Lambda handler audit log tests
// ============================================================

func TestLambdaHandler_AuditLog_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	mock := &github.MockSSMClient{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_audit_test","expires_at":"2026-03-23T04:00:00Z"}`)
	}))
	defer server.Close()
	github.GitHubAPIBaseURL = server.URL

	h := &github.TokenRefreshHandler{
		SSMClient: mock,
		Logger:    logger,
		GenerateJWTFn: func(appClientID string, privateKeyPEM []byte) (string, error) {
			return "mock-jwt-token", nil
		},
		PrivateKeyPEM: []byte(testPKCS1Key),
		AppClientID:   "test-app-id",
	}

	event := github.TokenRefreshEvent{
		SandboxID:      "sb-test-001",
		InstallationID: "12345",
		SSMParamName:   "/sandbox/sb-test-001/github-token",
		KMSKeyARN:      "arn:aws:kms:us-east-1:123:key/abc",
		AllowedRepos:   []string{"org/my-repo"},
		Permissions:    []string{"clone"},
	}

	err := h.HandleTokenRefresh(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleTokenRefresh: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "sb-test-001") {
		t.Errorf("expected sandbox_id in log output, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "token_generated") {
		t.Errorf("expected token_generated event in log output, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "my-repo") {
		t.Errorf("expected allowed_repos in log output, got: %s", logOutput)
	}
}

func TestLambdaHandler_AuditLog_Failure(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	mock := &github.MockSSMClient{Err: fmt.Errorf("ssm write failed")}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_fail_test","expires_at":"2026-03-23T04:00:00Z"}`)
	}))
	defer server.Close()
	github.GitHubAPIBaseURL = server.URL

	h := &github.TokenRefreshHandler{
		SSMClient: mock,
		Logger:    logger,
		GenerateJWTFn: func(appClientID string, privateKeyPEM []byte) (string, error) {
			return "mock-jwt", nil
		},
		PrivateKeyPEM: []byte(testPKCS1Key),
		AppClientID:   "test-app-id",
	}

	event := github.TokenRefreshEvent{
		SandboxID:      "sb-fail-001",
		InstallationID: "99999",
		SSMParamName:   "/sandbox/sb-fail-001/github-token",
		KMSKeyARN:      "arn:aws:kms:us-east-1:123:key/abc",
		AllowedRepos:   []string{"org/fail-repo"},
		Permissions:    []string{"clone"},
	}

	err := h.HandleTokenRefresh(context.Background(), event)
	if err == nil {
		t.Fatal("expected error when SSM write fails, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "token_generation_failed") {
		t.Errorf("expected token_generation_failed in log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "sb-fail-001") {
		t.Errorf("expected sandbox_id in failure log, got: %s", logOutput)
	}
}
