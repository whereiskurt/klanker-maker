// Package main — km-quota-alerter Lambda handler.
//
// Triggered by DynamoDB Streams on the {prefix}-action-quota table.
// On the MODIFY where a window first breaches (breached_at newly set,
// alert_sent absent): sends the operator an SES email, optionally posts to
// a Slack control channel, then sets alert_sent conditionally (exactly one
// alert per sandbox/action/window).
//
// For proxy-origin actions (github_*/email_send) it also posts a channel-level
// user notice to the sandbox's main Slack channel resolved from km-sandboxes.
//
// Phase 121, Wave 3, plan 09.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

func main() {
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("[km-quota-alerter] load AWS config: %v", err)
	}

	ddbClient := dynamodb.NewFromConfig(awsCfg)
	sesClient := sesv2.NewFromConfig(awsCfg)
	ssmClient := ssm.NewFromConfig(awsCfg)

	// Resolve configuration from environment variables injected by Terraform.
	acfg := alerterConfig{
		operatorEmail:       os.Getenv("KM_OPERATOR_EMAIL"),
		emailDomain:         os.Getenv("KM_EMAIL_DOMAIN"),
		quotaTableName:      os.Getenv("KM_QUOTA_TABLE"),
		sandboxTableName:    os.Getenv("KM_SANDBOX_TABLE_NAME"),
		slackControlChannel: os.Getenv("KM_SLACK_CONTROL_CHANNEL"),
		botTokenPath:        os.Getenv("KM_BOT_TOKEN_PATH"),
		resourcePrefix:      os.Getenv("KM_RESOURCE_PREFIX"),
	}

	// Resolve Slack poster (optional — only when bot token path is configured).
	var sp slackPoster
	if acfg.botTokenPath != "" && acfg.sandboxTableName != "" {
		botToken, tokenErr := fetchSSMParameter(ctx, ssmClient, acfg.botTokenPath)
		if tokenErr != nil {
			// Non-fatal: log and continue without Slack posting.
			log.Printf("[km-quota-alerter] could not fetch bot token from SSM %q: %v", acfg.botTokenPath, tokenErr)
		} else {
			sp = &httpSlackPoster{botToken: botToken, httpClient: &http.Client{Timeout: 10 * time.Second}}
		}
	}

	a := &alerter{
		cfg:   acfg,
		ses:   sesClient,
		ddb:   &ddbWrapper{client: ddbClient},
		slack: sp,
	}

	lambda.Start(func(ctx context.Context, ev events.DynamoDBEvent) error {
		return a.Handle(ctx, ev)
	})
}

// ─────────────────────────────────────────────────────────────
// ddbWrapper satisfies ddbAPI using the real DynamoDB client.
// ─────────────────────────────────────────────────────────────

type ddbWrapper struct {
	client *dynamodb.Client
}

func (w *ddbWrapper) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return w.client.UpdateItem(ctx, in, optFns...)
}

func (w *ddbWrapper) GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return w.client.GetItem(ctx, in, optFns...)
}

// ─────────────────────────────────────────────────────────────
// httpSlackPoster posts a plain-text message to a Slack channel
// via chat.postMessage using the bot token.
// ─────────────────────────────────────────────────────────────

type httpSlackPoster struct {
	botToken   string
	httpClient *http.Client
}

func (p *httpSlackPoster) PostChannelMessage(ctx context.Context, channelID, text string) error {
	if p.botToken == "" || channelID == "" {
		return nil
	}
	type payload struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	body, err := json.Marshal(payload{Channel: channelID, Text: text})
	if err != nil {
		return fmt.Errorf("marshal Slack payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+p.botToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack chat.postMessage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err = io.ReadAll(resp.Body); err != nil {
		return fmt.Errorf("read slack response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack chat.postMessage status %d", resp.StatusCode)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// SSM helper
// ─────────────────────────────────────────────────────────────

func fetchSSMParameter(ctx context.Context, client *ssm.Client, path string) (string, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(path),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("get SSM parameter %q: %w", path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("SSM parameter %q is nil", path)
	}
	return *out.Parameter.Value, nil
}
