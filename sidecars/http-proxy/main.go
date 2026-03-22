package main

import (
	"net/http"
	"os"
	"strings"

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

	proxy := httpproxy.NewProxy(allowedHosts, sandboxID)

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
