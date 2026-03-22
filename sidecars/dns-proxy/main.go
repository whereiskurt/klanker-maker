package main

import (
	"os"
	"strings"

	dnsproxy "github.com/whereiskurt/klankrmkr/sidecars/dns-proxy/dnsproxy"
	"github.com/miekg/dns"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// JSON-only output to stdout.
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	allowedRaw := os.Getenv("ALLOWED_SUFFIXES")
	upstream := getEnv("UPSTREAM_DNS", "169.254.169.253")
	port := getEnv("DNS_PORT", "53")
	sandboxID := getEnv("SANDBOX_ID", "unknown")

	var allowedSuffixes []string
	for _, s := range strings.Split(allowedRaw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			allowedSuffixes = append(allowedSuffixes, s)
		}
	}

	handler := dnsproxy.NewHandler(allowedSuffixes, upstream, sandboxID)
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)

	addr := ":" + port
	log.Info().
		Str("event_type", "dns_proxy_start").
		Str("addr", addr).
		Str("upstream", upstream).
		Strs("allowed_suffixes", allowedSuffixes).
		Str("sandbox_id", sandboxID).
		Msg("")

	// Start UDP and TCP servers concurrently; block on errors.
	errCh := make(chan error, 2)

	udpServer := &dns.Server{Addr: addr, Net: "udp", Handler: mux}
	tcpServer := &dns.Server{Addr: addr, Net: "tcp", Handler: mux}

	go func() { errCh <- udpServer.ListenAndServe() }()
	go func() { errCh <- tcpServer.ListenAndServe() }()

	if err := <-errCh; err != nil {
		log.Fatal().Err(err).Msg("dns server error")
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
