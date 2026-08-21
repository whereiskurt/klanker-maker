package main

import (
	"os"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	dnsproxy "github.com/whereiskurt/klanker-maker/sidecars/dns-proxy/dnsproxy"
	"github.com/miekg/dns"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// JSON-only output to stdout.
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	upstream := getEnv("UPSTREAM_DNS", "169.254.169.253")
	port := getEnv("DNS_PORT", "53")
	sandboxID := getEnv("SANDBOX_ID", "unknown")

	allowedSuffixes := splitCSV(os.Getenv("ALLOWED_SUFFIXES"))
	// DENIED_SUFFIXES is absent on any sandbox whose profile declares no denies,
	// which leaves the list empty and the behaviour identical to before.
	deniedSuffixes := splitCSV(os.Getenv("DENIED_SUFFIXES"))

	// KM_NETPOLICY_FILE is set only when the profile enables runtime narrowing.
	// The file itself may not exist yet — a sandbox that has never narrowed
	// itself reads as no runtime denies.
	var runtimeStore *netpolicy.Store
	if p := os.Getenv("KM_NETPOLICY_FILE"); p != "" {
		runtimeStore = netpolicy.NewStore(p, netpolicy.DefaultReloadInterval)
	}

	// A nil denier when neither source is configured keeps the hot path free of
	// any deny work at all.
	var denier *netpolicy.Denier
	if len(deniedSuffixes) > 0 || runtimeStore != nil {
		denier = netpolicy.NewDenier(deniedSuffixes, runtimeStore)
	}

	handler := dnsproxy.NewHandler(allowedSuffixes, denier, upstream, sandboxID)
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)

	addr := ":" + port
	log.Info().
		Str("event_type", "dns_proxy_start").
		Str("addr", addr).
		Str("upstream", upstream).
		Strs("allowed_suffixes", allowedSuffixes).
		Strs("denied_suffixes", deniedSuffixes).
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

// splitCSV parses a comma-separated env value into a trimmed, empty-free list.
// An unset or empty value yields a nil slice.
func splitCSV(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
