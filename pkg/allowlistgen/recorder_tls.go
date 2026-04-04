//go:build linux

package allowlistgen

import (
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/tls"
)

// HandleTLSEvent implements tls.EventHandler. It processes outbound (DirWrite)
// TLS events, parses HTTP/1.1 requests from the payload, and records the host
// and any GitHub repo accessed.
//
// DirRead events and non-HTTP payloads are silently ignored (return nil).
func (r *Recorder) HandleTLSEvent(event *tls.TLSEvent) error {
	// Only process outbound writes — inbound reads are responses, not requests.
	if event.Direction != tls.DirWrite {
		return nil
	}

	req, err := tls.ParseHTTPRequest(event.PayloadBytes())
	if err != nil {
		// Not an HTTP request — silently skip.
		return nil
	}

	r.RecordHost(req.Host)

	owner, repo := tls.ExtractGitHubRepo(req.Host, req.Path)
	if owner != "" {
		r.RecordRepo(owner + "/" + repo)
	}

	return nil
}
