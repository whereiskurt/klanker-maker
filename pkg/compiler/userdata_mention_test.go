package compiler

import "testing"

// TestResolveMentionOnly stubs the Wave 0 contract for POL-04 and POL-11.
// Plan 91-02 will implement the real test once resolveMentionOnly(*profile.CLISpec) bool
// helper exists in pkg/compiler/userdata.go.
//
// 9-case table (Mode × override) per RESEARCH.md Pattern 4 + Q5:
//
//	Case 1: shared-channel mode,   override nil    → false (mention-only off)
//	Case 2: shared-channel mode,   override &true  → true  (mention-only on)
//	Case 3: shared-channel mode,   override &false → false (mention-only off)
//	Case 4: per-sandbox mode,      override nil    → false (mention-only off)
//	Case 5: per-sandbox mode,      override &true  → true  (mention-only on)
//	Case 6: per-sandbox mode,      override &false → false (mention-only off)
//	Case 7: channel-override mode, override nil    → true  (mention-only on by default)
//	Case 8: channel-override mode, override &true  → true  (mention-only on)
//	Case 9: channel-override mode, override &false → false (mention-only explicitly off)
func TestResolveMentionOnly(t *testing.T) {
	t.Skip("TODO Plan 91-02: implement once resolveMentionOnly(*profile.CLISpec) bool helper exists")
}

// TestMentionOnlyCompiler stubs the Wave 0 contract for POL-04 and POL-11.
// Plan 91-02 will implement the real test once the compiler emits KM_SLACK_MENTION_ONLY
// to the bridge config — assert env value matches resolveMentionOnly() output for each
// profile fixture.
func TestMentionOnlyCompiler(t *testing.T) {
	t.Skip("TODO Plan 91-02: implement once compiler emits KM_SLACK_MENTION_ONLY to bridge config — assert env value matches resolveMentionOnly() output for each profile fixture")
}
