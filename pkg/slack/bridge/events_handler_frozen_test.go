// Package bridge — events_handler_frozen_test.go
// Wave 0 test stub for BRG-02: bridge checks action_frozen before dispatch.
// The body is guarded with t.Skip("plan 06") because the production code
// (ActionFrozen field on SandboxRoutingInfo + frozen-dispatch gate in
// EventsHandler.handleEvent) is implemented in plan 06.
package bridge

import (
	"testing"
)

// TestFrozenDispatch (BRG-02) — when SandboxRoutingInfo.ActionFrozen is true,
// the events handler must:
//  1. Refuse to enqueue the turn to SQS (no SQS SendMessage call).
//  2. Post an in-thread control-plane frozen notice via the bot token:
//     "🛑 This sandbox is frozen (reason). No further actions or replies
//     until your operator releases it." (or equivalent wording).
//  3. Return HTTP 200 (the event was processed; it was intentionally dropped).
//
// This is a control-plane path — the frozen notice itself is never counted
// against the slack_post quota (dual-stream, §5 of CONTEXT.md).
func TestFrozenDispatch(t *testing.T) {
	t.Skip("plan 06: ActionFrozen field on SandboxRoutingInfo + frozen gate not yet implemented")

	// When implemented, assert using the existing fakeSandboxes / fakeQueueSender /
	// fakeSlackPoster pattern from events_handler_test.go:
	//
	// 1. Build a fakeSandboxes that returns SandboxRoutingInfo{
	//      SandboxID: "sb-frozen", ActionFrozen: true,
	//      FrozenReason: "quota: github_pr lifetime exceeded",
	//    }.
	// 2. Send a valid inbound message event to Handle().
	// 3. Assert fakeQueueSender.sendCalls == 0 (no SQS dispatch).
	// 4. Assert fakeSlackPoster received a chat.postMessage call to the
	//    originating channel/thread containing the frozen notice text.
	// 5. Assert Handle() returned EventsResponse{StatusCode: 200}.
}
