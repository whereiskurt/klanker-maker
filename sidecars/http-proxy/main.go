package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/quota"
	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

func main() {
	// JSON-only output to stdout.
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	allowedRaw := os.Getenv("ALLOWED_HOSTS")
	port := getEnv("PROXY_PORT", "3128")
	sandboxID := getEnv("SANDBOX_ID", "unknown")

	var allowedHosts []string
	for _, h := range strings.Split(allowedRaw, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			allowedHosts = append(allowedHosts, h)
		}
	}

	// Build proxy options.
	var proxyOpts []httpproxy.ProxyOption

	// Budget enforcement state — hoisted so both goproxy and transparent listener
	// can share the same DynamoDB client, model rates, and budget update callback.
	budgetEnabled := strings.EqualFold(getEnv("KM_BUDGET_ENABLED", "false"), "true")
	var budgetDynClient *dynamodb.Client
	var budgetTableName string
	var budgetModelRates map[string]aws.BedrockModelRate
	var budgetOnUpdate httpproxy.BudgetUpdater

	if budgetEnabled {
		budgetTableName = getEnv("KM_BUDGET_TABLE", "km-budgets")

		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Fatal().Err(err).Msg("failed to load AWS config for budget enforcement")
		}

		budgetDynClient = dynamodb.NewFromConfig(cfg)

		// Load Bedrock model rates from the static fallback (nil pricing client).
		// TODO: wire a real pricing.Client (us-east-1) for live rate lookups.
		budgetModelRates, err = aws.GetBedrockModelRates(context.Background(), nil)
		if err != nil {
			log.Warn().Err(err).Msg("failed to load Bedrock model rates; budget cost calculations may be inaccurate")
			budgetModelRates = make(map[string]aws.BedrockModelRate)
		}

		// Write remaining AI budget to /run/km/budget_remaining on each cache refresh.
		budgetOnUpdate = func(remaining float64) {
			path := "/run/km/budget_remaining"
			if err := os.MkdirAll("/run/km", 0o755); err == nil {
				_ = os.WriteFile(path, []byte(fmt.Sprintf("%.6f", remaining)), 0o644)
			}
		}

		proxyOpts = append(proxyOpts, httpproxy.WithBudgetEnforcement(budgetDynClient, budgetTableName, budgetModelRates, budgetOnUpdate))

		log.Info().
			Str("event_type", "budget_enforcement_enabled").
			Str("sandbox_id", sandboxID).
			Str("table", budgetTableName).
			Msg("")
	}

	// GitHub repo filter: parse KM_GITHUB_ALLOWED_REPOS CSV and enable repo-level filtering.
	githubReposRaw := os.Getenv("KM_GITHUB_ALLOWED_REPOS")
	var githubAllowedRepos []string
	for _, r := range strings.Split(githubReposRaw, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			githubAllowedRepos = append(githubAllowedRepos, r)
		}
	}
	if len(githubAllowedRepos) > 0 {
		proxyOpts = append(proxyOpts, httpproxy.WithGitHubRepoFilter(githubAllowedRepos))
		log.Info().
			Str("event_type", "github_repo_filter_enabled").
			Str("sandbox_id", sandboxID).
			Strs("allowed_repos", githubAllowedRepos).
			Msg("")
	}

	// Action quota enforcement: read KM_QUOTA_TABLE + KM_ACTION_LIMITS from env.
	// When KM_QUOTA_TABLE is set, the proxy counts and optionally blocks
	// github_pr/github_comment/github_review/email_send actions.
	// Absent KM_QUOTA_TABLE ⇒ option not applied ⇒ byte-identical to pre-Phase-121.
	if opt := buildQuotaOption(sandboxID, budgetEnabled, budgetDynClient); opt != nil {
		proxyOpts = append(proxyOpts, opt)
	}

	// Custom CA for MITM: read base64-encoded PEM (cert+key) from env var.
	// The sandbox trusts this CA via update-ca-certificates at boot time.
	if caCertB64 := os.Getenv("KM_PROXY_CA_CERT"); caCertB64 != "" {
		caPEM, err := base64.StdEncoding.DecodeString(caCertB64)
		if err != nil {
			log.Error().Err(err).Msg("KM_PROXY_CA_CERT is not valid base64; using default CA")
		} else {
			proxyOpts = append([]httpproxy.ProxyOption{httpproxy.WithCustomCA(caPEM)}, proxyOpts...)
		}
	}

	// HTTPS-only mode: block plain HTTP requests (port 80).
	// On EC2 the security group enforces this; on Docker we need proxy-level enforcement.
	if strings.EqualFold(getEnv("KM_HTTPS_ONLY", "false"), "true") {
		proxyOpts = append(proxyOpts, httpproxy.WithHTTPSOnly())
		log.Info().
			Str("event_type", "https_only_enabled").
			Str("sandbox_id", sandboxID).
			Msg("")
	}

	proxy := httpproxy.NewProxy(allowedHosts, sandboxID, proxyOpts...)

	addr := ":" + port
	log.Info().
		Str("event_type", "http_proxy_start").
		Str("addr", addr).
		Strs("allowed_hosts", allowedHosts).
		Str("sandbox_id", sandboxID).
		Msg("")

	// Check if BPF maps exist for transparent proxy mode (gatekeeper/both enforcement).
	if strings.EqualFold(getEnv("KM_TRANSPARENT_PROXY", "false"), "true") {
		log.Info().
			Str("event_type", "transparent_proxy_enabled").
			Str("sandbox_id", sandboxID).
			Msg("BPF maps detected — enabling transparent proxy for redirected connections")

		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatal().Err(err).Msg("listen error")
		}
		tl := httpproxy.NewTransparentListener(ln, proxy, sandboxID)
		if len(githubAllowedRepos) > 0 {
			tl.SetGitHubRepos(githubAllowedRepos)
		}
		if budgetEnabled {
			tl.SetBudgetEnforcement(budgetDynClient, budgetTableName, budgetModelRates, budgetOnUpdate)
		}
		if err := tl.Serve(); err != nil {
			log.Fatal().Err(err).Msg("transparent proxy server error")
		}
	} else {
		// Standard explicit proxy mode (no BPF maps — proxy-only enforcement)
		if err := http.ListenAndServe(addr, proxy); err != nil {
			log.Fatal().Err(err).Msg("http proxy server error")
		}
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// buildQuotaOption reads KM_QUOTA_TABLE + KM_ACTION_LIMITS from the environment
// and returns a WithActionQuota ProxyOption when both are set and valid.
// Returns nil when KM_QUOTA_TABLE is absent (dormant — byte-identical to pre-Phase-121).
// budgetDynClient is reused when budgetEnabled is true (avoids a second AWS config load).
func buildQuotaOption(sandboxID string, budgetEnabled bool, budgetDynClient *dynamodb.Client) httpproxy.ProxyOption {
	quotaTable := os.Getenv("KM_QUOTA_TABLE")
	if quotaTable == "" {
		return nil // dormant
	}
	limitsJSON := os.Getenv("KM_ACTION_LIMITS")
	if limitsJSON == "" {
		log.Warn().
			Str("event_type", "quota_limits_missing").
			Str("sandbox_id", sandboxID).
			Msg("KM_QUOTA_TABLE set but KM_ACTION_LIMITS empty; quota enforcement disabled")
		return nil
	}
	var limits quota.Limits
	if err := json.Unmarshal([]byte(limitsJSON), &limits); err != nil {
		log.Warn().
			Err(err).
			Str("event_type", "quota_limits_parse_error").
			Str("sandbox_id", sandboxID).
			Msg("KM_ACTION_LIMITS is not valid JSON; quota enforcement disabled")
		return nil
	}
	// Reuse the budget DDB client when available to avoid an extra AWS config load.
	var dynClient *dynamodb.Client
	if budgetEnabled && budgetDynClient != nil {
		dynClient = budgetDynClient
	} else {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Warn().
				Err(err).
				Str("event_type", "quota_aws_config_error").
				Str("sandbox_id", sandboxID).
				Msg("failed to load AWS config for quota enforcement; quota disabled")
			return nil
		}
		dynClient = dynamodb.NewFromConfig(cfg)
	}
	log.Info().
		Str("event_type", "quota_enforcement_enabled").
		Str("sandbox_id", sandboxID).
		Str("table", quotaTable).
		Int("action_count", len(limits)).
		Msg("")
	return httpproxy.WithActionQuota(dynClient, quotaTable, sandboxID, limits)
}
