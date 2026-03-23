// Package main implements the km github-token-refresher Lambda.
//
// The Lambda runs every 45 minutes via EventBridge Scheduler for each active sandbox
// that has sourceAccess.github configured in its SandboxProfile.
//
// It:
//  1. Reads the event payload (sandbox_id, installation_id, ssm_parameter_name,
//     kms_key_arn, allowed_repos, permissions).
//  2. Reads the GitHub App private key PEM from SSM at /km/config/github/private-key.
//  3. Reads the GitHub App client ID from SSM at /km/config/github/app-client-id.
//  4. Calls GenerateGitHubAppJWT to mint a short-lived RS256 JWT.
//  5. Calls ExchangeForInstallationToken to obtain a scoped installation token.
//  6. Calls WriteTokenToSSM with overwrite=true to store the refreshed token.
//  7. Logs a structured JSON audit event to CloudWatch (captured from Lambda stdout).
//
// Failure mode: non-fatal for the sandbox — if refresh fails, the existing token
// remains valid until its 1-hour GitHub expiry. The 45-minute schedule leaves 15
// minutes of buffer. Errors are logged and returned so Lambda marks the invocation
// as failed (visible in CloudWatch Logs Insights and EventBridge metrics).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-lambda-go/lambda"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	githubpkg "github.com/whereiskurt/klankrmkr/pkg/github"
)

// ============================================================
// SSM config paths
// ============================================================

const (
	// ssmPrivateKeyPath is the SSM path for the GitHub App RSA private key PEM.
	ssmPrivateKeyPath = "/km/config/github/private-key"
	// ssmAppClientIDPath is the SSM path for the GitHub App client ID (used as JWT issuer).
	ssmAppClientIDPath = "/km/config/github/app-client-id"
)

// ============================================================
// Narrow SSM GetParameter interface
// ============================================================

// SSMGetAPI is a narrow interface for the SSM GetParameter call.
// Only used in main() to read config at startup; testability is
// handled by injecting pre-parsed values into TokenRefreshHandler.
type SSMGetAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// readSSMSecret reads a SecureString parameter from SSM with decryption.
func readSSMSecret(ctx context.Context, client SSMGetAPI, path string) (string, error) {
	withDecryption := true
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &path,
		WithDecryption: &withDecryption,
	})
	if err != nil {
		return "", fmt.Errorf("github-token-refresher: read SSM %q: %w", path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("github-token-refresher: SSM %q returned nil value", path)
	}
	return *out.Parameter.Value, nil
}

// ============================================================
// Lambda entrypoint
// ============================================================

func main() {
	ctx := context.Background()
	awsProfile := os.Getenv("KM_AWS_PROFILE") // empty in Lambda — uses execution role

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		slog.Error("failed to load AWS config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	ssmClient := ssm.NewFromConfig(awsCfg)

	// Read GitHub App credentials from SSM at Lambda startup.
	// These are operator-level secrets stored at /km/config/github/ prefix.
	privateKeyPEM, err := readSSMSecret(ctx, ssmClient, ssmPrivateKeyPath)
	if err != nil {
		slog.Error("failed to read GitHub App private key from SSM",
			slog.String("path", ssmPrivateKeyPath),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	appClientID, err := readSSMSecret(ctx, ssmClient, ssmAppClientIDPath)
	if err != nil {
		slog.Error("failed to read GitHub App client ID from SSM",
			slog.String("path", ssmAppClientIDPath),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	// Build the handler with structured JSON logging to stdout (captured by CloudWatch).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	h := &githubpkg.TokenRefreshHandler{
		SSMClient:     ssmClient,
		Logger:        logger,
		AppClientID:   appClientID,
		PrivateKeyPEM: []byte(privateKeyPEM),
		// GenerateJWTFn is nil — the real GenerateGitHubAppJWT will be used.
	}

	lambda.Start(h.HandleTokenRefresh)
}
