// Command km-h1-bridge is the Phase 103 HackerOne inbound bridge Lambda.
//
// It receives HackerOne program webhook POST requests via a Lambda Function URL,
// verifies the HMAC-SHA256 X-H1-Signature signature, guards against self-comment
// loops and replays, enforces the two-trigger model (auto-triage event-gate +
// deny-by-default comment-keyword allowlist), resolves the report's program handle
// to one-or-more sandbox targets, and dispatches each:
//   - Warm (alias → running sandbox): enqueue to the per-sandbox h1-inbound FIFO.
//   - Cold (no sandbox for alias): publish SandboxCreate EventBridge event carrying
//     the H1 envelope so the create-handler can drain it post-provisioning.
//   - Resume (alias stopped/paused): StartInstances + enqueue.
//
// Returns 200 on EVERY internal error (Pitfall 2) so HackerOne does not redeliver
// with a fresh GUID that bypasses dedup. A single synchronous INTERNAL "on it"
// comment acks a successful dispatch.
//
// This is the HackerOne analog of cmd/km-github-bridge/main.go. The federation
// relayer, the orphan router, the GitHub App JWT reactor, and the App-credentials
// SSM reads are DROPPED — HackerOne's customer API is HTTP Basic Auth (no App
// install model), and each program's webhook points directly at one install's
// Function URL (no one-App-many-installs relay needed).
//
// Environment variables (all required unless noted):
//
//	KM_RESOURCE_PREFIX       — resource_prefix (default: "km")
//	KM_H1_PROGRAMS           — JSON object: {programs:[ProgramEntry], default_profile, bot_handle} (list-of-objects env var)
//	KM_H1_DEFAULT_PROFILE    — fallback profile name when a target omits Profile (default: h1-triage)
//	KM_H1_BOT_HANDLE         — install-wide comment-keyword token (e.g. "@km")
//	KM_NONCE_TABLE           — DynamoDB shared nonces table (default: {prefix}-slack-bridge-nonces)
//	KM_SANDBOX_TABLE_NAME    — DynamoDB km-sandboxes table (default: {prefix}-sandboxes)
//	KM_H1_THREADS_TABLE      — DynamoDB km-h1-threads table (default: {prefix}-h1-threads)
//	KM_WEBHOOK_SECRET_PATH   — SSM path for the H1 webhook secret (default: /{prefix}/config/h1/webhook-secret)
//	KM_H1_API_USERNAME_PATH  — SSM path for the H1 customer-API Basic-Auth username (default: /{prefix}/config/h1/api-username)
//	KM_H1_API_TOKEN_PATH     — SSM path for the H1 customer-API Basic-Auth token (default: /{prefix}/config/h1/api-token)
//	KM_H1_API_BASE_URL       — HackerOne customer-API base URL (optional; default https://api.hackerone.com/v1)
//	KM_COMMANDS_PATH         — SSM path for the h1 command set (default: /{prefix}/config/h1/commands)
//	KM_ARTIFACTS_BUCKET      — S3 artifacts bucket (for EventBridge artifact_bucket field)
//	KM_ARTIFACTS_PREFIX      — S3 artifacts prefix (for EventBridge artifact_prefix field)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// webhookHandler is the global handler constructed once per cold start.
var webhookHandler *bridge.WebhookHandler

// programsConfig is the top-level shape of KM_H1_PROGRAMS JSON.
// It carries the resolved list of ProgramEntry objects plus the install-wide
// default profile and comment-keyword bot handle.
type programsConfig struct {
	Programs       []bridge.ProgramEntry `json:"programs"`
	DefaultProfile string                `json:"default_profile"`
	BotHandle      string                `json:"bot_handle"`
}

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-h1-bridge: load AWS config: %v", err)
	}

	ddbClient := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)
	ssmClient := ssm.NewFromConfig(cfg)
	ebClient := eventbridge.NewFromConfig(cfg)
	ec2Client := ec2.NewFromConfig(cfg)

	// ── Resource prefix ──────────────────────────────────────────────────────
	prefix := envOr("KM_RESOURCE_PREFIX", "km")

	// ── SSM paths ────────────────────────────────────────────────────────────
	ssmWebhookSecretPath := envOr("KM_WEBHOOK_SECRET_PATH", "/"+prefix+"/config/h1/webhook-secret")
	ssmAPIUsernamePath := envOr("KM_H1_API_USERNAME_PATH", "/"+prefix+"/config/h1/api-username")
	ssmAPITokenPath := envOr("KM_H1_API_TOKEN_PATH", "/"+prefix+"/config/h1/api-token")
	ssmCommandsPath := envOr("KM_COMMANDS_PATH", "/"+prefix+"/config/h1/commands")

	// ── Table names ──────────────────────────────────────────────────────────
	nonceTable := envOr("KM_NONCE_TABLE", prefix+"-slack-bridge-nonces")
	sandboxesTable := envOr("KM_SANDBOX_TABLE_NAME", prefix+"-sandboxes")
	h1ThreadsTable := envOr("KM_H1_THREADS_TABLE", prefix+"-h1-threads")

	// ── Artifacts (for cold-create EventBridge event) ─────────────────────────
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	artifactsPrefix := os.Getenv("KM_ARTIFACTS_PREFIX")

	// ── HackerOne customer-API base URL (Basic Auth back-channel) ─────────────
	apiBaseURL := os.Getenv("KM_H1_API_BASE_URL")

	// ── Parse KM_H1_PROGRAMS JSON (list-of-objects env var, Pitfall 2) ────────
	var entries []bridge.ProgramEntry
	defaultProfile := envOr("KM_H1_DEFAULT_PROFILE", "h1-triage")
	botHandle := os.Getenv("KM_H1_BOT_HANDLE")

	if raw := os.Getenv("KM_H1_PROGRAMS"); raw != "" {
		var pcfg programsConfig
		if err := json.Unmarshal([]byte(raw), &pcfg); err != nil {
			slog.Warn("km-h1-bridge: failed to parse KM_H1_PROGRAMS JSON; bridge will silent-drop all programs",
				"err", err)
		} else {
			entries = pcfg.Programs
			if pcfg.DefaultProfile != "" {
				defaultProfile = pcfg.DefaultProfile
			}
			if pcfg.BotHandle != "" {
				botHandle = pcfg.BotHandle
			}
			slog.Info("km-h1-bridge: loaded program config",
				"program_count", len(entries),
				"default_profile", defaultProfile,
				"bot_handle", botHandle)
		}
	} else {
		slog.Warn("km-h1-bridge: KM_H1_PROGRAMS not set; bridge is dormant (all programs silent-drop)")
	}

	// ── Read the HackerOne customer-API Basic-Auth identity from SSM ─────────
	// The bridge holds these creds purely for (a) the loop-guard (drop the bridge's
	// own internal ack so it does not re-trigger) and (b) posting the synchronous
	// INTERNAL "on it" comment. Researcher-visible replies come from the sandbox
	// helper (cmd/km-h1), never this Lambda. On fetch failure the bridge degrades:
	// no loop-guard username + no internal ack, but dispatch still works.
	apiUsername, apiToken := readH1APICreds(ctx, ssmClient, ssmAPIUsernamePath, ssmAPITokenPath)

	// ── Secret fetcher (webhook signing secret) ───────────────────────────────
	secretFetcher := &bridge.SSMSecretFetcher{
		Client:   ssmClient,
		Path:     ssmWebhookSecretPath,
		CacheTTL: 15 * time.Minute,
	}

	// ── Command set from SSM (dormant when absent) ────────────────────────────
	// The commands parameter is a (base64-encoded) CommandSet envelope:
	// {"commands": {...}, "default_command": "triage"}. SSMCommandsFetcher returns
	// an empty map + "" default (not nil, not error) when the parameter is absent —
	// the dormant signal (free-form dispatch only).
	commandsFetcher := &bridge.SSMCommandsFetcher{
		Client:   ssmClient,
		Path:     ssmCommandsPath,
		CacheTTL: 15 * time.Minute,
	}
	commands, defaultCommand, cmdErr := commandsFetcher.Fetch(ctx)
	if cmdErr != nil {
		slog.Warn("km-h1-bridge: failed to fetch commands from SSM at cold start; command pass dormant",
			"err", cmdErr, "path", ssmCommandsPath)
		commands = map[string]bridge.CommandEntry{}
		defaultCommand = ""
	} else {
		slog.Info("km-h1-bridge: loaded command config",
			"command_count", len(commands),
			"default_command", defaultCommand,
			"path", ssmCommandsPath)
	}

	// ── Wire adapters ─────────────────────────────────────────────────────────
	nonceStore := &bridge.DynamoH1NonceStore{
		Client:    ddbClient,
		TableName: nonceTable,
	}
	resolver := &bridge.DynamoAliasResolver{
		Client:    ddbClient,
		TableName: sandboxesTable,
	}
	statusWriter := &bridge.DynamoSandboxStatusWriter{
		Client:    ddbClient,
		TableName: sandboxesTable,
	}
	threadStore := &bridge.DynamoH1ThreadStore{
		Client:    ddbClient,
		TableName: h1ThreadsTable,
	}
	sqsSender := &bridge.H1SQSAdapter{Client: sqsClient}
	publisher := &bridge.EventBridgeAdapter{
		Client:         ebClient,
		ArtifactBucket: artifactsBucket,
		ArtifactPrefix: artifactsPrefix,
	}
	resumer := &bridge.EC2Resumer{
		Client:         ec2Client,
		ResourcePrefix: prefix,
	}
	commenter := &bridge.H1APICommenter{
		BaseURL:     apiBaseURL,
		APIUsername: apiUsername,
		APIToken:    apiToken,
	}

	// ── Construct WebhookHandler ──────────────────────────────────────────────
	webhookHandler = &bridge.WebhookHandler{
		Secret:         secretFetcher,
		APIUsername:    apiUsername,
		Nonces:         nonceStore,
		Resolver:       resolver,
		Resumer:        resumer,
		Publisher:      publisher,
		SQS:            sqsSender,
		StatusWriter:   statusWriter,
		Threads:        threadStore,
		Commenter:      commenter,
		Entries:        entries,
		DefaultProfile: defaultProfile,
		BotHandle:      botHandle,
		Commands:       commands,
		DefaultCommand: defaultCommand,
	}

	// Phase 121 follow-up: wire action-quota + auto-freeze enforcement (dormant
	// unless KM_QUOTA_TABLE is set on the Lambda env by the TF module).
	WireActionQuota(webhookHandler, ddbClient, sandboxesTable)

	slog.Info("km-h1-bridge: cold start",
		"KM_RESOURCE_PREFIX", prefix,
		"KM_SANDBOX_TABLE_NAME", sandboxesTable,
		"KM_NONCE_TABLE", nonceTable,
		"KM_H1_THREADS_TABLE", h1ThreadsTable,
		"KM_WEBHOOK_SECRET_PATH", ssmWebhookSecretPath,
		"KM_ARTIFACTS_BUCKET", artifactsBucket,
		"program_count", len(entries),
		"command_count", len(commands),
		"default_command", defaultCommand,
		"api_username_set", apiUsername != "",
	)
}

// readH1APICreds fetches the HackerOne customer-API Basic-Auth username and token
// from SSM at Lambda cold-start. On failure, returns empty strings and logs a
// warning — the bridge degrades to no-loop-guard + no-internal-ack but still
// dispatches (the 200-on-internal-error contract is preserved).
func readH1APICreds(ctx context.Context, client *ssm.Client, usernamePath, tokenPath string) (apiUsername, apiToken string) {
	out, err := client.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          []string{usernamePath, tokenPath},
		WithDecryption: boolPtr(true),
	})
	if err != nil {
		slog.Warn("km-h1-bridge: could not fetch HackerOne API credentials from SSM at cold start",
			"err", err, "paths", []string{usernamePath, tokenPath})
		return "", ""
	}
	for _, p := range out.Parameters {
		if p.Name == nil || p.Value == nil {
			continue
		}
		switch *p.Name {
		case usernamePath:
			apiUsername = *p.Value
		case tokenPath:
			apiToken = *p.Value
		}
	}
	if apiUsername == "" || apiToken == "" {
		slog.Warn("km-h1-bridge: HackerOne API credentials incomplete in SSM; loop-guard + internal ACK disabled until config is set")
	}
	return apiUsername, apiToken
}

// WireActionQuota wires the Phase 121 action-quota + auto-freeze fields onto the
// WebhookHandler from env. Gated on KM_QUOTA_TABLE: empty ⇒ dormant
// (Quota/Limits/Freezer stay nil ⇒ the h1_comment quota check no-ops,
// byte-identical to the pre-follow-up bridge). When set, the per-sandbox limits
// come from the km-sandboxes action_limits attr (DDBActionLimitsFetcher) and a
// BreachFreeze latches action_frozen via DynamoFreezer. Returns true when wired.
func WireActionQuota(h *bridge.WebhookHandler, ddb *dynamodb.Client, sandboxesTable string) bool {
	quotaTable := os.Getenv("KM_QUOTA_TABLE")
	if quotaTable == "" {
		return false
	}
	h.Quota = ddb
	h.QuotaTable = quotaTable
	h.Limits = &bridge.DDBActionLimitsFetcher{Client: ddb, TableName: sandboxesTable}
	h.Freezer = &bridge.DynamoFreezer{Client: ddb, Table: sandboxesTable}
	slog.Info("km-h1-bridge: action-quota enforcement wired",
		"quota_table", quotaTable, "sandboxes_table", sandboxesTable)
	return true
}

func main() {
	lambda.Start(handle)
}

// handle converts a Lambda Function URL request to WebhookHandler.Handle.
// Normalizes base64-encoded bodies (Pitfall 1: HMAC the DECODED bytes) and
// lowercases headers (Lambda Function URL convention; signature verification is
// case-sensitive on key names).
func handle(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Normalize body — Lambda Function URL may base64-encode binary bodies.
	// CRITICAL (Pitfall 1): the HMAC must be computed over the DECODED bytes, so
	// decode BEFORE constructing the request that VerifyH1Signature consumes.
	bodyStr := ev.Body
	if ev.IsBase64Encoded {
		decoded, err := decodeBase64Body(ev.Body)
		if err != nil {
			slog.Warn("km-h1-bridge: base64 decode failed", "err", err)
			return events.LambdaFunctionURLResponse{StatusCode: 400, Body: "bad request"}, nil
		}
		bodyStr = decoded
	}

	req := bridge.WebhookRequest{
		Headers: lowercaseHeaders(ev.Headers),
		RawBody: []byte(bodyStr),
		Body:    bodyStr,
	}

	resp := webhookHandler.Handle(ctx, req)
	return events.LambdaFunctionURLResponse{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
	}, nil
}

// lowercaseHeaders returns a copy of headers with all keys lowercased.
// Lambda Function URL headers are typically already lowercase, but normalize
// defensively — H1 signature verification reads "x-h1-signature" lowercase.
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

func boolPtr(b bool) *bool { return &b }
