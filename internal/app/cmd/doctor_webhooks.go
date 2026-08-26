// Package cmd — doctor_webhooks.go
// Phase 127: km doctor checks for the generic webhook ingress bridge
// (config.WebhooksConfig / km-config.yaml `webhooks:` block).
//
// Two functions, split the same way checkLaunchAccountLinks /
// checkLaunchAccountAssumable split structural vs. live-credential checks:
//
//   - checkWebhookSources is pure/offline (no ctx, no AWS client): structural
//     validation of the `webhooks:` block plus the filesystem-backed
//     rule-profile-exists check, mirroring checkGitHubEventsValid's identical
//     os.Stat-against-configDir pattern (a filesystem stat is not an AWS call,
//     so it belongs with the other pure checks, not the AWS-touching group).
//   - checkWebhookSourcesAWS makes the genuinely live AWS calls: SSM secret
//     existence per source, the bridge Function URL recorded in SSM, and DLQ
//     depth.
//
// BOTH gate on the identical `len(wh.Sources) == 0` early return, so a
// dormant install (no `webhooks:` block in km-config.yaml) costs zero checks
// and zero AWS calls — matching the launch_accounts precedent
// (project_action_quota_intent's "dormant by default" invariant applied here).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkWebhookSources validates the webhooks: block structurally, including
// the filesystem-backed rule-profile-exists check. Returns an EMPTY slice
// when no sources are configured.
//
// configDir resolves a rule's relative `profile:` path (mirrors
// checkGitHubEventsValid — pass filepath.Dir(cfg.GetConfigFilePath()), or "."
// when the config file path is unknown).
func checkWebhookSources(wh appcfg.WebhooksConfig, configDir string) []CheckResult {
	if len(wh.Sources) == 0 {
		return nil
	}

	var results []CheckResult
	seen := map[string]bool{}

	for _, src := range wh.Sources {
		label := fmt.Sprintf("webhook source %q", src.Name)
		var issues []string

		switch {
		case src.Name == "":
			issues = append(issues, "source has an empty name; it can never be routed")
		case strings.ContainsAny(src.Name, "/?#% "):
			issues = append(issues, "name is not URL-path-safe; it is the POST path segment, so it must contain no /, ?, #, %, or spaces")
		}

		if src.Name != "" {
			key := strings.ToLower(src.Name)
			if seen[key] {
				issues = append(issues, "duplicate source name (case-insensitive); only the first is reachable")
			}
			seen[key] = true
		}

		switch strings.ToLower(src.Auth.Type) {
		case "bearer", "hmac":
		default:
			issues = append(issues, fmt.Sprintf("unknown auth.type %q (want bearer or hmac); every request will fail closed", src.Auth.Type))
		}

		if src.Auth.SecretPath == "" {
			issues = append(issues, "auth.secret_path is empty; every request fails closed until it names an SSM SecureString")
		}

		if len(src.Rules) == 0 {
			issues = append(issues, "source has no rules; every payload is dropped")
		}

		for i, r := range src.Rules {
			results = append(results, checkWebhookRule(label, i, r, configDir)...)
		}

		for _, msg := range issues {
			results = append(results, CheckResult{Name: label, Status: CheckWarn, Message: msg})
		}
		if len(issues) == 0 {
			results = append(results, CheckResult{
				Name:    label,
				Status:  CheckOK,
				Message: fmt.Sprintf("%d rule(s) configured", len(src.Rules)),
			})
		}
	}

	results = append(results, checkWebhookRateLimit(wh.RateLimit))

	return results
}

// checkWebhookRule validates one rule of one source. Split out of
// checkWebhookSources so each defect stands as its own CheckResult (rather
// than one long joined message per source) — a WARN naming exactly the
// offending field, matching the quality bar for a doctor check.
func checkWebhookRule(sourceLabel string, i int, r appcfg.WebhookRule, configDir string) []CheckResult {
	label := fmt.Sprintf("%s rule[%d]", sourceLabel, i)
	var results []CheckResult

	if r.Alias == "" {
		results = append(results, CheckResult{Name: label, Status: CheckWarn, Message: "rule has no alias; nothing can be dispatched"})
	}
	if r.Prompt == "" {
		results = append(results, CheckResult{Name: label, Status: CheckWarn, Message: "rule has no prompt; the agent turn would be empty"})
	}

	skip := strings.EqualFold(r.OnAbsent, "skip")
	if !skip && r.Profile == "" {
		results = append(results, CheckResult{
			Name:    label,
			Status:  CheckWarn,
			Message: "on_absent is cold-create (the default) but no profile is set; an absent alias cannot be created",
		})
	}
	// Filesystem-backed profile-exists check (mirrors checkGitHubEventsValid):
	// only meaningful once a profile is actually named and the alias isn't
	// allowed to simply be skipped.
	if !skip && r.Profile != "" {
		profilePath := r.Profile
		if !filepath.IsAbs(profilePath) && configDir != "" {
			profilePath = filepath.Join(configDir, profilePath)
		}
		if _, err := os.Stat(profilePath); err != nil {
			results = append(results, CheckResult{
				Name:    label,
				Status:  CheckWarn,
				Message: fmt.Sprintf("profile %q not found: %v; cold-create will fail when the alias is absent", r.Profile, err),
			})
		}
	}

	if r.CooldownSeconds > 0 && r.GroupBy == "" {
		results = append(results, CheckResult{
			Name:    label,
			Status:  CheckWarn,
			Message: "cooldown_seconds is set without group_by; suppression falls back to per-delivery keys and will rarely fire",
		})
	}

	return results
}

// checkWebhookRateLimit validates the install-wide rate_limit block. nil
// (absent from km-config.yaml) is OMITTED entirely — no dedicated
// CheckResult is produced, since "no ceiling configured" is not itself a
// defect, unlike max_dispatches/window_seconds explicitly set to <= 0.
//
// The <= 0 disjunction mirors pkg/webhook (CheckRate): the ceiling is
// disabled when EITHER max_dispatches OR window_seconds is <= 0.
func checkWebhookRateLimit(rl *appcfg.WebhookRateLimit) CheckResult {
	name := "webhook rate_limit"
	if rl == nil {
		return CheckResult{Name: name, Status: CheckOK, Message: "not configured (no ceiling)"}
	}
	switch {
	case rl.MaxDispatches <= 0:
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: "max_dispatches must be > 0; the ceiling is disabled as configured",
		}
	case rl.WindowSeconds <= 0:
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: "window_seconds must be > 0; the ceiling is disabled as configured",
		}
	default:
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d dispatches per %ds", rl.MaxDispatches, rl.WindowSeconds),
		}
	}
}

// checkWebhookSourcesAWS runs the AWS-touching webhook doctor probes: SSM
// secret existence per source, the bridge Function URL recorded in SSM, and
// DLQ depth. Gated on the identical len(wh.Sources)==0 early return as
// checkWebhookSources so a dormant install still makes ZERO AWS calls.
func checkWebhookSourcesAWS(ctx context.Context, wh appcfg.WebhooksConfig, ssmClient SSMReadAPI, ssmPrefix string, sqsClient kmaws.SQSClient, resourcePrefix string) []CheckResult {
	if len(wh.Sources) == 0 {
		return nil
	}

	var results []CheckResult

	for _, src := range wh.Sources {
		results = append(results, checkWebhookSecretExists(ctx, src, ssmClient))
	}

	results = append(results, checkWebhookBridgeURL(ctx, ssmClient, ssmPrefix))
	results = append(results, checkWebhookDLQDepth(ctx, sqsClient, resourcePrefix))

	return results
}

// checkWebhookSecretExists verifies one source's auth.secret_path resolves to
// an existing SSM parameter. An empty secret_path is not re-flagged here —
// checkWebhookSources already WARNs on that structurally; probing an empty
// path would just be a redundant, confusing SKIP.
func checkWebhookSecretExists(ctx context.Context, src appcfg.WebhookSource, client SSMReadAPI) CheckResult {
	name := fmt.Sprintf("webhook source %q secret", src.Name)
	if src.Auth.SecretPath == "" {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "no secret_path configured — flagged separately by the structural check"}
	}
	if client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "SSM client not available"}
	}
	_, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(src.Auth.SecretPath),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return CheckResult{
				Name:        name,
				Status:      CheckWarn,
				Message:     fmt.Sprintf("secret %q does not exist; every request fails closed until it is created", src.Auth.SecretPath),
				Remediation: fmt.Sprintf("aws ssm put-parameter --name %q --type SecureString --value <token-or-hmac-key>", src.Auth.SecretPath),
			}
		}
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not read %q: %v", src.Auth.SecretPath, err)}
	}
	return CheckResult{Name: name, Status: CheckOK, Message: "secret present"}
}

// checkWebhookBridgeURL mirrors checkGitHubBridgeURL exactly, scoped to the
// webhook bridge's own SSM path ({prefix}config/webhooks/bridge-url, written
// by `km init`).
func checkWebhookBridgeURL(ctx context.Context, client SSMReadAPI, ssmPrefix string) CheckResult {
	name := "Webhook Bridge URL"
	if client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "SSM client not available"}
	}
	param := ssmPrefix + "config/webhooks/bridge-url"
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: awssdk.String(param)})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return CheckResult{
				Name:        name,
				Status:      CheckWarn,
				Message:     "bridge URL not configured in SSM — the operator has no URL to paste into the source's integration",
				Remediation: "Run 'km init --dry-run=false' to deploy the webhook bridge Lambda",
			}
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("could not read %q: %v", param, err),
			Remediation: "Run 'km init --dry-run=false' to deploy the webhook bridge Lambda",
		}
	}
	url := ""
	if out.Parameter != nil && out.Parameter.Value != nil {
		url = awssdk.ToString(out.Parameter.Value)
	}
	if url == "" {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     "bridge URL parameter exists but is empty",
			Remediation: "Run 'km init --dry-run=false' to deploy the webhook bridge Lambda",
		}
	}
	if !strings.HasPrefix(url, "https://") {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("bridge URL %q is not HTTPS — webhook delivery may fail", url),
		}
	}
	return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("bridge URL configured: %s", url)}
}

// checkWebhookDLQDepth reports the depth of the shared generic-webhook inbound
// DLQ ({prefix}-webhook-inbound-dlq.fifo). Mirrors checkInboundDLQDepth
// (doctor_inbound_dlq.go), scoped to the single webhook DLQ: a missing DLQ is
// SKIP (not yet provisioned), not WARN.
func checkWebhookDLQDepth(ctx context.Context, sqsClient kmaws.SQSClient, resourcePrefix string) CheckResult {
	name := "Webhook inbound DLQ depth"
	if sqsClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "inbound DLQ deps not configured"}
	}
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	qName := kmaws.WebhookInboundDLQName(resourcePrefix)
	qURL, ok, err := resolveQueueURL(ctx, sqsClient, qName)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("could not resolve webhook inbound DLQ %s: %v", qName, err),
		}
	}
	if !ok {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "webhook inbound DLQ not found (not yet provisioned)"}
	}

	depth, dErr := kmaws.QueueDepth(ctx, sqsClient, qURL)
	if dErr != nil {
		var notFound *sqstypes.QueueDoesNotExist
		if errors.As(dErr, &notFound) {
			return CheckResult{Name: name, Status: CheckSkipped, Message: "webhook inbound DLQ not found (not yet provisioned)"}
		}
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not read webhook inbound DLQ depth: %v", dErr)}
	}

	if depth > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d poison message(s) in webhook inbound DLQ", depth),
			Remediation: "Inspect with 'aws sqs receive-message --queue-url <dlq-url>'; once triaged, redrive or 'aws sqs purge-queue --queue-url <dlq-url>'. A poison message indicates an agent turn that failed 3x — check the webhook poller logs.",
		}
	}
	return CheckResult{Name: name, Status: CheckOK, Message: "webhook inbound DLQ present and empty"}
}
