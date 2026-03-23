// Package github provides GitHub App authentication helpers for the Klanker Maker sandbox system.
// It implements JWT generation, installation token exchange, permission compilation, and SSM storage.
package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/golang-jwt/jwt/v5"
)

// GitHubAPIBaseURL is the GitHub API base URL. It is a package-level variable so
// tests can point at an httptest server. Production code uses the default.
var GitHubAPIBaseURL = "https://api.github.com"

// ============================================================
// Narrow SSM interface
// ============================================================

// SSMAPI is a narrow interface for SSM operations needed by this package.
// Only PutParameter is required — the real *ssm.Client satisfies this interface directly.
// This follows the S3PutAPI, CWLogsAPI narrow-interface pattern established in Phase 3.
type SSMAPI interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// ============================================================
// JWT generation
// ============================================================

// GenerateGitHubAppJWT mints a short-lived RS256 JWT for authenticating as a GitHub App.
//
// Claims:
//   - iss: appClientID (GitHub App client ID)
//   - iat: now - 60s (60-second clock drift buffer as per GitHub docs)
//   - exp: now + 10 minutes (GitHub's maximum allowed expiry)
//
// privateKeyPEM supports both PKCS#1 (-----BEGIN RSA PRIVATE KEY-----) and
// PKCS#8 (-----BEGIN PRIVATE KEY-----) formats. Returns an error for non-RSA keys
// or invalid PEM.
func GenerateGitHubAppJWT(appClientID string, privateKeyPEM []byte) (string, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("github: failed to decode PEM block from private key")
	}

	var privateKey *rsa.PrivateKey

	// Try PKCS#1 first (-----BEGIN RSA PRIVATE KEY-----).
	pkcs1Key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		privateKey = pkcs1Key
	} else {
		// Fall back to PKCS#8 (-----BEGIN PRIVATE KEY-----).
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", fmt.Errorf("github: parse private key (PKCS#1: %v; PKCS#8: %v)", err, err2)
		}
		rsaKey, ok := pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("github: private key is not an RSA key (got %T)", pkcs8Key)
		}
		privateKey = rsaKey
	}

	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    appClientID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("github: sign JWT: %w", err)
	}
	return signed, nil
}

// ============================================================
// Installation token exchange
// ============================================================

// tokenRequest is the JSON body sent to the GitHub API for token creation.
type tokenRequest struct {
	Repositories []string          `json:"repositories,omitempty"`
	Permissions  map[string]string `json:"permissions,omitempty"`
}

// tokenResponse is the JSON body returned by GitHub's installation token endpoint.
type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ExchangeForInstallationToken exchanges a GitHub App JWT for a scoped installation
// access token via POST /app/installations/{id}/access_tokens.
//
// repos should be in "org/repo" or bare "repo" format; org prefixes are stripped
// before sending (GitHub expects short names only).
// perms is a map of permission name to level, e.g. {"contents": "read"}.
//
// Returns the token string on 201 Created, or an error that includes the HTTP
// status code for non-201 responses.
func ExchangeForInstallationToken(ctx context.Context, jwtToken, installationID string, repos []string, perms map[string]string) (string, error) {
	shortNames := make([]string, len(repos))
	for i, r := range repos {
		shortNames[i] = repoShortName(r)
	}

	reqBody := tokenRequest{
		Repositories: shortNames,
		Permissions:  perms,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("github: marshal token request: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", GitHubAPIBaseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("github: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: token exchange request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("github: read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github: token exchange returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("github: unmarshal token response: %w", err)
	}
	if tokenResp.Token == "" {
		return "", fmt.Errorf("github: token exchange returned empty token")
	}
	return tokenResp.Token, nil
}

// repoShortName strips the org prefix from an "org/repo" format string.
// If there is no slash, the string is returned unchanged.
func repoShortName(repo string) string {
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		return repo[idx+1:]
	}
	return repo
}

// ============================================================
// Permission compilation
// ============================================================

// CompilePermissions maps profile permission names (clone, fetch, push) to
// GitHub API permission map entries.
//
// Mapping:
//   - clone or fetch → contents: read
//   - push → contents: write (supersedes read)
func CompilePermissions(permissions []string) map[string]string {
	result := make(map[string]string)
	for _, p := range permissions {
		switch p {
		case "push":
			result["contents"] = "write"
		case "clone", "fetch":
			// Only set read if write hasn't been set yet.
			if result["contents"] != "write" {
				result["contents"] = "read"
			}
		}
	}
	return result
}

// ============================================================
// SSM token storage
// ============================================================

// WriteTokenToSSM writes a GitHub installation token to SSM Parameter Store at
// /sandbox/{sandboxID}/github-token as a SecureString encrypted with kmsKeyARN.
//
// When overwrite is true, an existing parameter is updated in place (used for refresh).
// When overwrite is false, creation fails if the parameter already exists.
func WriteTokenToSSM(ctx context.Context, client SSMAPI, sandboxID, token, kmsKeyARN string, overwrite bool) error {
	paramName := fmt.Sprintf("/sandbox/%s/github-token", sandboxID)
	_, err := client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      awssdk.String(paramName),
		Value:     awssdk.String(token),
		Type:      ssmtypes.ParameterTypeSecureString,
		KeyId:     awssdk.String(kmsKeyARN),
		Overwrite: awssdk.Bool(overwrite),
	})
	if err != nil {
		return fmt.Errorf("github: write token to SSM %q: %w", paramName, err)
	}
	return nil
}

// ============================================================
// Lambda handler types
// ============================================================

// TokenRefreshEvent is the EventBridge Scheduler payload delivered to the
// GitHub token refresher Lambda.
type TokenRefreshEvent struct {
	SandboxID      string   `json:"sandbox_id"`
	InstallationID string   `json:"installation_id"`
	SSMParamName   string   `json:"ssm_parameter_name"`
	KMSKeyARN      string   `json:"kms_key_arn"`
	AllowedRepos   []string `json:"allowed_repos"`
	Permissions    []string `json:"permissions"`
}

// GenerateJWTFunc is a function type for JWT generation, allowing DI in tests.
type GenerateJWTFunc func(appClientID string, privateKeyPEM []byte) (string, error)

// TokenRefreshHandler holds injected dependencies for the Lambda token refresher.
// When GenerateJWTFn is nil, the real GenerateGitHubAppJWT is used.
type TokenRefreshHandler struct {
	SSMClient     SSMAPI
	Logger        *slog.Logger
	AppClientID   string
	PrivateKeyPEM []byte
	// GenerateJWTFn allows tests to inject a stub JWT generator.
	GenerateJWTFn GenerateJWTFunc
}

// HandleTokenRefresh is the Lambda handler method. It:
//  1. Compiles permissions from the event's Permissions list.
//  2. Generates a GitHub App JWT.
//  3. Exchanges the JWT for an installation token scoped to AllowedRepos.
//  4. Writes the token to SSM.
//  5. Logs a structured audit event to CloudWatch (via slog JSON to stdout).
//
// On success, logs {"event": "token_generated", "sandbox_id": ..., "allowed_repos": ..., "permissions": ...}.
// On failure, logs {"event": "token_generation_failed", "sandbox_id": ..., "error": ..., "allowed_repos": ...}
// and returns the error.
func (h *TokenRefreshHandler) HandleTokenRefresh(ctx context.Context, event TokenRefreshEvent) error {
	log := h.Logger
	if log == nil {
		log = slog.Default()
	}

	sandboxID := event.SandboxID
	compiledPerms := CompilePermissions(event.Permissions)

	// Step 1: Generate GitHub App JWT.
	generateJWT := h.GenerateJWTFn
	if generateJWT == nil {
		generateJWT = GenerateGitHubAppJWT
	}
	jwtToken, err := generateJWT(h.AppClientID, h.PrivateKeyPEM)
	if err != nil {
		log.Error("token_generation_failed",
			slog.String("event", "token_generation_failed"),
			slog.String("sandbox_id", sandboxID),
			slog.String("error", err.Error()),
			slog.Any("allowed_repos", event.AllowedRepos),
		)
		return fmt.Errorf("github-token-refresher: generate JWT: %w", err)
	}

	// Step 2: Exchange JWT for installation token.
	token, err := ExchangeForInstallationToken(ctx, jwtToken, event.InstallationID, event.AllowedRepos, compiledPerms)
	if err != nil {
		log.Error("token_generation_failed",
			slog.String("event", "token_generation_failed"),
			slog.String("sandbox_id", sandboxID),
			slog.String("error", err.Error()),
			slog.Any("allowed_repos", event.AllowedRepos),
		)
		return fmt.Errorf("github-token-refresher: exchange for installation token: %w", err)
	}

	// Step 3: Write token to SSM.
	paramName := event.SSMParamName
	if paramName == "" {
		paramName = fmt.Sprintf("/sandbox/%s/github-token", sandboxID)
	}
	if err := WriteTokenToSSM(ctx, h.SSMClient, sandboxID, token, event.KMSKeyARN, true); err != nil {
		log.Error("token_generation_failed",
			slog.String("event", "token_generation_failed"),
			slog.String("sandbox_id", sandboxID),
			slog.String("error", err.Error()),
			slog.Any("allowed_repos", event.AllowedRepos),
		)
		return fmt.Errorf("github-token-refresher: write token to SSM: %w", err)
	}

	// Step 4: Log success audit event.
	log.Info("token_generated",
		slog.String("event", "token_generated"),
		slog.String("sandbox_id", sandboxID),
		slog.Any("allowed_repos", event.AllowedRepos),
		slog.Any("permissions", compiledPerms),
		slog.Bool("success", true),
	)
	return nil
}

// ============================================================
// MockSSMClient — test double exported for use in _test packages
// ============================================================

// MockSSMClient is a test double for SSMAPI that records PutParameter calls.
// Exported so that external test packages (cmd/github-token-refresher tests) can use it.
type MockSSMClient struct {
	PutParameterCallCount int
	LastName              string
	LastValue             string
	LastKeyID             string
	LastOverwrite         bool
	Err                   error
}

// PutParameter records the call and returns the configured error.
func (m *MockSSMClient) PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.PutParameterCallCount++
	if input.Name != nil {
		m.LastName = *input.Name
	}
	if input.Value != nil {
		m.LastValue = *input.Value
	}
	if input.KeyId != nil {
		m.LastKeyID = *input.KeyId
	}
	if input.Overwrite != nil {
		m.LastOverwrite = *input.Overwrite
	}
	if m.Err != nil {
		return nil, m.Err
	}
	return &ssm.PutParameterOutput{}, nil
}
