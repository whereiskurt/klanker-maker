# Phase 57: Email Enhancement — Research

**Researched:** 2026-04-27
**Domain:** Bash script MIME handling, Ed25519 signing, SES receipt rules, Claude Code marketplace plugin docs
**Confidence:** HIGH

## Summary

Phase 57 adds four related capabilities to the existing email infrastructure established in Phase 45 (km-send/km-recv bash scripts) and Phase 46 (AI email-to-command / safe phrase). The changes are surgical bash script edits and documentation updates — no new Lambda functions, no new Terraform modules, no Go changes required for the core work.

**km-send** currently unconditionally fetches the Ed25519 private key from SSM and embeds `X-KM-Sender-ID` and `X-KM-Signature` headers on every message. A `--no-sign` flag must gate the entire signing block: when set, no SSM fetch, no signature, no X-KM-* headers. The From header stays as `$KM_EMAIL_ADDRESS` (sandbox still sends as itself), but the message is a plain RFC 5322 email suitable for external recipients (Gmail, etc.) that would reject unknown headers or require standard DKIM.

**km-recv** has two known parsing bugs: (1) its `parse_headers()` function reads raw lines with `grep -i "^Header:"` which fails on RFC 5322 folded headers (multi-line values where continuation lines start with whitespace); (2) its `extract_body()` function looks for `text/plain` parts but Gmail HTML emails arrive as `multipart/alternative` with both `text/html` and `text/plain` parts — the current code finds `text/plain` correctly inside `multipart/mixed`, but Gmail's `multipart/alternative` nesting (which can be inside `multipart/mixed`) may not be detected. A `--from-external` display hint is needed to visually flag unsigned messages from external senders.

**Safe phrase validation on inbound** is about enforcing that external emails arriving in a sandbox's mailbox must contain the operator's safe phrase (`KM-AUTH: <phrase>`). The validation point is the km-mail-poller bash script, NOT a new SES receipt Lambda. The SES infrastructure routes all `@sandboxes.{domain}` email to S3 unconditionally (the `sandbox-inbound` rule, no Lambda trigger). The km-mail-poller already has sender allowlist enforcement; safe phrase validation is a natural addition at the same filter step.

**Marketplace plugin docs** live in `skills/email/SKILL.md`, `skills/sandbox/SKILL.md`, `skills/operator/SKILL.md`, and `skills/user/SKILL.md`. These need to document external email workflow, the `--no-sign` flag, and the safe phrase requirement for inbound external email.

**Primary recommendation:** Four separate plan files: (1) km-send --no-sign flag, (2) km-recv RFC 5322 + multipart/alternative + --from-external, (3) km-mail-poller safe phrase validation for inbound, (4) skills/plugin doc updates. All changes are in `pkg/compiler/userdata.go` (bash scripts embedded in Go template) plus `skills/` markdown files.

## Standard Stack

### Core
| Component | Location | Purpose | Notes |
|-----------|----------|---------|-------|
| km-send bash script | `pkg/compiler/userdata.go` lines 688–920 (embedded heredoc) | Send signed or unsigned email from sandbox | Embedded in Go template; deployed to `/opt/km/bin/km-send` at boot |
| km-recv bash script | `pkg/compiler/userdata.go` lines 941–1353 (embedded heredoc) | Read and display inbox email | Deployed to `/opt/km/bin/km-recv` at boot |
| km-mail-poller bash script | `pkg/compiler/userdata.go` lines 606–684 (embedded heredoc) | S3 to local Maildir sync | Runs as systemd service; polls every 60s |
| `pkg/aws/mailbox.go` | Go library | ParseSignedMessage, MatchesAllowList, KMAuthPattern | Used by operator-side km email read — not by bash scripts |
| `cmd/email-create-handler/main.go` | Lambda | Handles operator@ inbox | Has safe phrase validation; NO role in sandbox inbound |
| `skills/email/SKILL.md` | Claude Code plugin skill | Email orchestration patterns | marketplace plugin |
| `skills/sandbox/SKILL.md` | Claude Code plugin skill | Sandbox environment detection | marketplace plugin |
| `skills/operator/SKILL.md` | Claude Code plugin skill | Operator communication via email | marketplace plugin |

### Tools Available in Sandbox (Amazon Linux 2023)
| Tool | Version | Notes |
|------|---------|-------|
| bash | 5.x | Pure bash; heredocs, arrays, `set -euo pipefail` |
| openssl | 3.x | `pkeyutl -sign`, `pkeyutl -verify`, `-rawin -in <file>` (not stdin — Phase 45 fix) |
| aws CLI | 2.x | `sesv2 send-email`, `ssm get-parameter`, `s3 cp` |
| python3 | 3.11 | Available on AL2023; can be used for MIME parsing if bash is insufficient |
| grep, sed, awk | GNU | Standard; `grep -oP` (Perl regex) available |
| base64, xxd, od | GNU | Used for key encoding in existing scripts |
| jq | 1.6 | Not installed by default — current scripts avoid it |

**CRITICAL:** python3 is available on AL2023 and can parse MIME with `email.parser` stdlib. This is a viable alternative for RFC 5322 folded header parsing if pure bash proves fragile. The current scripts are pure bash + openssl + aws CLI — maintain that pattern unless clearly inadequate.

## Architecture Patterns

### Pattern 1: Bash Script Deployment via Go Heredoc
**What:** All bash scripts (km-send, km-recv, km-mail-poller) are embedded in `userdata.go` as Go template heredocs. The template is rendered at sandbox creation time and runs as cloud-init user-data.
**Location:** `pkg/compiler/userdata.go` — the scripts are wrapped in `cat > /opt/km/bin/km-send << 'KMSEND'` ... `KMSEND` blocks.
**Key constraint:** Changes to these scripts only take effect on newly provisioned sandboxes. Existing sandboxes need manual patching (or reprovisioning).
**Test pattern:** Existing tests assert string presence in rendered userdata via `strings.Contains(out, ...)` — see `TestKmSendPresentWhenEmailSet`, `TestKmRecvContainsDynamoDBLookup`, etc.

### Pattern 2: km-send `--no-sign` Flag Design
**What must change:** The signing block (lines 773–884 of userdata.go) is currently unconditional. It must be gated on `NO_SIGN=false` with `--no-sign` setting `NO_SIGN=true`.

**When `--no-sign` is set:**
- Skip SSM fetch of `/sandbox/$KM_SANDBOX_ID/signing-key`
- Skip `ed25519_privkey_to_pem()`
- Skip `openssl pkeyutl -sign`
- `build_mime` must accept an empty signature arg and omit `X-KM-Sender-ID` and `X-KM-Signature` headers
- From header: keep as-is (`$KM_EMAIL_ADDRESS`) — the sandbox still sends from its own address
- KM-AUTH phrase auto-append (for operator inbox): KEEP this even in `--no-sign` mode — KM-AUTH is needed for operator authentication and is not a signing artifact

**When `--no-sign` is NOT set (default):** Behavior unchanged.

**Usage pattern for external email:**
```bash
km-send --no-sign --to user@gmail.com --subject "hello" --body /tmp/msg.txt
```

### Pattern 3: RFC 5322 Folded Header Unfolding in bash
**What is RFC 5322 header folding:** A header value that is too long may be split across multiple lines where continuation lines begin with a space or tab. Example:
```
Subject: This is a very long subject line that
 gets wrapped here
X-Custom-Header: first part
\tsecond part after tab
```

**Current bug:** `parse_headers()` in km-recv uses `grep -i "^Header:"` which only matches the first line of a folded header. The continuation line (starting with whitespace) is missed or returned as part of the next header.

**Fix approach (pure bash):**
```bash
unfold_headers() {
  # Join continuation lines (lines starting with space/tab) to previous line
  awk '/^[ \t]/ { printf "%s", $0; next } { if (NR>1) print ""; printf "%s", $0 } END { print "" }'
}
```
Apply `unfold_headers` to the first 8KB of the raw email before running `parse_headers`. This is a single-pass awk command that is reliable and fast.

**Alternative using python3:**
```bash
python3 -c "
import email, sys
raw = sys.stdin.read(8192)
msg = email.message_from_string(raw)
print(msg.get('Subject',''))
"
```
Python's `email.parser` handles RFC 5322 folding natively. However, this adds a python3 subprocess per header extraction — acceptable for occasional email reads but adds latency.

**Recommendation:** Use the awk unfold approach — it is a single additional pipeline step before the existing grep-based parsing. Keep python3 as a fallback for future complexity.

### Pattern 4: `multipart/alternative` Body Extraction in bash
**What is multipart/alternative:** Gmail and modern email clients send HTML emails as `multipart/alternative` with two parts: `text/plain` (fallback) and `text/html` (rich). The entire `multipart/alternative` block may itself be wrapped in `multipart/mixed` (when there are also attachments).

**Current km-recv behavior:** `extract_body()` looks for `Content-Type: text/plain` parts. It works for km-send generated emails (which use `text/plain` or `multipart/mixed` with `text/plain` first part). It fails to extract the text/plain body from Gmail's `multipart/alternative` wrapper because:
1. The outer Content-Type is `multipart/alternative` not `multipart/mixed`
2. The current code only looks for a boundary in `$HDR_CONTENT_TYPE` (the top-level content type)
3. When `multipart/alternative` is nested inside `multipart/mixed`, the code needs recursive part inspection

**Fix approach:** The `extract_body()` function must:
1. Handle `multipart/alternative` as a recognized top-level multipart type (alongside `multipart/mixed`)
2. Within any multipart block, prefer `text/plain` over `text/html`
3. Fall back to extracting `text/html` and stripping tags if no `text/plain` is found

**HTML tag stripping in pure bash:**
```bash
strip_html() {
  sed 's/<[^>]*>//g' | sed 's/&nbsp;/ /g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g'
}
```

**Alternatively with python3:**
```bash
python3 -c "
import sys
from html.parser import HTMLParser

class Stripper(HTMLParser):
    def __init__(self):
        super().__init__()
        self.text = []
    def handle_data(self, d):
        self.text.append(d)
    def get_data(self):
        return ''.join(self.text)

s = Stripper()
s.feed(sys.stdin.read())
print(s.get_data())
"
```

**Recommendation:** Use pure bash sed for tag stripping (sufficient for display in km-recv), not full HTML parsing.

### Pattern 5: `--from-external` Display Hint
**What:** When km-recv displays a message from an external sender (no `X-KM-Sender-ID` header, `SIG_STATUS="unsigned"`), it should show a visual indicator like `[EXTERNAL]` or similar in the human-readable output and an `"external": true` field in JSON output.

**How to detect:** If `HDR_SENDER_ID` is empty AND `SIG_STATUS="unsigned"`, the message is external.

**Current output format:**
```
[1] From: sb-a1b2c3d4      Subject: hello         Sig: — unsigned   Enc: no
```

**Proposed with external flag:**
```
[1] From: user@gmail.com [EXTERNAL]  Subject: hello     Sig: — unsigned  Enc: no
```

**JSON field addition:** Add `"external": true` to the JSON output object when no `X-KM-Sender-ID` header is present.

### Pattern 6: Safe Phrase Validation in km-mail-poller
**The delivery path:** External email → SES receipt rule → S3 `mail/` prefix → km-mail-poller polls and copies to `/var/mail/km/new/`.

**No Lambda at the sandbox inbound path.** The `sandbox-inbound` SES receipt rule is a plain S3 action with no Lambda trigger. Adding a new Lambda would require Terraform changes, IAM roles, and SES rule ordering changes — all out of scope for Phase 57.

**The right insertion point is km-mail-poller.** It already performs:
1. To-header matching (is this for my sandbox?)
2. Sender allowlist enforcement (KM_ALLOWED_SENDERS)

Safe phrase validation should happen AFTER these checks, as a third gate:
- If the sender has `X-KM-Sender-ID` (it's a sandbox sender) → skip safe phrase check
- If the sender lacks `X-KM-Sender-ID` (it's external) → check body for `KM-AUTH: <phrase>`
- The safe phrase value comes from SSM: `aws ssm get-parameter --name "/km/config/remote-create/safe-phrase" --with-decryption --query 'Parameter.Value' --output text`
- Cache it in a local variable for the duration of the poller run to avoid repeated SSM calls

**What to do when validation fails:**
- Silently drop (move to `$MAIL_DIR/skipped/`) — same behavior as the existing allowlist rejection
- Log to stderr: `[km-mail-poller] External email from $sender_email rejected: missing/invalid safe phrase`
- Do NOT bounce (km-mail-poller has no SES send capability; bouncing would require km-send logic)

**env var for the safe phrase:** It is currently NOT injected as a sandbox environment variable (unlike KM_OPERATOR_EMAIL, KM_ARTIFACTS_BUCKET, etc.). Two options:
1. Fetch from SSM at poller startup (and cache) — consistent with km-send's pattern
2. Pass via systemd Environment= directive baked in at compile time — simpler but exposes the phrase in process environment

**Recommendation:** Fetch from SSM at poller startup (same SSM path: `/km/config/remote-create/safe-phrase`) — consistent with km-send's existing pattern. Cache in a bash variable for the loop iteration.

### Pattern 7: Marketplace Plugin Doc Updates
**Location:** `skills/email/SKILL.md`, `skills/sandbox/SKILL.md`, `skills/operator/SKILL.md`
**Plugin manifests:** `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`

**skills/email/SKILL.md needs:**
- New "Sending to External Recipients" section documenting `--no-sign` flag
- Updated "Signing Behavior" section noting `--no-sign` skips SSM fetch and X-KM-* headers
- Update "Receiving Email" section noting `"external": true` field in JSON output
- Note that external inbound email must contain KM-AUTH phrase to pass km-mail-poller filter

**skills/sandbox/SKILL.md needs:**
- Step 3 "Verify Tooling" already covers `/opt/km/bin/km-send` and `/opt/km/bin/km-recv` — no change needed to paths
- Add note: `--no-sign` flag is available when sending to non-sandbox recipients

**skills/operator/SKILL.md needs:**
- No changes needed (operator communication is always signed to operator inbox)

**The plugin.json and marketplace.json do NOT reference skill files directly** — they only have name/description/version metadata. The skills are loaded separately by Claude Code from the `skills/` directory. No changes needed to these JSON manifests.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| RFC 5322 header unfolding | Custom bash state machine | `awk` one-liner or `python3 -c "import email..."` | RFC 5322 folding edge cases (tabs, multiple folds, encoded-word in subject) are subtle |
| HTML to plaintext | Full HTML parser in bash | `sed 's/<[^>]*>//g'` for basic stripping, or python3 HTMLParser | Good enough for display; full DOM parsing is overkill |
| MIME multipart parser | Recursive bash parser | `python3 email.message_from_string()` if needed | MIME boundary parsing with nested multiparts is error-prone in pure bash |
| Safe phrase storage | New DynamoDB attribute | Existing SSM path `/km/config/remote-create/safe-phrase` | Already used by km-send and email-create-handler Lambda |
| New SES receipt Lambda for sandbox validation | New Lambda + Terraform | km-mail-poller filter (bash, already deployed) | Lambda would require Terraform changes, new IAM roles, SES rule reordering |

## Common Pitfalls

### Pitfall 1: Heredoc-within-heredoc quoting
**What goes wrong:** The bash scripts are embedded in a Go template as `cat > /opt/km/bin/km-send << 'KMSEND'` ... `KMSEND`. A `--no-sign` branch that introduces a new heredoc inside the script will conflict with the outer KMSEND delimiter.
**Why it happens:** Single-quoted heredoc delimiters (`<< 'KMSEND'`) don't need to be unique within the content, but any line that matches `KMSEND` exactly would terminate the outer block prematurely.
**How to avoid:** Use different inner heredoc labels (e.g., `<< 'NOSIGN_HELP'`), or use process substitution, or use `printf` instead of heredocs for multi-line strings inside the script. The existing scripts already avoid inner heredocs — keep this convention.

### Pitfall 2: `set -euo pipefail` propagation in `--no-sign` mode
**What goes wrong:** km-send has `set -euo pipefail`. If `--no-sign` skips the SSM fetch but the code path still tries to use `$PEM_FILE` or `$SIGNATURE` variables, the script will fail on undefined variable with `set -u`.
**How to avoid:** Initialize `PEM_FILE=""` and `SIGNATURE=""` at the top. In `build_mime()`, check `[[ -n "$signature" ]]` before emitting the `X-KM-Signature` header.

### Pitfall 3: RFC 5322 unfolding must happen before KM header parsing
**What goes wrong:** If `unfold_headers` is applied after `parse_headers`, the X-KM-Signature header (which is a long base64 string, likely to be folded by SES) will be truncated.
**How to avoid:** Apply `unfold_headers` to the raw email variable BEFORE calling `parse_headers`. The existing code reads the raw file into `$raw` — apply unfolding to that variable.

### Pitfall 4: Safe phrase caching across poller iterations
**What goes wrong:** SSM GetParameter has a cost and latency. Fetching it on every file in every poll iteration (with potentially many files) creates unnecessary API calls.
**How to avoid:** Fetch the safe phrase once at startup (or once per outer loop iteration). Cache in `KM_SAFE_PHRASE_CACHED` local variable. If SSM fetch fails, log a warning and skip external validation (fail-open for this specific check).

### Pitfall 5: km-recv `--from-external` vs `--from-external` display-only mode
**What goes wrong:** The phase description says "from-external display hint". If implemented as a new CLI flag (`--from-external`), users would need to pass it manually. If implemented as automatic detection (no flag needed), it's always correct.
**How to avoid:** Implement as automatic detection based on `X-KM-Sender-ID` absence — no flag needed. The `--from-external` language in the phase description refers to the display behavior, not a new flag that users must pass. Confirm with the planner, but the automatic detection approach is simpler and more useful.

### Pitfall 6: multipart/alternative nested inside multipart/mixed
**What goes wrong:** Gmail sends: `Content-Type: multipart/mixed` → part 1: `Content-Type: multipart/alternative` → alternative A: `text/plain`, alternative B: `text/html`. The current `extract_body()` only looks one level deep.
**How to avoid:** When a part inside `multipart/mixed` is itself `multipart/alternative` (or `multipart/related`), recurse into it to find the `text/plain` part. In bash, this means a second pass of boundary extraction on the nested part.

## Code Examples

Verified patterns from current codebase:

### Current km-send argument parsing loop (reference for adding --no-sign)
```bash
# Source: pkg/compiler/userdata.go lines 709-722
while [[ $# -gt 0 ]]; do
  case "$1" in
    --to)        TO="$2";        shift 2 ;;
    --subject)   SUBJECT="$2";   shift 2 ;;
    --body)      BODY_FILE="$2"; shift 2 ;;
    --attach)    ATTACH_CSV="$2"; shift 2 ;;
    --cc)        CC_CSV="$2";    shift 2 ;;
    --use-bcc)   USE_BCC=true;   shift ;;
    --reply-to)  REPLY_TO="$2";  shift 2 ;;
    --)          shift; break ;;
    -*)          echo "[km-send] Unknown option: $1" >&2; usage ;;
    *)           break ;;
  esac
done
```
Add `--no-sign) NO_SIGN=true; shift ;;` to this block.

### Current build_mime() header emission (reference for conditional X-KM-* headers)
```bash
# Source: pkg/compiler/userdata.go lines 817-828
emit_headers() {
    printf 'From: %s\r\n' "$from"
    printf 'To: %s\r\n' "$to"
    [[ -n "$CC_CSV" ]] && printf 'Cc: %s\r\n' "$CC_CSV"
    [[ -n "$REPLY_TO" ]] && printf 'Reply-To: %s\r\n' "$REPLY_TO"
    printf 'Subject: %s\r\n' "$subject"
    printf 'Date: %s\r\n' "$date_str"
    printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"
    printf 'X-KM-Signature: %s\r\n' "$signature"
    printf 'MIME-Version: 1.0\r\n'
}
```
Change to: `[[ -n "$sender_id" ]] && printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"` and `[[ -n "$signature" ]] && printf 'X-KM-Signature: %s\r\n' "$signature"`.

### Current km-recv parse_headers() (reference for RFC 5322 fix)
```bash
# Source: pkg/compiler/userdata.go lines 1026-1036
parse_headers() {
  local raw="$1"
  HDR_FROM=$(echo "$raw"         | grep -i "^From:"              | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  HDR_SUBJECT=$(echo "$raw"      | grep -i "^Subject:"           | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  HDR_SENDER_ID=$(echo "$raw"    | grep -i "^X-KM-Sender-ID:"    | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  HDR_SIGNATURE=$(echo "$raw"    | grep -i "^X-KM-Signature:"    | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  # ...
}
```
Fix: apply RFC 5322 unfolding before this function.

### RFC 5322 unfold awk one-liner (verified pattern)
```bash
# Unfold RFC 5322 folded headers: join continuation lines (starting with space/tab)
# to the preceding header line.
unfold_headers() {
  awk 'BEGIN{line=""} /^[ \t]/{sub(/^[ \t]+/, " "); line = line $0; next} {if(line!="") print line; line=$0} END{if(line!="") print line}'
}
# Usage:
raw_unfolded=$(printf '%s' "$raw" | head -c 8192 | unfold_headers)
# Then call parse_headers "$raw_unfolded" ... "$raw" (for body extraction, use original)
```

### km-mail-poller safe phrase check (proposed addition)
```bash
# After sender allowlist check, before moving to new/:
# External email safe phrase validation
if [[ -z "$sender_id" ]]; then
  # External sender — require KM-AUTH phrase
  if [[ -n "${KM_SAFE_PHRASE_CACHED:-}" ]]; then
    body_check=$(head -c 4096 "$local_file" || true)
    if ! echo "$body_check" | grep -qP "KM-AUTH:\s*${KM_SAFE_PHRASE_CACHED}"; then
      echo "[km-mail-poller] External email from $sender_email missing/invalid safe phrase, skipping $key" >&2
      rm -f "$local_file"
      mkdir -p "$MAIL_DIR/skipped"
      touch "$MAIL_DIR/skipped/$key"
      continue
    fi
  fi
fi
```

### MatchesAllowList in Go (reference — not changing, but planner should know the pattern)
```go
// Source: pkg/aws/identity.go line 382
// Email patterns ("*@domain.com"), sandbox IDs, "self", "*" — used by ParseSignedMessage
func MatchesAllowList(patterns []string, senderID, senderAlias, receiverSandboxID, senderEmail string) bool
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| km-send always signs | --no-sign flag skips signing | Phase 57 | Enables external (Gmail) recipients |
| km-recv only parses straight headers | km-recv unfolds RFC 5322 folded headers | Phase 57 | Gmail / Outlook headers become parseable |
| km-recv only handles multipart/mixed | km-recv handles multipart/alternative too | Phase 57 | Gmail HTML emails become readable |
| No external sender indicator | `[EXTERNAL]` display flag + `"external":true` JSON | Phase 57 | Agents know to treat unsigned email as untrusted |
| km-mail-poller drops based on allowlist only | km-mail-poller also enforces safe phrase for external | Phase 57 | External email requires operator authorization |
| Plugin docs omit external email | Plugin docs document --no-sign and safe phrase | Phase 57 | Agents know when and how to send to external recipients |

**SSM path for safe phrase (confirmed from codebase):**
`/km/config/remote-create/safe-phrase`
Used in:
- `cmd/email-create-handler/main.go` (Lambda, operator inbox)
- `pkg/compiler/userdata.go` km-send (auto-appends to operator-bound emails)
- `internal/app/config/config.go` (`SafePhrase` field from `safe_phrase` in km-config.yaml)
- `internal/app/cmd/init.go` (writes to SSM during `km init`)

## Open Questions

1. **`--from-external` as flag vs automatic detection**
   - What we know: Phase description says "from-external display hint" — ambiguous on whether it's a flag
   - What's unclear: Is the intent a manual flag or automatic detection based on X-KM-Sender-ID absence?
   - Recommendation: Implement as automatic detection (no flag needed). External = no X-KM-Sender-ID header. This is unambiguous and requires no user action.

2. **km-mail-poller safe phrase check: fail-open or fail-closed when SSM is unavailable?**
   - What we know: km-mail-poller runs as a systemd service; SSM may occasionally be unreachable
   - What's unclear: Should external emails be delivered if SSM can't be reached to fetch the safe phrase?
   - Recommendation: Fail-open (if SSM fetch fails, skip safe phrase check for external email). Log the failure. This matches the existing allowlist behavior (`fail-open` on errors).

3. **safe phrase in km-recv: should km-recv also enforce safe phrase?**
   - What we know: Phase description only mentions km-mail-poller. km-recv has a belt-and-suspenders sender allowlist check that mirrors km-mail-poller's.
   - What's unclear: Should km-recv also check safe phrase (belt-and-suspenders)?
   - Recommendation: No — km-mail-poller is the right place. km-recv operates on already-filtered files in `/var/mail/km/new/`. Adding the check in km-recv as well would be redundant but not harmful; phase scope says poller.

4. **multipart/alternative handling: pure bash or python3?**
   - What we know: python3 is available on AL2023. Current scripts are pure bash.
   - What's unclear: How complex will the multipart/alternative + nested structure become?
   - Recommendation: Start with enhanced pure bash (awk unfold + recursive boundary extraction). Use `python3 -c "import email..."` as a fallback inline if the bash approach hits edge cases. Don't add a python3 subprocess dependency to the main path.

## Validation Architecture

> nyquist_validation is `true` in `.planning/config.json` — section required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib `testing` package) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... -run TestKmSend -v` |
| Full suite command | `go test ./pkg/compiler/... -v` |

### Phase Requirements → Test Map
| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|--------------|
| km-send emits `--no-sign` in rendered userdata | unit | `go test ./pkg/compiler/... -run TestKmSendNoSign -v` | Wave 0 |
| km-send with `--no-sign` omits X-KM-Sender-ID header pattern | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignOmitsXKMHeaders -v` | Wave 0 |
| km-send with `--no-sign` omits SSM fetch pattern | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignSkipsSSMFetch -v` | Wave 0 |
| km-recv contains RFC 5322 unfold logic | unit | `go test ./pkg/compiler/... -run TestKmRecvUnfoldHeaders -v` | Wave 0 |
| km-recv handles multipart/alternative content type | unit | `go test ./pkg/compiler/... -run TestKmRecvMultipartAlternative -v` | Wave 0 |
| km-recv JSON output includes `external` field pattern | unit | `go test ./pkg/compiler/... -run TestKmRecvExternalField -v` | Wave 0 |
| km-mail-poller contains safe phrase validation logic | unit | `go test ./pkg/compiler/... -run TestKmMailPollerSafePhrase -v` | Wave 0 |
| skills/email/SKILL.md documents --no-sign flag | manual | review SKILL.md content | ❌ Wave 0 |
| skills/email/SKILL.md documents external field | manual | review SKILL.md content | ❌ Wave 0 |

All test assertions follow the existing `strings.Contains(out, expectedPattern)` pattern in `pkg/compiler/userdata_test.go`. No new test file is needed — add functions to the existing file.

### Test Patterns (existing, to follow)
```go
// Source: pkg/compiler/userdata_test.go — TestKmSendContainsSSMFetch pattern
func TestKmSendNoSignFlag(t *testing.T) {
    out := renderWithEmail(t)
    if !strings.Contains(out, "--no-sign") {
        t.Error("expected --no-sign flag handling in km-send")
    }
}
```

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... -run TestKmSend -v && go test ./pkg/compiler/... -run TestKmRecv -v`
- **Per wave merge:** `go test ./pkg/compiler/... -v && go test ./cmd/email-create-handler/... -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmSendNoSign*` functions (3 assertions)
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmRecvUnfoldHeaders`, `TestKmRecvMultipartAlternative`, `TestKmRecvExternalField`
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmMailPollerSafePhrase`

No new test files needed — all additions go in the existing `userdata_test.go`.

## Sources

### Primary (HIGH confidence)
- `pkg/compiler/userdata.go` — full km-send, km-recv, km-mail-poller source (directly read)
- `cmd/email-create-handler/main.go` — safe phrase Lambda implementation (directly read)
- `pkg/aws/mailbox.go` — KMAuthPattern, MatchesAllowList, ParseSignedMessage (directly read)
- `infra/modules/ses/v1.0.0/main.tf` — SES receipt rule structure (directly read)
- `pkg/compiler/userdata_test.go` — existing test patterns (directly read)
- `skills/email/SKILL.md` — current plugin email skill (directly read)
- `skills/sandbox/SKILL.md` — current plugin sandbox skill (directly read)
- `.planning/ROADMAP.md` Phase 57 description (directly read)

### Secondary (MEDIUM confidence)
- RFC 5322 folded header behavior: well-established standard; awk unfolding pattern is a widely-used approach
- AL2023 python3 availability: Amazon Linux 2023 ships with python3 by default (confirmed by AL2023 documentation patterns)
- Gmail multipart/alternative structure: standard behavior for all Gmail HTML emails

### Tertiary (LOW confidence)
- None — all critical claims verified from codebase source

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — verified from direct codebase inspection
- Architecture: HIGH — all patterns derived from existing code
- Pitfalls: HIGH — derived from observed code structure and known bash/MIME edge cases
- Test patterns: HIGH — directly mirrors existing test file patterns

**Research date:** 2026-04-27
**Valid until:** 2026-05-27 (stable codebase; scripts only change when Phase 57 is implemented)
