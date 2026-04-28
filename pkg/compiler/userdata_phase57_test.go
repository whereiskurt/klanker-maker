package compiler

import (
	"strings"
	"testing"
)

// renderUD57 is a tiny helper that calls generateUserData with stable defaults
// and fails the test on render error. Use this in every Phase 57 test.
func renderUD57(t *testing.T) string {
	t.Helper()
	out, err := generateUserData(emailProfile(), "sb-p57", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// ============================================================
// km-send --no-sign tests (Plan 57-01)
// ============================================================

// TestUserData_KmSend_NoSignFlag verifies km-send defines the --no-sign flag
// with a NO_SIGN=false initialization (research Pattern 2).
func TestUserData_KmSend_NoSignFlag(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "--no-sign") {
		t.Error("expected --no-sign flag handling in km-send")
	}
	if !strings.Contains(out, "NO_SIGN=false") {
		t.Error("expected NO_SIGN=false initialization in km-send")
	}
}

// TestUserData_KmSend_NoSignOmitsHeaders verifies that X-KM-Sender-ID and
// X-KM-Signature header emission are guarded by non-empty variable checks
// so they are omitted when --no-sign is active (research Pattern 2).
func TestUserData_KmSend_NoSignOmitsHeaders(t *testing.T) {
	out := renderUD57(t)
	// Conditional guard before X-KM-Sender-ID emission
	if !strings.Contains(out, `[[ -n "$sender_id" ]] && printf 'X-KM-Sender-ID:`) {
		t.Error("expected conditional X-KM-Sender-ID emission guarded by [[ -n \"$sender_id\" ]]")
	}
	if !strings.Contains(out, `[[ -n "$signature" ]] && printf 'X-KM-Signature:`) {
		t.Error("expected conditional X-KM-Signature emission guarded by [[ -n \"$signature\" ]]")
	}
}

// TestUserData_KmSend_NoSignKeepsAuthPhrase verifies that the KM-AUTH safe-phrase
// auto-append block remains present and is NOT inside an `if ! $NO_SIGN` guard
// (research Pattern 2 — KM-AUTH is independent of Ed25519 signing).
func TestUserData_KmSend_NoSignKeepsAuthPhrase(t *testing.T) {
	out := renderUD57(t)
	// The KM-AUTH block comment must still exist
	if !strings.Contains(out, "# Auto-include KM-AUTH when sending to the operator inbox.") {
		t.Error("expected '# Auto-include KM-AUTH when sending to the operator inbox.' comment in km-send (must remain unguarded by NO_SIGN)")
	}
	// The OPERATOR_INBOX variable must be present
	if !strings.Contains(out, "OPERATOR_INBOX=") {
		t.Error("expected OPERATOR_INBOX= assignment in km-send KM-AUTH block")
	}
	// The KM-AUTH block must appear BEFORE the NO_SIGN signing gate:
	// i.e., OPERATOR_INBOX assignment index must be before `if ! $NO_SIGN` index.
	kmAuthIdx := strings.Index(out, "OPERATOR_INBOX=")
	noSignIdx := strings.Index(out, "if ! $NO_SIGN")
	if kmAuthIdx < 0 {
		t.Error("OPERATOR_INBOX= not found in rendered userdata")
		return
	}
	if noSignIdx < 0 {
		t.Error("'if ! $NO_SIGN' signing gate not found in km-send (Plan 01 must introduce it)")
		return
	}
	if kmAuthIdx > noSignIdx {
		t.Errorf("KM-AUTH block (pos %d) appears AFTER the NO_SIGN signing gate (pos %d) — it must come before", kmAuthIdx, noSignIdx)
	}
}

// ============================================================
// km-recv RFC 5322 + multipart + [EXTERNAL] tests (Plan 57-02)
// ============================================================

// TestUserData_KmRecv_FoldedHeaders verifies that km-recv defines an
// unfold_headers() bash function for RFC 5322 folded header support
// (research Pattern 3).
func TestUserData_KmRecv_FoldedHeaders(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "unfold_headers()") {
		t.Error("expected unfold_headers() bash function defined in km-recv block (research Pattern 3)")
	}
}

// TestUserData_KmRecv_MultipartAlternative verifies that km-recv's extract_body
// handles multipart/alternative as a recognized MIME container type
// (research Pattern 4).
func TestUserData_KmRecv_MultipartAlternative(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "multipart/alternative") {
		t.Error("expected literal 'multipart/alternative' in km-recv extract_body logic (research Pattern 4)")
	}
}

// TestUserData_KmRecv_NestedMultipart verifies that km-recv uses a second-pass
// boundary variable for the nested multipart/alternative scan to avoid
// boundary variable collision (research Pattern 4, Pitfall 6).
func TestUserData_KmRecv_NestedMultipart(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "alt_boundary=") {
		t.Error("expected alt_boundary= variable in km-recv for nested multipart/alternative second-pass (research Pattern 4 / Pitfall 6)")
	}
}

// TestUserData_KmRecv_ExternalDisplay verifies that km-recv shows an [EXTERNAL]
// display hint when X-KM-Sender-ID is absent (research Pattern 5).
func TestUserData_KmRecv_ExternalDisplay(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "[EXTERNAL]") {
		t.Error("expected literal '[EXTERNAL]' substring in km-recv human-readable output (research Pattern 5)")
	}
}

// TestUserData_KmRecv_ExternalJSONField verifies that km-recv's JSON printf
// format string includes an "external" field for agent-readable output
// (research Pattern 5 — Plan 02 Task 3 introduces this field).
func TestUserData_KmRecv_ExternalJSONField(t *testing.T) {
	out := renderUD57(t)
	want := `"external":` + `%s`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q literal in km-recv JSON printf format string (research Pattern 5)", want)
	}
}

// ============================================================
// km-mail-poller safe phrase gate tests (Plan 57-03)
// ============================================================

// TestUserData_MailPoller_ExtractsSenderIdUnconditionally verifies that
// km-mail-poller extracts sender_id outside the KM_ALLOWED_SENDERS block
// so the safe phrase gate has access to it regardless of allowlist config
// (research Pattern 6, Pitfall 5).
func TestUserData_MailPoller_ExtractsSenderIdUnconditionally(t *testing.T) {
	out := renderUD57(t)
	// Plan 03 must hoist sender_id/sender_email extraction with this marker comment:
	if !strings.Contains(out, "# Always extract sender_id for external validation") {
		t.Error("expected unconditional sender_id extraction comment+code in km-mail-poller (research Pattern 6 / Pitfall 5)")
	}
}

// TestUserData_MailPoller_FetchesSafePhrase verifies that km-mail-poller fetches
// the safe phrase from SSM at startup and caches it (research Pattern 6).
func TestUserData_MailPoller_FetchesSafePhrase(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "/km/config/remote-create/safe-phrase") {
		t.Error("expected SSM path '/km/config/remote-create/safe-phrase' in km-mail-poller block")
	}
	if !strings.Contains(out, "KM_SAFE_PHRASE_CACHED=") {
		t.Error("expected KM_SAFE_PHRASE_CACHED= cache variable in km-mail-poller block")
	}
}

// TestUserData_MailPoller_DropsExternalNoPhrase verifies that km-mail-poller
// rejects external email missing the safe phrase and moves it to skipped/
// (research Pattern 6).
func TestUserData_MailPoller_DropsExternalNoPhrase(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "external email") {
		t.Error("expected 'external email' keyword in km-mail-poller rejection log line")
	}
	if !strings.Contains(out, "safe phrase") {
		t.Error("expected 'safe phrase' keyword in km-mail-poller rejection log line")
	}
	if !strings.Contains(out, "MAIL_DIR/skipped") {
		t.Error("expected MAIL_DIR/skipped move in km-mail-poller rejection path (research Pattern 6)")
	}
}

// TestUserData_MailPoller_DeliversExternalWithPhrase verifies that km-mail-poller
// accepts and logs delivery of external email that contains the safe phrase
// (research Pattern 6).
func TestUserData_MailPoller_DeliversExternalWithPhrase(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "safe phrase OK") {
		t.Error("expected 'safe phrase OK' acceptance log line in km-mail-poller (research Pattern 6)")
	}
}

// TestUserData_MailPoller_SkipsCheckForSandbox verifies that the safe phrase gate
// is guarded by a combined check — [ -z "$sender_id" ] AND KM_SAFE_PHRASE_CACHED —
// so sandbox-to-sandbox email bypasses the phrase check (research Pattern 6).
func TestUserData_MailPoller_SkipsCheckForSandbox(t *testing.T) {
	out := renderUD57(t)
	// Plan 03 introduces the combined guard: [ -z "$sender_id" ] && [ -n "${KM_SAFE_PHRASE_CACHED:-}" ]
	if !strings.Contains(out, `[ -z "$sender_id" ] && [ -n "${KM_SAFE_PHRASE_CACHED:-}`) {
		t.Error("expected safe-phrase gate '[ -z \"$sender_id\" ] && [ -n \"${KM_SAFE_PHRASE_CACHED:-}' in km-mail-poller (research Pattern 6)")
	}
}

// TestUserData_MailPoller_SsmFailOpen verifies that the safe phrase SSM fetch
// fails open (2>/dev/null || true) and logs a warning when the phrase is unavailable
// (research Pattern 6, Pitfall 4).
func TestUserData_MailPoller_SsmFailOpen(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "2>/dev/null || true") {
		t.Error("expected '2>/dev/null || true' after SSM safe-phrase get-parameter for fail-open behavior (research Pattern 6)")
	}
	if !strings.Contains(out, "safe phrase not available") {
		t.Error("expected WARN log 'safe phrase not available' in km-mail-poller when SSM fetch fails (research Pattern 6)")
	}
}

// TestUserData_MailPoller_PhraseMatchUsesFixedString verifies that the phrase
// match uses grep -qF (fixed-string) to avoid regex injection from SSM-stored
// phrase values (research Pattern 6, Pitfall 4).
func TestUserData_MailPoller_PhraseMatchUsesFixedString(t *testing.T) {
	out := renderUD57(t)
	if !strings.Contains(out, "grep -qF") {
		t.Error("expected grep -qF (fixed-string) for safe-phrase match in km-mail-poller; do not use grep -qP/-qE (regex injection risk from SSM-stored values)")
	}
}
