package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	// Budget enforcement is opt-in via KM_BUDGET_ENABLED=true.
	if strings.EqualFold(getEnv("KM_BUDGET_ENABLED", "false"), "true") {
		tableName := getEnv("KM_BUDGET_TABLE", "km-budgets")

		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Fatal().Err(err).Msg("failed to load AWS config for budget enforcement")
		}

		dynClient := dynamodb.NewFromConfig(cfg)

		// Load Bedrock model rates from the static fallback (nil pricing client).
		// TODO: wire a real pricing.Client (us-east-1) for live rate lookups.
		modelRates, err := aws.GetBedrockModelRates(context.Background(), nil)
		if err != nil {
			log.Warn().Err(err).Msg("failed to load Bedrock model rates; budget cost calculations may be inaccurate")
			modelRates = make(map[string]aws.BedrockModelRate)
		}

		// Write remaining AI budget to /run/km/budget_remaining on each cache refresh.
		// TODO: custom CA support — read KM_PROXY_CA_CERT (base64 PEM) and set proxy.CertStore.
		onBudgetUpdate := func(remaining float64) {
			path := "/run/km/budget_remaining"
			if err := os.MkdirAll("/run/km", 0o755); err == nil {
				_ = os.WriteFile(path, []byte(fmt.Sprintf("%.6f", remaining)), 0o644)
			}
		}

		proxyOpts = append(proxyOpts, httpproxy.WithBudgetEnforcement(dynClient, tableName, modelRates, onBudgetUpdate))

		log.Info().
			Str("event_type", "budget_enforcement_enabled").
			Str("sandbox_id", sandboxID).
			Str("table", tableName).
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

	if err := http.ListenAndServe(addr, proxy); err != nil {
		log.Fatal().Err(err).Msg("http proxy server error")
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
