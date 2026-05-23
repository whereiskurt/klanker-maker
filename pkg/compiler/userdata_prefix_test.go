package compiler

import "testing"

// TestPoller_PrefixParser_TableDriven covers the locked grammar
// `^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]?` across 11 representative
// inputs per 70-RESEARCH.md Pitfall 4.
// SC-8/SC-9: Plan 70-06 Task 2 implements; Wave 0 baseline stub.
func TestPoller_PrefixParser_TableDriven(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 2")
}

// TestPoller_PrefixParser_AnchoredAtStart guards Pitfall 4: a Slack message
// asking "what does claude: mean?" must NOT trigger routing.
// SC-8/SC-9: Plan 70-06 Task 2 implements; Wave 0 baseline stub.
func TestPoller_PrefixParser_AnchoredAtStart(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 2")
}

// TestPoller_TopLevelPrefix_FreshThread (SC-8): on a fresh top-level message
// (no DDB row), a matching prefix overrides EFFECTIVE_AGENT for that thread;
// profile's KM_AGENT on disk unchanged; new DDB row carries agent_type=requested.
// Plan 70-06 Task 2 implements; Wave 0 baseline stub.
func TestPoller_TopLevelPrefix_FreshThread(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 2")
}

// TestPoller_SameAgentPrefix_NoOp (SC-9): `claude: ...` inside an existing
// claude-rooted thread strips the prefix + dispatches the SAME agent in the
// SAME thread; no handoff post, no new thread, no new DDB row.
// Plan 70-06 Task 2 implements; Wave 0 baseline stub.
func TestPoller_SameAgentPrefix_NoOp(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 2")
}

// TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst (SC-10): the
// cross-agent switch block in the poller bash must call `km-slack permalink`
// (for the OLD thread, with the already-known THREAD_TS) BEFORE it calls
// `km-slack post --new-message`. This satisfies the CONTEXT.md locked
// requirement that the NEW top-level's body contains `Continuing from
// <permalink-to-old-thread>` at post-time — no placeholder pattern,
// no chat.update in the critical path.
// Plan 70-06 Task 3 implements; Wave 0 baseline stub.
func TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 3")
}

// TestPoller_CrossAgentSwitch_OldRowUntouched (SC-10): the cross-agent block
// must NOT call `aws dynamodb update-item` or `aws dynamodb delete-item` on
// the OLD thread_ts. The old DDB row stays resumable.
// Plan 70-06 Task 3 implements; Wave 0 baseline stub.
func TestPoller_CrossAgentSwitch_OldRowUntouched(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-06 Task 3")
}
