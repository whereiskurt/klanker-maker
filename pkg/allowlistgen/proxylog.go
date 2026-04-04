package allowlistgen

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// proxyLogEntry represents a single zerolog JSON line from a proxy container.
type proxyLogEntry struct {
	EventType string `json:"event_type"`
	Domain    string `json:"domain"`  // dns_query events
	Host      string `json:"host"`    // CONNECT / blocked events
	Owner     string `json:"owner"`   // github_repo_allowed events
	Repo      string `json:"repo"`    // github_repo_allowed events
	Allowed   bool   `json:"allowed"` // dns_query events
}

// ParseProxyLogs reads zerolog JSON lines from DNS proxy and HTTP proxy log
// readers and feeds observed traffic into the provided Recorder. This is the
// Docker substrate equivalent of the eBPF TLS handler + DNS observer path
// used on EC2.
//
// Either reader may be nil (skipped). Returns an error only on malformed JSON
// that prevents further parsing; individual malformed lines are skipped.
func ParseProxyLogs(dnsLogs, httpLogs io.Reader, recorder *Recorder) error {
	if dnsLogs != nil {
		if err := parseDNSProxyLogs(dnsLogs, recorder); err != nil {
			return fmt.Errorf("parse dns proxy logs: %w", err)
		}
	}
	if httpLogs != nil {
		if err := parseHTTPProxyLogs(httpLogs, recorder); err != nil {
			return fmt.Errorf("parse http proxy logs: %w", err)
		}
	}
	return nil
}

// parseDNSProxyLogs scans zerolog JSON lines from a DNS proxy container and
// records all dns_query events into the Recorder (both allowed and denied).
func parseDNSProxyLogs(r io.Reader, rec *Recorder) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry proxyLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip non-JSON or malformed lines (debug output, etc.)
			continue
		}
		if entry.EventType == "dns_query" && entry.Domain != "" {
			rec.RecordDNSQuery(entry.Domain)
		}
	}
	return scanner.Err()
}

// parseHTTPProxyLogs scans zerolog JSON lines from an HTTP proxy container and
// records github_repo_allowed, CONNECT, and blocked events into the Recorder.
func parseHTTPProxyLogs(r io.Reader, rec *Recorder) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry proxyLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip non-JSON or malformed lines.
			continue
		}
		switch {
		case entry.EventType == "github_repo_allowed" && entry.Owner != "" && entry.Repo != "":
			rec.RecordRepo(entry.Owner + "/" + entry.Repo)
		case entry.Host != "":
			// Any event with a non-empty Host field: strip port and record the host.
			// This covers github_mitm_connect, http_blocked, and any future events.
			host, _, err := net.SplitHostPort(entry.Host)
			if err != nil {
				// No port present — use raw value.
				host = entry.Host
			}
			if host != "" {
				rec.RecordHost(host)
			}
		}
	}
	return scanner.Err()
}
