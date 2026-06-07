// Command km-github-bridge is the Phase 97 GitHub App inbound bridge Lambda.
//
// It receives GitHub webhook POST requests (issue_comment events) via a
// Lambda Function URL, verifies the HMAC-SHA256 X-Hub-Signature-256 signature,
// guards against bot loops and replays, enforces a deny-by-default per-repo
// allowlist, resolves the owner/repo to a sandbox alias, and dispatches:
//   - Warm (alias → running sandbox): enqueue to github-inbound FIFO queue.
//   - Cold (no sandbox for alias): publish SandboxCreate EventBridge event carrying
//     the GitHub envelope so the create-handler can drain it post-provisioning.
//
// Returns 200 immediately (within GitHub's ~10s ack window) with a synchronous 👀
// reaction on the originating comment as the acknowledgement.
//
// Environment variables (all required unless noted):
//
//	KM_RESOURCE_PREFIX       — resource_prefix (default: "km")
//	KM_GITHUB_REPOS          — JSON array of RepoEntry objects (list-of-objects, Pitfall 2)
//	KM_GITHUB_DEFAULT_PROFILE — fallback profile name when matched entry has no profile
//	KM_NONCE_TABLE           — DynamoDB nonces table name (default: {prefix}-slack-bridge-nonces)
//	KM_SANDBOX_TABLE_NAME    — DynamoDB km-sandboxes table name (default: {prefix}-sandboxes)
//	KM_WEBHOOK_SECRET_PATH   — SSM path for the GitHub webhook secret (default: /{prefix}/config/github/webhook-secret)
//	KM_BOT_LOGIN_PATH        — SSM path for the bot-login string (default: /{prefix}/config/github/bot-login)
//	KM_APP_CLIENT_ID_PATH    — SSM path for the GitHub App client ID (default: /{prefix}/config/github/app-client-id)
//	KM_PRIVATE_KEY_PATH      — SSM path for the GitHub App RSA private key PEM (default: /{prefix}/config/github/private-key)
//	KM_INSTALLATION_ID_PATH  — SSM path for the GitHub App installation ID (default: /{prefix}/config/github/installation-id)
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

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// Package-level AWS clients and the global WebhookHandler, constructed once per cold start.
var (
	webhookHandler *bridge.WebhookHandler

	// SSM paths resolved from env vars at init.
	ssmWebhookSecretPath string
	ssmBotLoginPath      string
	ssmAppClientIDPath   string
	ssmPrivateKeyPath    string
	ssmInstallationIDPath string
)

// reposConfig is the top-level shape of KM_GITHUB_REPOS JSON.
// It contains the resolved list of RepoEntry objects and the default profile.
type reposConfig struct {
	Repos          []bridge.RepoEntry `json:"repos"`
	DefaultProfile string             `json:"default_profile"`
}

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-github-bridge: load AWS config: %v", err)
	}

	ddbClient := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)
	ssmClient := ssm.NewFromConfig(cfg)
	ebClient := eventbridge.NewFromConfig(cfg)
	ec2Client := ec2.NewFromConfig(cfg)

	// ── Resource prefix ──────────────────────────────────────────────────────
	prefix := envOr("KM_RESOURCE_PREFIX", "km")

	// ── SSM paths ────────────────────────────────────────────────────────────
	ssmWebhookSecretPath = envOr("KM_WEBHOOK_SECRET_PATH", "/"+prefix+"/config/github/webhook-secret")
	ssmBotLoginPath = envOr("KM_BOT_LOGIN_PATH", "/"+prefix+"/config/github/bot-login")
	ssmAppClientIDPath = envOr("KM_APP_CLIENT_ID_PATH", "/"+prefix+"/config/github/app-client-id")
	ssmPrivateKeyPath = envOr("KM_PRIVATE_KEY_PATH", "/"+prefix+"/config/github/private-key")
	ssmInstallationIDPath = envOr("KM_INSTALLATION_ID_PATH", "/"+prefix+"/config/github/installation-id")

	// ── Table names ───────────────────────────────────────────────────────────
	nonceTable := envOr("KM_NONCE_TABLE", prefix+"-slack-bridge-nonces")
	sandboxesTable := envOr("KM_SANDBOX_TABLE_NAME", prefix+"-sandboxes")
	githubThreadsTable := envOr("KM_GITHUB_THREADS_TABLE", prefix+"-github-threads")

	// ── Artifacts (for cold-create EventBridge event) ─────────────────────────
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	artifactsPrefix := os.Getenv("KM_ARTIFACTS_PREFIX")

	// ── Parse KM_GITHUB_REPOS JSON (Pitfall 2: list-of-objects as JSON env var) ─
	var entries []bridge.RepoEntry
	defaultProfile := envOr("KM_GITHUB_DEFAULT_PROFILE", "github-review")

	if raw := os.Getenv("KM_GITHUB_REPOS"); raw != "" {
		var rcfg reposConfig
		if err := json.Unmarshal([]byte(raw), &rcfg); err != nil {
			slog.Warn("km-github-bridge: failed to parse KM_GITHUB_REPOS JSON; bridge will silent-drop all repos",
				"err", err)
		} else {
			entries = rcfg.Repos
			if rcfg.DefaultProfile != "" {
				defaultProfile = rcfg.DefaultProfile
			}
			slog.Info("km-github-bridge: loaded repo config",
				"repo_count", len(entries),
				"default_profile", defaultProfile)
		}
	} else {
		slog.Warn("km-github-bridge: KM_GITHUB_REPOS not set; bridge is dormant (all repos silent-drop)")
	}

	// ── Wire the secret fetchers ──────────────────────────────────────────────
	secretFetcher := &bridge.SSMSecretFetcher{
		Client:   ssmClient,
		Path:     ssmWebhookSecretPath,
		CacheTTL: 15 * time.Minute,
	}
	botLoginFetcher := &bridge.SSMBotLoginFetcher{
		Client:   ssmClient,
		Path:     ssmBotLoginPath,
		CacheTTL: 15 * time.Minute,
	}

	// ── Read App credentials from SSM at cold-start for the Reactor ──────────
	// We eagerly read these at cold-start so startup logs surface missing config.
	// On fetch failure, the reactor will fail at invocation time (logged + 200).
	appClientID, privateKeyPEM := readAppCredentials(ctx, ssmClient)

	// ── Wire adapters ─────────────────────────────────────────────────────────
	nonceStore := &bridge.DynamoGitHubNonceStore{
		Client:    ddbClient,
		TableName: nonceTable,
	}
	resolver := &bridge.DynamoAliasResolver{
		Client:    ddbClient,
		TableName: sandboxesTable,
	}
	threadStore := &bridge.DynamoGitHubThreadStore{
		Client:    ddbClient,
		TableName: githubThreadsTable,
	}
	sqsSender := &bridge.GitHubSQSAdapter{Client: sqsClient}
	publisher := &bridge.EventBridgeAdapter{
		Client:         ebClient,
		ArtifactBucket: artifactsBucket,
		ArtifactPrefix: artifactsPrefix,
	}
	resumer := &bridge.EC2Resumer{
		Client:         ec2Client,
		ResourcePrefix: prefix,
	}
	reactor := &bridge.InstallationReactor{
		AppClientID:   appClientID,
		PrivateKeyPEM: []byte(privateKeyPEM),
	}

	// ── Construct WebhookHandler ──────────────────────────────────────────────
	webhookHandler = &bridge.WebhookHandler{
		Secret:         secretFetcher,
		BotLogin:       botLoginFetcher,
		Nonces:         nonceStore,
		Resolver:       resolver,
		Publisher:      publisher,
		SQS:            sqsSender,
		Reactor:        reactor,
		Resumer:        resumer,
		Threads:        threadStore,
		Entries:        entries,
		DefaultProfile: defaultProfile,
		ResourcePrefix: prefix,
		SandboxesTable: sandboxesTable,
	}

	slog.Info("km-github-bridge: cold start",
		"KM_RESOURCE_PREFIX", prefix,
		"KM_SANDBOX_TABLE_NAME", sandboxesTable,
		"KM_NONCE_TABLE", nonceTable,
		"KM_GITHUB_THREADS_TABLE", githubThreadsTable,
		"KM_WEBHOOK_SECRET_PATH", ssmWebhookSecretPath,
		"KM_BOT_LOGIN_PATH", ssmBotLoginPath,
		"KM_ARTIFACTS_BUCKET", artifactsBucket,
		"repo_count", len(entries),
	)
}

// readAppCredentials fetches the GitHub App client ID and RSA private key PEM
// from SSM at Lambda cold-start. On failure, returns empty strings and logs a
// warning — the Reactor will fail at invocation time with a logged error + 200.
func readAppCredentials(ctx context.Context, client *ssm.Client) (appClientID, privateKeyPEM string) {
	paths := []string{ssmAppClientIDPath, ssmPrivateKeyPath}
	out, err := client.GetParameters(ctx, &ssm.GetParametersInput{
		Names:          paths,
		WithDecryption: boolPtr(true),
	})
	if err != nil {
		slog.Warn("km-github-bridge: could not fetch GitHub App credentials from SSM at cold start",
			"err", err, "paths", paths)
		return "", ""
	}
	for _, p := range out.Parameters {
		if p.Name == nil || p.Value == nil {
			continue
		}
		switch *p.Name {
		case ssmAppClientIDPath:
			appClientID = *p.Value
		case ssmPrivateKeyPath:
			privateKeyPEM = *p.Value
		}
	}
	if appClientID == "" || privateKeyPEM == "" {
		slog.Warn("km-github-bridge: GitHub App credentials incomplete in SSM; reactions disabled until config is set")
	}
	return appClientID, privateKeyPEM
}

func main() {
	lambda.Start(handle)
}

// handle converts a Lambda Function URL request to WebhookHandler.Handle.
// Normalizes base64-encoded bodies and lowercases headers (Lambda Function URL
// convention; see cmd/km-slack-bridge/main.go:380).
func handle(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Normalize body — Lambda Function URL may base64-encode binary bodies.
	bodyStr := ev.Body
	if ev.IsBase64Encoded {
		decoded, err := decodeBase64Body(ev.Body)
		if err != nil {
			slog.Warn("km-github-bridge: base64 decode failed", "err", err)
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
// defensively — GitHub signature verification is case-sensitive on key names.
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
