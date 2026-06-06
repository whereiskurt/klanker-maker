// Command km-slack-bridge is the Phase 63 Slack-notify Lambda, extended in
// Phase 67 to also handle POST /events from Slack Events API.
//
// Path-based dispatch:
//
//	POST /        → existing Phase 63 operator-signed envelope handler (bridge.Handler)
//	POST /events  → Phase 67 Slack Events API handler (bridge.EventsHandler)
//
// Cold start: reads env vars, builds AWS clients, wires production adapters
// into bridge.Handler and bridge.EventsHandler, and calls lambda.Start.
//
// Environment variables:
//
//	KM_IDENTITIES_TABLE     — DynamoDB table for public keys (default: km-identities)
//	KM_SANDBOX_TABLE_NAME   — DynamoDB table for sandbox metadata (default: km-sandboxes)
//	KM_NONCE_TABLE          — DynamoDB table for nonce replay protection (default: km-slack-bridge-nonces)
//	KM_BOT_TOKEN_PATH       — SSM parameter path for Slack bot token (default: /km/slack/bot-token)
//	KM_SIGNING_SECRET_PATH  — SSM parameter path for Slack signing secret (default: /km/slack/signing-secret)
//	KM_SLACK_THREADS_TABLE  — DynamoDB table for Slack thread tracking (default: km-slack-threads)
//	KM_RESOURCE_PREFIX      — resource prefix for SQS queue name pattern (default: km)
//	KM_ARTIFACTS_BUCKET     — S3 bucket holding transcript objects for ActionUpload (Phase 68; required for upload path, otherwise upload returns 502)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	pkgslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// handler is the global Phase 63 bridge.Handler, constructed once per cold start.
var handler *bridge.Handler

// eventsHandler is the global Phase 67 bridge.EventsHandler for POST /events.
// It may be nil when KM_SLACK_THREADS_TABLE or KM_SIGNING_SECRET_PATH are missing
// (backward-compat: existing Phase 63 sandboxes don't need inbound support).
var eventsHandler *bridge.EventsHandler

// Package-level AWS clients captured in init() and reused by wireEventsHandler().
// Splitting init() from wireEventsHandler() keeps the env validation (os.Exit)
// out of init() so test builds can exercise resolveThreadsTable without the
// Lambda cold-start env requirements.
var (
	initDDB        *dynamodb.Client
	initSSMC       *ssm.Client
	initS3Client   *s3.Client
	initSQSClient  *sqs.Client
	initPoster     *bridge.SlackPosterAdapter
	initToken      *bridge.SSMBotTokenFetcher
	initHTTPClient *http.Client
	initNonces     *bridge.DynamoNonceStore
)

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-slack-bridge: load AWS config: %v", err)
	}

	initDDB = dynamodb.NewFromConfig(cfg)
	initSSMC = ssm.NewFromConfig(cfg)

	// Defaults derive from KM_RESOURCE_PREFIX so a non-default install
	// (resource_prefix=kph) gets prefix-correct fallbacks (kph-identities,
	// kph-sandboxes, /kph/slack/bot-token) even if any specific env var
	// is accidentally not set by the Lambda terraform.
	prefix := resourcePrefix()
	identitiesTable := envOr("KM_IDENTITIES_TABLE", prefix+"-identities")
	sandboxesTable := envOr("KM_SANDBOX_TABLE_NAME", prefix+"-sandboxes")
	nonceTable := envOr("KM_NONCE_TABLE", prefix+"-slack-bridge-nonces")
	botTokenPath := envOr("KM_BOT_TOKEN_PATH", "/"+prefix+"/slack/bot-token")

	keys := &bridge.DynamoPublicKeyFetcher{Client: initDDB, TableName: identitiesTable}
	initNonces = &bridge.DynamoNonceStore{Client: initDDB, TableName: nonceTable}
	channels := &bridge.DynamoChannelOwnershipFetcher{Client: initDDB, TableName: sandboxesTable}
	initToken = &bridge.SSMBotTokenFetcher{Client: initSSMC, Path: botTokenPath}

	// Phase 75: CheckRedirect=ErrUseLastResponse prevents Go's stdlib from
	// stripping the Authorization header on cross-host 302 redirects
	// (files.slack.com → files-edge.slack-edge.com). S3FileDownloader.downloadOneFile
	// handles 302s manually and re-attaches Authorization on the follow-up GET.
	// See .planning/phases/75-.../75-RESEARCH.md § Pitfall 1.
	// Slack API methods (chat.postMessage, reactions.add, auth.test) return JSON
	// directly with no redirects expected, so disabling auto-redirect is safe for
	// all Phase 63/67/68 paths that also share this client.
	initHTTPClient = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// SlackPosterAdapter posts messages via the Slack Web API using the token
	// fetched lazily (and cached for 15 min) by SSMBotTokenFetcher.
	initPoster = &bridge.SlackPosterAdapter{
		HTTPClient: initHTTPClient,
		Tokens:     initToken,
	}

	handler = &bridge.Handler{
		Now:      time.Now,
		Keys:     keys,
		Nonces:   initNonces,
		Channels: channels,
		Token:    initToken,
		Slack:    initPoster,
	}

	// ==============================================================
	// Phase 68 — ActionUpload wiring: S3 getter + Slack file uploader
	// + cold-start files:write scope probe. KM_ARTIFACTS_BUCKET is the
	// transcript object store (already used by km-mail/km-slack); when
	// unset, the upload path returns 502 at runtime (the adapter still
	// constructs cleanly).
	// ==============================================================
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if artifactsBucket == "" {
		slog.Warn("km-slack-bridge: KM_ARTIFACTS_BUCKET not set; ActionUpload will fail at runtime",
			"path", "init", "remediation", "set KM_ARTIFACTS_BUCKET in Lambda env")
	}
	initS3Client = s3.NewFromConfig(cfg)
	initSQSClient = sqs.NewFromConfig(cfg)
	handler.S3Getter = &bridge.S3GetterAdapter{
		Client: initS3Client,
		Bucket: artifactsBucket,
	}

	// SlackFileUploaderAdapter wraps a pkg/slack.Client; the bot token is
	// fetched once at cold start (via the same SSM cache as SlackPosterAdapter).
	// On token rotation, the operator force-cold-starts the Lambda
	// (`km slack rotate-token`) — this matches Phase 63 behavior exactly.
	uploadToken, tokErr := initToken.Fetch(ctx)
	if tokErr != nil {
		// We do NOT log.Fatalf here — Phase 63 paths (post/archive/test)
		// must keep working even if the token fetch fails at cold start
		// (e.g. transient SSM unavailability). The upload path will
		// surface bot_token_unavailable through Step 7 of Handle.
		slog.Warn("km-slack-bridge: cold-start token fetch failed; upload adapter and scope probe disabled",
			"path", "init", "err", tokErr.Error())
	} else {
		uploadClient := pkgslack.NewClient(uploadToken, initHTTPClient)
		handler.FileUploader = &bridge.SlackFileUploaderAdapter{Client: uploadClient}

		// Cold-start scope probe (RESEARCH Open Question 2 resolution).
		// Use a dedicated raw HTTP probe at "https://slack.com" so we don't
		// have to extend SlackPosterAdapter.call() to surface the X-OAuth-Scopes
		// response header. On probe failure (network blip, scopes header
		// absent), default to missing=false — let the per-request Slack
		// response surface the real error rather than hard-blocking all uploads.
		missing, probeErr := probeFilesWriteScope(ctx, "https://slack.com", uploadToken)
		if probeErr != nil {
			slog.Warn("km-slack-bridge: scope probe failed; falling back to per-request scope errors",
				"path", "init", "err", probeErr.Error())
		}
		handler.MissingFilesWrite = missing
		slog.Info("km-slack-bridge: files:write scope probe",
			"missing_files_write", missing,
			"probe_err", probeErr,
			"KM_ARTIFACTS_BUCKET", artifactsBucket,
		)
	}

}

func main() {
	// ==============================================================
	// Phase 67-05: EventsHandler wiring
	// KM_SLACK_THREADS_TABLE is required — missing it would silently
	// cross-route to a stale "km-slack-threads" default belonging to a
	// different install. Hard-fail before lambda.Start so the cold-start
	// log clearly identifies the misconfiguration.
	//
	// This check is in main() rather than init() so that test builds
	// (which do not call main()) can exercise the pure resolveThreadsTable
	// helper without the Lambda cold-start env requirement.
	// ==============================================================
	wireEventsHandler()
	lambda.Start(handle)
}

// wireEventsHandler builds and assigns the package-level eventsHandler using
// the AWS clients captured by init(). It calls resolveThreadsTable which
// calls os.Exit(1) when KM_SLACK_THREADS_TABLE is unset.
func wireEventsHandler() {
	prefix := resourcePrefix()
	sandboxesTable := envOr("KM_SANDBOX_TABLE_NAME", prefix+"-sandboxes")
	signingSecretPath := envOr("KM_SIGNING_SECRET_PATH", "/"+prefix+"/slack/signing-secret")
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")

	threadsTable := resolveThreadsTable(func(key string) (string, bool) {
		v := os.Getenv(key)
		return v, v != ""
	}, os.Exit)

	slog.Info("km-slack-bridge: cold start",
		"KM_SANDBOX_TABLE_NAME", sandboxesTable,
		"KM_SLACK_THREADS_TABLE", threadsTable,
		"KM_SIGNING_SECRET_PATH", signingSecretPath,
		"KM_SLACK_ACK_EMOJI", envOr("KM_SLACK_ACK_EMOJI", "eyes"),
	)

	signingSecret := &bridge.SSMSigningSecretFetcher{
		Client:   initSSMC,
		Path:     signingSecretPath,
		CacheTTL: 15 * time.Minute,
	}

	sqsSender := &bridge.SQSAdapter{Client: initSQSClient}

	threadStore := &bridge.DDBThreadStore{
		Client:    initDDB,
		TableName: threadsTable,
	}

	sandboxResolver := &bridge.DDBSandboxByChannel{
		Client:    initDDB,
		TableName: sandboxesTable,
		IndexName: "slack_channel_id-index",
	}

	botUserIDFetcher := &bridge.CachedBotUserIDFetcher{
		SlackAPI:     &slackAuthTestAdapter{httpClient: initHTTPClient},
		TokenFetcher: initToken,
	}

	// Reuse DynamoNonceStore wrapped in an EventNonceStore adapter.
	// DynamoNonceStore uses Reserve/ErrNonceReplayed; we wrap it to provide
	// the CheckAndStore bool interface expected by EventsHandler.
	eventNonces := &nonceStoreAdapter{inner: initNonces}

	eventsHandler = &bridge.EventsHandler{
		SigningSecret: signingSecret,
		BotUserID:     botUserIDFetcher,
		Nonces:        eventNonces,
		Sandboxes:     sandboxResolver,
		Threads:       threadStore,
		SQS:           sqsSender,
		Logger:        slog.Default(),
	}

	// Phase 91: polite-bot mode. Read at cold-start; constant for Lambda lifetime.
	WireMentionOnly(eventsHandler, botUserIDFetcher)

	// Phase 95: federated relay. Parse KM_SLACK_PEER_BRIDGES; build HTTPPeerRelayer.
	// nil Relayer => federation off => byte-identical to today's handle path.
	// KM_SLACK_PEER_BRIDGES is set by the lambda-slack-bridge Terraform module
	// (var.slack_peer_bridges) from km-config.yaml slack.peer_bridges.
	if raw := os.Getenv("KM_SLACK_PEER_BRIDGES"); raw != "" {
		var peers []string
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				peers = append(peers, u)
			}
		}
		if len(peers) > 0 {
			eventsHandler.Relayer = &bridge.HTTPPeerRelayer{
				PeerURLs:   peers,
				HTTPClient: initHTTPClient, // reuse existing shared client
			}
			slog.Info("km-slack-bridge: federated relay enabled", "peer_count", len(peers))
		}
	}

	// Phase 96: default router. Off by default (zero value of bool); only meaningful
	// on the designated front-door install. When KM_SLACK_DEFAULT_ROUTER=true, wire:
	//   eventsHandler.DefaultRouter  = true
	//   eventsHandler.RunningChannels = DDBRunningChannelLister (scan km-sandboxes)
	//   eventsHandler.RouterCooldown  = routerCooldownAdapter wrapping nonces table
	// When absent/false, all three fields remain zero/nil => router dormant
	// => byte-identical to Phase 95 behavior.
	//
	// Deploy constraint: env-block change requires km init --dry-run=false (NOT --sidecars).
	// See project_km_init_lambdas_doesnt_deploy and project_km_init_skips_existing_lambda_zips.
	if os.Getenv("KM_SLACK_DEFAULT_ROUTER") == "true" {
		eventsHandler.DefaultRouter = true
		eventsHandler.RunningChannels = &bridge.DDBRunningChannelLister{
			Client:    initDDB,
			TableName: sandboxesTable,
		}
		eventsHandler.RouterCooldown = &routerCooldownAdapter{inner: initNonces}
		slog.Info("km-slack-bridge: default-router enabled")
	}

	// Wire DDBPauseHinter to eventsHandler.PauseHinter.
	postHintFn := bridge.PostHintFunc(func(ctx context.Context, channelID, threadTS, text string) error {
		_, err := initPoster.PostMessage(ctx, channelID, "", text, threadTS)
		return err
	})
	pauseHinter := &bridge.DDBPauseHinter{
		Client:             initDDB,
		SandboxesTableName: sandboxesTable,
		SandboxByChannel:   sandboxResolver,
		Post:               postHintFn,
		HintText:           "Sandbox is paused; message queued. Run `km resume <sandbox-id>` to wake it up.",
		CooldownSeconds:    3600,
	}
	eventsHandler.PauseHinter = pauseHinter

	// Phase 67.1: ACK reaction wiring.
	ackEmoji := os.Getenv("KM_SLACK_ACK_EMOJI")
	if ackEmoji == "" {
		ackEmoji = "eyes"
	}
	eventsHandler.Reactor = &bridge.SlackReactorAdapter{
		HTTPClient: initHTTPClient,
		Tokens:     initToken,
	}
	eventsHandler.AckEmoji = ackEmoji

	// Phase 75: wire S3FileDownloader for inbound file_share events.
	if artifactsBucket != "" {
		eventsHandler.FileDownloader = &bridge.S3FileDownloader{
			HTTPClient: initHTTPClient,
			S3:         initS3Client,
			Bucket:     artifactsBucket,
			Tokens:     initToken,
			FilesInfo: &bridge.SlackFilesInfoAdapter{
				HTTPClient: initHTTPClient,
				Tokens:     initToken,
			},
		}
	} else {
		slog.Warn("km-slack-bridge: phase-75 file downloader disabled: KM_ARTIFACTS_BUCKET unset; file_share events will dispatch text-only")
	}

	// Phase 75: wire SlackPoster into EventsHandler.
	eventsHandler.Slack = initPoster
}

// WireMentionOnly reads KM_SLACK_MENTION_ONLY and KM_SLACK_BOT_USER_ID from the
// environment and applies them to h and fetcher respectively. Exported so tests can
// call it directly without going through wireEventsHandler (which requires the full
// Lambda cold-start AWS client init).
//
//   - KM_SLACK_MENTION_ONLY=true  → bridge filters to @-mention-only messages.
//   - KM_SLACK_BOT_USER_ID=<UID> → prime CachedBotUserIDFetcher to avoid a live
//     auth.test call on the first mention scan. Set by the lambda-slack-bridge
//     Terraform module (var.slack_mention_only / var.slack_bot_user_id). Phase 91.
func WireMentionOnly(h *bridge.EventsHandler, fetcher *bridge.CachedBotUserIDFetcher) {
	h.MentionOnly = os.Getenv("KM_SLACK_MENTION_ONLY") == "true"
	if uid := os.Getenv("KM_SLACK_BOT_USER_ID"); uid != "" {
		fetcher.PrimeCache(uid)
		slog.Info("km-slack-bridge: primed bot_user_id cache from KM_SLACK_BOT_USER_ID env",
			"uid", uid)
	}
	// Phase 91.4: ReactAlways defaults to true (current behaviour). Set to false
	// ONLY when KM_SLACK_REACT_ALWAYS is explicitly "false". Any other value
	// (empty, "true", garbage) leaves the chatty-reactor behaviour intact.
	h.ReactAlways = os.Getenv("KM_SLACK_REACT_ALWAYS") != "false"
	slog.Info("km-slack-bridge: events handler mention-only mode",
		"enabled", h.MentionOnly,
		"react_always", h.ReactAlways)
}

// handle converts a Lambda Function URL request into the appropriate handler request,
// dispatching by RawPath:
//
//	/events  → bridge.EventsHandler.Handle (Phase 67 Slack Events API)
//	default  → bridge.Handler.Handle (Phase 63 operator-signed envelopes)
func handle(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Normalize body — Lambda Function URL may base64-encode binary bodies.
	body := ev.Body
	if ev.IsBase64Encoded {
		// Slack sends raw UTF-8; base64 decode before signature verification.
		decoded, err := decodeBase64Body(ev.Body)
		if err != nil {
			slog.Warn("km-slack-bridge: base64 decode failed", "err", err)
			return events.LambdaFunctionURLResponse{StatusCode: 400, Body: "bad request"}, nil
		}
		body = decoded
	}

	switch ev.RawPath {
	case "/events":
		if eventsHandler == nil {
			slog.Warn("km-slack-bridge: /events called but eventsHandler is nil")
			return events.LambdaFunctionURLResponse{StatusCode: 503, Body: "events handler not configured"}, nil
		}
		resp := eventsHandler.Handle(ctx, bridge.EventsRequest{
			Headers: lowercaseHeaders(ev.Headers),
			Body:    body,
		})
		return events.LambdaFunctionURLResponse{
			StatusCode: resp.StatusCode,
			Body:       resp.Body,
			Headers:    resp.Headers,
		}, nil

	default:
		// Phase 63 operator-signed envelope path (POST /).
		req := &bridge.Request{
			Body:    body,
			Headers: ev.Headers,
		}
		resp := handler.Handle(ctx, req)
		return events.LambdaFunctionURLResponse{
			StatusCode: resp.StatusCode,
			Body:       resp.Body,
			Headers:    resp.Headers,
		}, nil
	}
}

// lowercaseHeaders returns a copy of headers with all keys lowercased.
// Lambda Function URL headers are typically already lowercase, but normalize
// defensively — Slack signature verification is case-sensitive on key names.
func lowercaseHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return h
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

// decodeBase64Body decodes a base64-encoded request body.
// Returns an error only if the body is malformed base64.
func decodeBase64Body(encoded string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	return string(decoded), nil
}

// envOr returns the env var value or def when unset/empty.
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// resourcePrefix returns the operator's resource_prefix from the
// KM_RESOURCE_PREFIX env var, falling back to "km" only when truly unset.
// Used to derive prefix-aware fallbacks for table names and SSM paths so
// a non-default install (e.g. resource_prefix=kph) gets prefix-correct
// fallbacks even if a specific env var (KM_IDENTITIES_TABLE etc.) is
// accidentally not set by the Lambda terraform.
func resourcePrefix() string {
	if v := os.Getenv("KM_RESOURCE_PREFIX"); v != "" {
		return v
	}
	return "km"
}

// resolveThreadsTable is the testable core of the KM_SLACK_THREADS_TABLE
// cold-start check. It accepts a getenv function and an exit function so unit
// tests can capture both the return value and the exit call without forking a
// subprocess. If KM_SLACK_THREADS_TABLE is unset or empty, it logs an error
// and calls exit(1) to prevent silent cross-routing to a stale default table.
func resolveThreadsTable(getenv func(string) (string, bool), exit func(int)) string {
	if v, ok := getenv("KM_SLACK_THREADS_TABLE"); ok && v != "" {
		return v
	}
	slog.Error("KM_SLACK_THREADS_TABLE not set; refusing to start with stale default",
		"service", "km-slack-bridge")
	exit(1)
	return "" // unreachable
}

// ============================================================
// slackAuthTestAdapter — implements bridge.SlackAuthTestAPI
// Makes a POST to auth.test and extracts the bot user_id.
// ============================================================

// slackAuthTestResponse is the subset of auth.test response fields needed.
type slackAuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// slackAuthTestAdapter implements bridge.SlackAuthTestAPI via a direct HTTP call.
// It does NOT depend on pkg/slack.Client (which calls authTest without returning user_id).
type slackAuthTestAdapter struct {
	httpClient *http.Client
	baseURL    string // defaults to "https://slack.com/api"; overridden in tests
}

func (a *slackAuthTestAdapter) getBaseURL() string {
	if a.baseURL != "" {
		return a.baseURL
	}
	return "https://slack.com/api"
}

// AuthTest calls Slack auth.test with token and returns the bot's user_id.
func (a *slackAuthTestAdapter) AuthTest(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.getBaseURL()+"/auth.test", nil)
	if err != nil {
		return "", fmt.Errorf("auth.test: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	hc := a.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}
	defer resp.Body.Close()

	var apiResp slackAuthTestResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("auth.test: decode: %w", err)
	}
	if !apiResp.OK {
		return "", fmt.Errorf("auth.test: slack error: %s", apiResp.Error)
	}
	return apiResp.UserID, nil
}

// ============================================================
// Phase 68: probeFilesWriteScope — cold-start files:write scope check
// (RESEARCH Open Question 2 resolution).
// ============================================================

// probeFilesWriteScope queries auth.test once at cold start and inspects the
// X-OAuth-Scopes response header to determine whether the bot has files:write.
// Cached on Handler.MissingFilesWrite for the Lambda's lifetime — bot scopes
// only change when the operator re-installs the Slack App, which forces a
// Lambda cold start anyway.
//
// Decision (RESEARCH OQ 2): use a dedicated raw HTTP probe rather than
// extending SlackPosterAdapter.call() to surface response headers. Keeps the
// Phase 63 SlackPosterAdapter surface stable and isolates this Phase-68
// concern.
//
// Error handling: on transport failure or absent X-OAuth-Scopes header,
// returns (false, nil) — fail-open so the per-request Slack call surfaces
// the real error. This matches the conservative default: don't block all
// uploads on a flaky probe.
func probeFilesWriteScope(ctx context.Context, baseURL, botToken string) (missing bool, err error) {
	probeURL := strings.TrimRight(baseURL, "/") + "/api/auth.test"
	req, reqErr := http.NewRequestWithContext(ctx, "POST", probeURL, nil)
	if reqErr != nil {
		return false, fmt.Errorf("scope probe: build request: %w", reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	// auth.test accepts either form-encoded or empty body; explicit Content-Type
	// prevents some HTTP middlewares from rejecting the empty POST.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	hc := &http.Client{Timeout: 5 * time.Second}
	resp, doErr := hc.Do(req)
	if doErr != nil {
		return false, fmt.Errorf("scope probe: %w", doErr)
	}
	defer resp.Body.Close()

	scopes := resp.Header.Get("X-OAuth-Scopes")
	if scopes == "" {
		// Header absent — fail-open. Some Slack installs return scopes only
		// on certain methods or with non-standard headers.
		return false, nil
	}
	return !strings.Contains(scopes, "files:write"), nil
}

// ============================================================
// nonceStoreAdapter — bridges DynamoNonceStore to EventNonceStore
// EventsHandler needs CheckAndStore(bool); DynamoNonceStore uses Reserve/ErrNonceReplayed.
// ============================================================

type nonceStoreAdapter struct {
	inner *bridge.DynamoNonceStore
}

func (n *nonceStoreAdapter) CheckAndStore(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	err := n.inner.Reserve(ctx, id, int(ttl.Seconds()))
	if err == nil {
		return false, nil // first insertion
	}
	if errors.Is(err, bridge.ErrNonceReplayed) {
		return true, nil // already seen
	}
	return false, err // storage failure
}

// ============================================================
// routerCooldownAdapter — bridges DynamoNonceStore to RouterCooldownStore.
// Prefixes the per-channel key with "router-cooldown:" so router cooldown
// entries are namespaced away from operator nonces and event dedup keys.
// Modelled on nonceStoreAdapter above (Phase 96).
// ============================================================

type routerCooldownAdapter struct {
	inner *bridge.DynamoNonceStore
}

func (r *routerCooldownAdapter) Reserve(ctx context.Context, channelID string, cooldownSeconds int) error {
	return r.inner.Reserve(ctx, "router-cooldown:"+channelID, cooldownSeconds)
}
