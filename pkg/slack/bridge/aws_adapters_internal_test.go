package bridge

// aws_adapters_internal_test.go — Phase 67.2 internal tests that need
// access to package-private symbols (reactionErrorClass + classifyReactionError).
//
// Lives in `package bridge` (not `bridge_test`) so the table-driven test can
// reference the lowercase enum values directly without an exported test-only
// shim. The bulk of SlackReactorAdapter tests stay in aws_adapters_test.go
// (`package bridge_test`); only the classifier moat lives here.

import (
	"errors"
	"testing"
)

// TestClassifyReactionError exercises every branch of classifyReactionError's
// locked taxonomy (67.2-CONTEXT.md § "Error classification (locked)") plus
// the unknown-string default-transient policy.
func TestClassifyReactionError(t *testing.T) {
	boom := errors.New("network boom")
	cases := []struct {
		name   string
		status int
		apiErr string
		netErr error
		want   reactionErrorClass
	}{
		// Network error wins regardless of status / apiErr.
		{"net error wins", 0, "", boom, classTransient},
		{"net error wins even on 500", 500, "anything", boom, classTransient},

		// 429 → classRateLimited regardless of apiErr.
		{"429 with apiErr=ratelimited", 429, "ratelimited", nil, classRateLimited},
		{"429 with empty apiErr", 429, "", nil, classRateLimited},

		// 5xx → classTransient.
		{"500", 500, "", nil, classTransient},
		{"503 with body err", 503, "internal_error", nil, classTransient},
		{"599 edge of 5xx range", 599, "anything", nil, classTransient},

		// Success: HTTP 200 + ok or already_reacted (Phase 67.1 idempotency).
		{"200 ok", 200, "", nil, classSuccess},
		{"200 already_reacted is idempotent success", 200, "already_reacted", nil, classSuccess},

		// Terminal-auth (operator action required → log at Error).
		{"invalid_auth", 200, "invalid_auth", nil, classTerminalAuth},
		{"missing_scope", 200, "missing_scope", nil, classTerminalAuth},
		{"token_expired", 200, "token_expired", nil, classTerminalAuth},
		{"ekm_access_denied", 200, "ekm_access_denied", nil, classTerminalAuth},

		// Terminal-bad-input (unrecoverable client-side → log at Warn).
		{"bad_timestamp", 200, "bad_timestamp", nil, classTerminalBadInput},
		{"message_not_found", 200, "message_not_found", nil, classTerminalBadInput},
		{"invalid_name", 200, "invalid_name", nil, classTerminalBadInput},
		{"is_archived", 200, "is_archived", nil, classTerminalBadInput},
		{"no_access", 200, "no_access", nil, classTerminalBadInput},

		// Transient codes (retryable).
		{"internal_error", 200, "internal_error", nil, classTransient},
		{"service_unavailable", 200, "service_unavailable", nil, classTransient},
		{"fatal_error", 200, "fatal_error", nil, classTransient},
		{"accesslimited", 200, "accesslimited", nil, classTransient},
		{"external_channel_migrating", 200, "external_channel_migrating", nil, classTransient},

		// Default-unknown policy (locked: → classTransient). Cost of one extra
		// retry on an actually-terminal error is acceptable; cost of silently
		// ignoring a new transient signal is not.
		{"unknown new slack code", 200, "some_new_thing", nil, classTransient},
		{"another unknown", 200, "what_is_this", nil, classTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyReactionError(c.status, c.apiErr, c.netErr)
			if got != c.want {
				t.Errorf("classifyReactionError(%d, %q, %v) = %d, want %d",
					c.status, c.apiErr, c.netErr, got, c.want)
			}
		})
	}
}
