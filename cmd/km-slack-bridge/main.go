// Command km-slack-bridge is the Phase 63 Slack-notify Lambda.
//
// It accepts signed envelopes from sandboxes and the operator, verifies the
// Ed25519 signature and nonce, and dispatches to the Slack Web API.
// See pkg/slack/bridge for the verification + dispatch logic.
//
// Cold start: reads env vars, builds AWS clients, wires production adapters
// into bridge.Handler, and calls lambda.Start.
//
// Environment variables:
//
//	KM_IDENTITIES_TABLE  — DynamoDB table for public keys (default: km-identities)
//	KM_SANDBOXES_TABLE   — DynamoDB table for sandbox metadata (default: km-sandboxes)
//	KM_NONCE_TABLE       — DynamoDB table for nonce replay protection (default: km-slack-bridge-nonces)
//	KM_BOT_TOKEN_PATH    — SSM parameter path for Slack bot token (default: /km/slack/bot-token)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// handler is the global bridge.Handler, constructed once per cold start.
var handler *bridge.Handler

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-slack-bridge: load AWS config: %v", err)
	}

	ddb := dynamodb.NewFromConfig(cfg)
	ssmc := ssm.NewFromConfig(cfg)

	identitiesTable := envOr("KM_IDENTITIES_TABLE", "km-identities")
	sandboxesTable := envOr("KM_SANDBOXES_TABLE", "km-sandboxes")
	nonceTable := envOr("KM_NONCE_TABLE", "km-slack-bridge-nonces")
	botTokenPath := envOr("KM_BOT_TOKEN_PATH", "/km/slack/bot-token")

	keys := &bridge.DynamoPublicKeyFetcher{Client: ddb, TableName: identitiesTable}
	nonces := &bridge.DynamoNonceStore{Client: ddb, TableName: nonceTable}
	channels := &bridge.DynamoChannelOwnershipFetcher{Client: ddb, TableName: sandboxesTable}
	token := &bridge.SSMBotTokenFetcher{Client: ssmc, Path: botTokenPath}

	// SlackPosterAdapter posts messages via the Slack Web API using the token
	// fetched lazily (and cached for 15 min) by SSMBotTokenFetcher.
	poster := &bridge.SlackPosterAdapter{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Tokens:     token,
	}

	handler = &bridge.Handler{
		Now:      time.Now,
		Keys:     keys,
		Nonces:   nonces,
		Channels: channels,
		Token:    token,
		Slack:    poster,
	}
}

func main() {
	lambda.Start(handle)
}

// handle converts a Lambda Function URL request into a bridge.Request, delegates
// to the handler, and returns the bridge.Response as a LambdaFunctionURLResponse.
func handle(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	req := &bridge.Request{
		Body:    ev.Body,
		Headers: ev.Headers,
	}

	resp := handler.Handle(ctx, req)

	return events.LambdaFunctionURLResponse{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
		Headers:    resp.Headers,
	}, nil
}

// envOr returns the env var value or def when unset/empty.
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
