// Package cmd_test — freeze_test.go
// Wave 0 test stubs for CLI-01 (km freeze) and CLI-02 (km unlock latch-aware).
// Both are guarded with t.Skip("plan 10") because the production functions
// (RunFreeze / the latch-aware RunUnlock) are implemented in plan 10.
package cmd_test

import (
	"testing"
)

// TestRunFreeze (CLI-01) — asserts km freeze <id> writes action_frozen=true
// plus frozen_reason/frozen_at/frozen_by onto the km-sandboxes row via
// an atomic DynamoDB UpdateItem (not a full-row PutItem).
func TestRunFreeze(t *testing.T) {
	t.Skip("plan 10: RunFreeze command not yet implemented")

	// When implemented, assert:
	// 1. A mock DDB UpdateItemClient receives exactly one UpdateItem call.
	// 2. The UpdateExpression sets action_frozen=:true, frozen_at=:now,
	//    frozen_by=:reason, frozen_reason=:reason (or similar attrs).
	// 3. The update uses SET (not PutItem / full-row replace) so existing
	//    attrs (e.g. action_limits) are preserved.
	// 4. A sandbox ID that is not a valid km sandbox ID returns an error
	//    without calling UpdateItem.
}

// TestRunUnlockLatchAware (CLI-02) — asserts km unlock <id> clears
// action_frozen alongside the existing safety lock (locked=false).
func TestRunUnlockLatchAware(t *testing.T) {
	t.Skip("plan 10: latch-aware RunUnlock not yet implemented")

	// When implemented, assert:
	// 1. A mock DDB UpdateItemClient receives exactly one UpdateItem call.
	// 2. The UpdateExpression removes or sets action_frozen to false/absent
	//    AND clears the safety lock in the same or a subsequent atomic write.
	// 3. The unlock command reports both cleared attrs in its output (so the
	//    operator sees "cleared: safety lock + quarantine freeze").
	// 4. km unlock on a sandbox that has a safety lock but NO quarantine freeze
	//    still completes successfully (back-compat with pre-Phase-121 boxes).
}
