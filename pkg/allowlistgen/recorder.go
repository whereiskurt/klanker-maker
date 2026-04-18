// Package allowlistgen accumulates observed network traffic and generates
// a minimal SandboxProfile YAML allowlist from the observations.
package allowlistgen

import (
	"net"
	"sort"
	"strings"
	"sync"
)

// Recorder accumulates DNS domains, HTTP hosts, GitHub repos, Git refs, and
// shell commands observed during sandbox execution. All methods are safe for
// concurrent use.
type Recorder struct {
	mu              sync.Mutex
	dnsObserved     map[string]struct{}
	hostObserved    map[string]struct{}
	repoObserved    map[string]struct{}
	refObserved     map[string]struct{}
	commandSeen     map[string]struct{}
	commandOrdered  []string
}

// NewRecorder returns an initialised, empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		dnsObserved:    make(map[string]struct{}),
		hostObserved:   make(map[string]struct{}),
		repoObserved:   make(map[string]struct{}),
		refObserved:    make(map[string]struct{}),
		commandSeen:    make(map[string]struct{}),
		commandOrdered: []string{},
	}
}

// RecordDNSQuery records a DNS query domain. The trailing dot (FQDN) is
// stripped and the domain is lowercased before storage.
func (r *Recorder) RecordDNSQuery(domain string) {
	d := strings.ToLower(strings.TrimSuffix(domain, "."))
	if d == "" {
		return
	}
	r.mu.Lock()
	r.dnsObserved[d] = struct{}{}
	r.mu.Unlock()
}

// RecordHost records an HTTP host (with optional port). The port is stripped
// and the host is lowercased before storage.
func (r *Recorder) RecordHost(hostPort string) {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		// No port — use raw value.
		host = hostPort
	}
	h := strings.ToLower(host)
	if h == "" {
		return
	}
	r.mu.Lock()
	r.hostObserved[h] = struct{}{}
	r.mu.Unlock()
}

// RecordRepo records a GitHub owner/repo pair (lowercased).
func (r *Recorder) RecordRepo(ownerRepo string) {
	key := strings.ToLower(ownerRepo)
	if key == "" {
		return
	}
	r.mu.Lock()
	r.repoObserved[key] = struct{}{}
	r.mu.Unlock()
}

// RecordRef records a Git ref (branch or tag name). The "refs/heads/" and
// "refs/tags/" prefixes are stripped, and the ref is stored as-is (case-preserved).
func (r *Recorder) RecordRef(ref string) {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	if ref == "" {
		return
	}
	r.mu.Lock()
	r.refObserved[ref] = struct{}{}
	r.mu.Unlock()
}

// Refs returns a sorted, deduplicated slice of all observed Git refs.
func (r *Recorder) Refs() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.refObserved))
	for ref := range r.refObserved {
		out = append(out, ref)
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out
}

// DNSDomains returns a sorted, deduplicated slice of all observed DNS domains.
func (r *Recorder) DNSDomains() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.dnsObserved))
	for d := range r.dnsObserved {
		out = append(out, d)
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out
}

// Hosts returns a sorted, deduplicated slice of all observed HTTP hosts.
func (r *Recorder) Hosts() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.hostObserved))
	for h := range r.hostObserved {
		out = append(out, h)
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out
}

// Repos returns a sorted, deduplicated slice of all observed GitHub repos.
func (r *Recorder) Repos() []string {
	r.mu.Lock()
	out := make([]string, 0, len(r.repoObserved))
	for repo := range r.repoObserved {
		out = append(out, repo)
	}
	r.mu.Unlock()
	sort.Strings(out)
	return out
}

// RecordCommand records a shell command. Empty or whitespace-only commands are
// ignored. Duplicate commands are deduplicated while preserving first-seen order.
func (r *Recorder) RecordCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	r.mu.Lock()
	if _, exists := r.commandSeen[cmd]; !exists {
		r.commandSeen[cmd] = struct{}{}
		r.commandOrdered = append(r.commandOrdered, cmd)
	}
	r.mu.Unlock()
}

// Commands returns a deduplicated slice of all recorded shell commands in
// first-seen order. Unlike DNS/host/repo accessors, Commands does NOT sort —
// command order is semantically meaningful. Returns an empty (non-nil) slice
// when no commands have been recorded.
func (r *Recorder) Commands() []string {
	r.mu.Lock()
	out := make([]string, len(r.commandOrdered))
	copy(out, r.commandOrdered)
	r.mu.Unlock()
	return out
}
