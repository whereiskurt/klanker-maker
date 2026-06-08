// webhook_handler_phase102_test.go — Phase 102 handler tests: agent-verb conflict reply
// and envelope.Agent field population.
//
// Tests:
//   TestHandle_AgentVerbConflict: /claude AND /codex in one comment → one PostComment
//     with "Specify one agent", NO SQS enqueue, NO Publisher call.
//   TestHandle_EnvelopeCarriesAgent: single "/claude" comment → envelope has Agent=="claude".
//
// Reuses fixtures from handle_test.go, webhook_handler_phase98_test.go, and
// webhook_handler_phase99_test.go: testSecret, testBotLogin, defaultOpts,
// buildPayloadJSON, buildRequest, mockCommenter, buildHandlerWithCommands, testCommands.
package bridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// TestHandle_AgentVerbConflict: a comment containing BOTH /claude AND /codex must
// result in exactly one PostComment with the "Specify one agent" guidance message,
// and NO dispatch (SQS and Publisher must remain untouched).
func TestHandle_AgentVerbConflict(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] /claude /codex please review this"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter MUST be called with the "Specify one agent" message.
	if !commenter.called {
		t.Error("Commenter must be called for agent-verb conflict")
	}
	if !strings.Contains(commenter.body, "Specify one agent") {
		t.Errorf("conflict reply does not contain 'Specify one agent': %q", commenter.body)
	}

	// Dispatch must NOT happen — conflict short-circuits before envelope construction.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called on agent-verb conflict path")
	}
	if publisher.called {
		t.Error("Publisher must NOT be called on agent-verb conflict path")
	}
}

// TestHandle_EnvelopeCarriesAgent: a single "/claude" verb in the comment must result
// in GitHubEnvelope.Agent == "claude" in the SQS message.
func TestHandle_EnvelopeCarriesAgent(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] /claude please review the auth module"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(nil, "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter must NOT be called (single agent verb = happy path dispatch).
	if commenter.called {
		t.Errorf("Commenter must NOT be called on single-agent-verb path; got body=%q", commenter.body)
	}

	// Dispatch must happen.
	if !sqsSender.called {
		t.Error("SQS.Send must be called on single-agent-verb dispatch path")
	}

	// Verify the envelope carries Agent=="claude".
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(sqsSender.body), &env); err != nil {
		t.Fatalf("SQS body not valid GitHubEnvelope JSON: %v", err)
	}
	if env.Agent != "claude" {
		t.Errorf("envelope.Agent=%q want %q", env.Agent, "claude")
	}

	// The agent verb token must NOT appear in the body (stripped from {{args}}).
	if strings.Contains(env.Body, "/claude") {
		t.Errorf("envelope.Body must not contain '/claude' (must be stripped): %q", env.Body)
	}
}
