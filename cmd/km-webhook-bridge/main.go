// Command km-webhook-bridge is the generic webhook ingress Lambda.
//
// It receives a POST on a Lambda Function URL, resolves the source from the URL
// PATH segment (POST /wiz), authenticates via the source's configured scheme,
// drops replays, matches operator-declared rules, and dispatches a prompt to a
// sandbox alias — warm via the per-sandbox webhook-inbound FIFO, cold via an
// EventBridge SandboxCreate.
//
// Returns 200 on EVERY path including internal errors, so a sender never
// redelivers with a fresh id that would bypass dedup.
//
// This is the generic analog of cmd/km-github-bridge/main.go and
// cmd/km-h1-bridge/main.go. There is no threads table, no customer-API
// Basic-Auth back-channel, and no bot handle — a webhook source is one-way
// (pkg/webhook/bridge/interfaces.go doc comment).
//
// Environment variables:
//
//	KM_RESOURCE_PREFIX     — resource_prefix (default "km")
//	KM_WEBHOOK_SOURCES     — JSON {sources:[...], rate_limit:{...}}; absent ⇒ dormant
//	KM_NONCE_TABLE         — shared nonces table (default {prefix}-slack-bridge-nonces)
//	KM_SANDBOX_TABLE_NAME  — {prefix}-sandboxes (default {prefix}-sandboxes)
//	KM_ARTIFACTS_BUCKET    — S3 artifacts bucket for the cold-create event
//	KM_ARTIFACTS_PREFIX    — S3 artifacts prefix
//
//	KM_QUOTA_TABLE         — {prefix}-action-quota (Task 9A); empty ⇒ quota gate dormant
//
// Action-quota / auto-freeze enforcement (Phase 121, already wired into the
// GitHub/H1/Slack bridges) is wired here too (Task 9A) via WireActionQuota,
// gated on KM_QUOTA_TABLE. It only applies to the WARM dispatch path (a
// resolved sandbox id) — the cold-create path has no sandbox yet and hence no
// usage history to gate against, so it is ungated by construction.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/webhook/bridge"
)

// handler is the global Handler constructed once per cold start.
var handler *bridge.Handler

// sourcesEnv is the shape of KM_WEBHOOK_SOURCES.
type sourcesEnv struct {
	Sources   []config.WebhookSource   `json:"sources"`
	RateLimit *config.WebhookRateLimit `json:"rate_limit"`
}

// parseSourcesEnv parses KM_WEBHOOK_SOURCES. An empty/whitespace-only raw value
// is dormant (zero sources, nil error) — this must NOT crash the cold start when
// the operator has not configured any webhook source. Malformed JSON reports the
// error to the caller (who logs and stays dormant) rather than panicking.
func parseSourcesEnv(raw string) (sourcesEnv, error) {
	var cfg sourcesEnv
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return sourcesEnv{}, err
	}
	return cfg, nil
}

// lowercaseHeaders returns a copy of headers with all keys lowercased. Lambda
// Function URL headers are typically already lowercase, but normalize
// defensively — webhook.Authenticate indexes by lowercase header name.
func lowercaseHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(k)] = v
	}
	return out
}

// envOr returns the env var value or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-webhook-bridge: load AWS config: %v", err)
	}

	ddbClient := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)
	ssmClient := ssm.NewFromConfig(cfg)
	ebClient := eventbridge.NewFromConfig(cfg)
	ec2Client := ec2.NewFromConfig(cfg)

	// ── Resource prefix ──────────────────────────────────────────────────────
	prefix := envOr("KM_RESOURCE_PREFIX", "km")

	// ── Table names ──────────────────────────────────────────────────────────
	nonceTable := envOr("KM_NONCE_TABLE", prefix+"-slack-bridge-nonces")
	sandboxesTable := envOr("KM_SANDBOX_TABLE_NAME", prefix+"-sandboxes")

	// ── Artifacts (for cold-create EventBridge event) ─────────────────────────
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	artifactsPrefix := os.Getenv("KM_ARTIFACTS_PREFIX")

	// ── Parse KM_WEBHOOK_SOURCES JSON ──────────────────────────────────────────
	// Dormant by default: absent or malformed ⇒ zero sources, and the bridge
	// never crashes the cold start (Handle() finds no matching source and
	// returns 200 "unknown_source" for every request instead).
	var sources []config.WebhookSource
	var rateLimit *config.WebhookRateLimit
	if raw := os.Getenv("KM_WEBHOOK_SOURCES"); raw != "" {
		parsed, perr := parseSourcesEnv(raw)
		if perr != nil {
			slog.Warn("km-webhook-bridge: failed to parse KM_WEBHOOK_SOURCES JSON; bridge is dormant (all requests silent-drop)",
				"err", perr)
		} else {
			sources = parsed.Sources
			rateLimit = parsed.RateLimit
			slog.Info("km-webhook-bridge: loaded source config",
				"source_count", len(sources), "rate_limited", rateLimit != nil)
		}
	} else {
		slog.Warn("km-webhook-bridge: KM_WEBHOOK_SOURCES not set; bridge is dormant (all requests silent-drop)")
	}

	// ── Wire adapters ─────────────────────────────────────────────────────────
	secretFetcher := &bridge.SSMSecretFetcher{
		Client:   ssmClient,
		CacheTTL: 15 * time.Minute,
	}
	nonceStore := &bridge.DynamoWebhookNonceStore{
		Client:    ddbClient,
		TableName: nonceTable,
	}
	rateCounter := &bridge.DynamoRateCounter{
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
	sqsSender := &bridge.WebhookSQSAdapter{Client: sqsClient}
	publisher := &bridge.EventBridgeAdapter{
		Client:         ebClient,
		ArtifactBucket: artifactsBucket,
		ArtifactPrefix: artifactsPrefix,
	}
	resumer := &bridge.EC2Resumer{
		Client:         ec2Client,
		ResourcePrefix: prefix,
	}

	// ── Construct Handler ──────────────────────────────────────────────────────
	// Every injected dependency below MUST be non-nil, and Now MUST be set: a
	// nil field panics inside Handle() (no recover() in the request path), the
	// Lambda returns a non-200, and the sender redelivers with a fresh delivery
	// id — walking straight past dedup. See TestHandlerFieldsAllWired.
	handler = &bridge.Handler{
		Sources:   sources,
		RateLimit: rateLimit,
		Secrets:   secretFetcher,
		Resolver:  resolver,
		Queue:     sqsSender,
		Resumer:   resumer,
		Status:    statusWriter,
		Cold:      publisher,
		Nonces:    nonceStore,
		Rates:     rateCounter,
		Now:       func() int64 { return time.Now().Unix() },
	}

	// Task 9A (Phase 121 follow-up) — wire action-quota + auto-freeze
	// enforcement (dormant unless KM_QUOTA_TABLE is set on the Lambda env by
	// the TF module).
	WireActionQuota(handler, ddbClient, sandboxesTable)

	slog.Info("km-webhook-bridge: cold start",
		"KM_RESOURCE_PREFIX", prefix,
		"KM_SANDBOX_TABLE_NAME", sandboxesTable,
		"KM_NONCE_TABLE", nonceTable,
		"KM_ARTIFACTS_BUCKET", artifactsBucket,
		"source_count", len(sources),
	)
}

// WireActionQuota wires the Task 9A action-quota + auto-freeze fields onto the
// Handler from env. Gated on KM_QUOTA_TABLE: empty ⇒ dormant (Quota/Limits/
// Freezer stay nil ⇒ the webhook_dispatch quota check no-ops, byte-identical
// to the pre-Task-9A bridge). When set, the per-sandbox limits come from the
// km-sandboxes action_limits attr (DDBActionLimitsFetcher) and a BreachFreeze
// latches action_frozen via DynamoFreezer. Returns true when wired. Mirrors
// cmd/km-h1-bridge/main.go's WireActionQuota.
func WireActionQuota(h *bridge.Handler, ddb *dynamodb.Client, sandboxesTable string) bool {
	quotaTable := os.Getenv("KM_QUOTA_TABLE")
	if quotaTable == "" {
		return false
	}
	h.Quota = ddb
	h.QuotaTable = quotaTable
	h.Limits = &bridge.DDBActionLimitsFetcher{Client: ddb, TableName: sandboxesTable}
	h.Freezer = &bridge.DynamoFreezer{Client: ddb, Table: sandboxesTable}
	slog.Info("km-webhook-bridge: action-quota enforcement wired",
		"quota_table", quotaTable, "sandboxes_table", sandboxesTable)
	return true
}

func handleRequest(ctx context.Context, req events.LambdaFunctionURLRequest) (
	events.LambdaFunctionURLResponse, error) {

	body := []byte(req.Body)
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err == nil {
			body = decoded
		}
		// A decode failure falls back to the raw (still base64) body rather than
		// erroring — Authenticate/ParseEnvelope will fail closed on it downstream
		// and Handle() always returns 200 regardless.
	}
	hdrs := lowercaseHeaders(req.Headers)

	resp := handler.Handle(ctx, bridge.Request{
		Path:            req.RawPath,
		ContentEncoding: hdrs["content-encoding"],
		Headers:         hdrs,
		Body:            body,
	})

	return events.LambdaFunctionURLResponse{
		StatusCode: resp.Status,
		Body:       `{"ok":true,"result":"` + resp.Log + `"}`,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

func main() { lambda.Start(handleRequest) }
