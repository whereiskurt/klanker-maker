# Phase 57: Email Enhancement — Research

**Researched:** 2026-04-27 (full re-verification)
**Domain:** Bash script MIME handling, Ed25519 signing, SES receipt rules, Claude Code marketplace plugin docs
**Confidence:** HIGH — all findings verified directly from current source

## Summary

Phase 57 adds four related capabilities to email infrastructure established in Phase 45 (km-send/km-recv bash scripts) and Phase 46 (AI email-to-command / safe phrase). The changes are surgical bash script edits plus documentation updates — no new Lambda functions, no new Terraform modules, no Go struct changes required.

**Scripts confirmed location (re-verified 2026-04-27):** All three bash scripts are embedded as heredocs in `pkg/compiler/userdata.go`. km-mail-poller lives at lines 606–684, km-send at lines 688–919, km-recv at lines 941–1353. They are deployed to `/opt/km/bin/` by cloud-init user-data at sandbox boot.

**km-send** currently unconditionally fetches the Ed25519 private key from SSM (line 873) and signs (line 884) before building the MIME message. The signing block is NOT inside a conditional. The `emit_headers()` helper (lines 818–828) always emits `X-KM-Sender-ID` and `X-KM-Signature` headers unconditionally. A `--no-sign` flag must gate the entire signing block: when set, skip SSM fetch, skip openssl sign, and `emit_headers` must conditionally omit both X-KM-* headers.

**km-recv** has confirmed parsing bugs: (1) `parse_headers()` at lines 1026–1036 uses `grep -i "^Header:"` which misses RFC 5322 folded continuation lines; (2) `extract_body()` at lines 1043–1096 handles `multipart/mixed` by scanning for `text/plain` parts one level deep, but Gmail emails arrive as `multipart/alternative` (or `multipart/mixed` wrapping `multipart/alternative`) — the current code reads `HDR_CONTENT_TYPE` from the outer envelope and only extracts the boundary from the top-level Content-Type, so a Gmail `multipart/alternative` inner envelope never has its `text/plain` extracted. The `--from-external` display hint refers to automatic detection (not a CLI flag), based on absence of `X-KM-Sender-ID` header.

**Safe phrase validation on inbound** belongs in km-mail-poller, NOT a new SES Lambda. The `sandbox_inbound` SES receipt rule (verified in `infra/modules/ses/v1.0.0/main.tf` line 126) is a plain S3 action with no Lambda trigger. km-mail-poller already has sender allowlist enforcement (lines 641–668) — safe phrase validation is a third gate added after the allowlist check.

**Skills docs location (re-verified):** `skills/email/SKILL.md`, `skills/sandbox/SKILL.md`, `skills/operator/SKILL.md`, `skills/user/SKILL.md` all live at repo root level `skills/` (not under `.claude/skills/`). The `.claude/skills/` directory does not exist. Skills are loaded directly from `skills/` by Claude Code.

**Primary recommendation:** Four plan files plus Wave 0: (57-00) RED test stubs + MIME fixtures, (57-01) km-send `--no-sign`, (57-02) km-recv RFC 5322 + multipart/alternative + [EXTERNAL] hint, (57-03) km-mail-poller safe phrase gate, (57-04) skills doc updates. All code changes are in `pkg/compiler/userdata.go` plus `skills/` markdown files.

## Standard Stack

### Core (verified from source)
| Component | Location | Lines (current) | Purpose |
|-----------|----------|-----------------|---------|
| km-mail-poller bash script | `pkg/compiler/userdata.go` | 606–684 | S3-to-Maildir sync with allowlist enforcement |
| km-send bash script | `pkg/compiler/userdata.go` | 688–919 | Ed25519-signed email send via sesv2 |
| km-recv bash script | `pkg/compiler/userdata.go` | 941–1353 | Read and verify inbox email |
| `skills/email/SKILL.md` | `skills/email/SKILL.md` | full file | Claude Code marketplace skill — email patterns |
| `skills/sandbox/SKILL.md` | `skills/sandbox/SKILL.md` | full file | Claude Code marketplace skill — env detection |
| `skills/operator/SKILL.md` | `skills/operator/SKILL.md` | full file | Claude Code marketplace skill — operator comms |
| `skills/user/SKILL.md` | `skills/user/SKILL.md` | full file | Claude Code marketplace skill — operator CLI guide |
| `pkg/compiler/userdata_test.go` | same | 1247–1422 | Existing km-send/km-recv tests to extend |

### Tools Available in Sandbox (Amazon Linux 2023 — confirmed in existing scripts)
| Tool | Notes |
|------|-------|
| bash 5.x | `set -euo pipefail`; arrays; heredocs |
| openssl 3.x | `pkeyutl -sign -rawin -in <file>` (Phase 45 fix already applied) |
| aws CLI 2.x | `sesv2 send-email`, `ssm get-parameter`, `s3 cp`, `dynamodb get-item` |
| python3 3.11 | Available on AL2023; `email.parser` stdlib handles RFC 5322 natively |
| grep, sed, awk | GNU variants; `grep -oP` Perl regex available |
| base64, xxd, od | Used in existing signing/verification functions |
| jq | NOT installed by default — current scripts avoid it; keep this convention |

### SES Architecture (verified from `infra/modules/ses/v1.0.0/main.tf`)
| Rule | Lines | Action | Lambda? |
|------|-------|--------|---------|
| `create_inbound` | 74–89 | S3 action | Optional Lambda if `email_create_handler_arn != ""` |
| `sandbox_inbound` | 126–141 | S3 action only | **No Lambda trigger** |

The `sandbox_inbound` rule runs AFTER `create_inbound` (`after = ... create_inbound[0].name`). All `@sandboxes.{domain}` email goes to S3 with no Lambda intercepting it before delivery.

## Architecture Patterns

### Pattern 1: Bash Script Deployment via Go Heredoc

All bash scripts are embedded in `pkg/compiler/userdata.go` inside `cat > /path << 'DELIMITER'` ... `DELIMITER` blocks. The Go template is rendered at sandbox creation time and runs as EC2 cloud-init user-data. Scripts are deployed to `/opt/km/bin/` at boot.

**Key constraint:** Changes only take effect on newly provisioned sandboxes. The `km-mail-poller.service` systemd unit definition at lines 923–937 passes environment variables as `Environment=` directives; the service itself is at line 932 `ExecStart=/opt/km/bin/km-mail-poller`.

**Test pattern (existing):** `strings.Contains(out, <pattern>)` on rendered userdata — see `pkg/compiler/userdata_test.go` lines 1247–1422. All new tests follow this pattern by calling `generateUserData(emailProfile(), ...)` or `parseUserDataTemplate()` + `tmpl.Execute()`.

### Pattern 2: km-send `--no-sign` Flag — Exact Diff

**Variables to initialize at top (before arg parsing):**
```bash
# Add at line ~701, alongside other flag vars:
NO_SIGN=false
```

**Arg parsing addition (after line 717 `--reply-to` case):**
```bash
--no-sign) NO_SIGN=true; shift ;;
```

**Usage line addition (after line 705):**
```bash
echo "       [--no-sign]" >&2
```

**Gate the signing block (currently lines 873–884 are unconditional — wrap them):**
```bash
# Before line 873:
if ! $NO_SIGN; then
  PRIVKEY_B64=$(aws ssm get-parameter \
    --name "/sandbox/$KM_SANDBOX_ID/signing-key" \
    --with-decryption \
    --query 'Parameter.Value' \
    --output text)
  PEM_FILE=$(ed25519_privkey_to_pem "$PRIVKEY_B64")
  SIGNATURE=$(openssl pkeyutl -sign -inkey "$PEM_FILE" -rawin -in "$BODY_TMP" | base64 -w0)
else
  PEM_FILE=""
  SIGNATURE=""
fi
```

**`emit_headers()` conditional header emission (lines 825–826 currently unconditional):**
```bash
# Replace:
printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"
printf 'X-KM-Signature: %s\r\n' "$signature"
# With:
[[ -n "$sender_id" ]] && printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"
[[ -n "$signature" ]] && printf 'X-KM-Signature: %s\r\n' "$signature"
```

**`build_mime()` call site (line 894) — when `--no-sign`, pass empty strings:**
The call already passes `"$SENDER_ID"` and `"$SIGNATURE"`. When `NO_SIGN=true`, `SENDER_ID` should also be cleared so no X-KM-Sender-ID is emitted:
```bash
if $NO_SIGN; then
  SENDER_ID=""
fi
```
Place this block just before the `build_mime` call.

**KM-AUTH behavior:** The operator-inbox safe phrase auto-append (lines 755–771) checks `$TO` against the operator inbox pattern. This block runs before signing and operates on the body file — it should remain active even when `--no-sign` is set. KM-AUTH is an authentication mechanism independent of Ed25519 signing. Keep it unchanged.

**Success message (line 918):** Update to handle unsigned case:
```bash
if $NO_SIGN; then
  echo "[km-send] Sent unsigned email to $TO"
else
  echo "[km-send] Sent signed email to $TO (sig: ${SIGNATURE:0:12}...)"
fi
```

### Pattern 3: RFC 5322 Folded Header Unfolding in km-recv

**What RFC 5322 folding is:** A header value spans multiple lines when continuation lines start with a space or tab. Example — Gmail often folds `Subject:` headers with long encoded-word values.

**Current broken `parse_headers()` at lines 1026–1036:**
```bash
# Uses grep -i "^Subject:" which misses continuation lines starting with whitespace
HDR_SUBJECT=$(echo "$raw" | grep -i "^Subject:" | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
```

**Fix — add `unfold_headers()` function before `parse_headers()`:**
```bash
# RFC 5322 header unfolding: join continuation lines (starting with space/tab)
# to the preceding header line. Apply only to header section.
unfold_headers() {
  awk 'BEGIN{line=""} /^[ \t]/{sub(/^[ \t]+/, " "); line = line $0; next} {if(line!="") print line; line=$0} END{if(line!="") print line}'
}
```

**Usage — in `process_messages()` at line 1238, BEFORE calling `parse_headers`:**
```bash
# Unfold headers only — apply to header section (up to first blank line)
local raw_headers raw_body
raw_headers=$(printf '%s' "$raw" | awk '/^[[:space:]]*$/{exit} {print}' | head -c 8192)
raw_headers_unfolded=$(printf '%s' "$raw_headers" | unfold_headers)
# Re-assemble with original body (body bytes must not be mangled)
local raw_body_only
raw_body_only=$(printf '%s' "$raw" | awk 'found{print} /^[[:space:]]*$/{found=1}')
raw_for_parse="${raw_headers_unfolded}

${raw_body_only}"
parse_headers "$raw_for_parse"
```

**IMPORTANT:** The body extraction must operate on the ORIGINAL `$raw` (not the header-unfolded version) because `unfold_headers` could corrupt base64-encoded attachment bodies if they contain lines starting with a space. Apply unfolding only to parsed header extraction, not to `extract_body`.

### Pattern 4: `multipart/alternative` Body Extraction

**Current `extract_body()` behavior (lines 1043–1096):**
- Reads `$HDR_CONTENT_TYPE` (top-level content type set by `parse_headers`)
- Extracts boundary from top-level content type
- Scans for `Content-Type:.*text/plain` one level deep inside that boundary
- **BUG:** When top-level is `multipart/mixed` and one part is `multipart/alternative`, the current code sees the `multipart/alternative` part header but looks for `text/plain` at the same nesting level. The `text/plain` is inside the `multipart/alternative` boundary, not at the `multipart/mixed` level.

**Gmail email structure:**
```
Content-Type: multipart/mixed; boundary="outer"
--outer
Content-Type: multipart/alternative; boundary="inner"
--inner
Content-Type: text/plain; charset="UTF-8"
[plain text body]
--inner
Content-Type: text/html; charset="UTF-8"
[html body]
--inner--
--outer
Content-Type: application/octet-stream [attachment if any]
--outer--
```

**Fix strategy — two-level extraction in `extract_body()`:**
1. If top-level content type is `multipart/mixed` or `multipart/alternative`:
   a. Look for `text/plain` at the first level (existing behavior — works for km-send generated emails)
   b. If not found, look for a `multipart/alternative` part at the first level
   c. If a `multipart/alternative` part is found, extract its boundary and repeat the scan for `text/plain` inside it
   d. If `text/plain` still not found inside `multipart/alternative`, extract `text/html` and strip tags
2. If top-level is `multipart/alternative` directly:
   a. Scan for `text/plain` first
   b. Fallback to `text/html` with tag stripping

**HTML tag stripping (pure bash — sufficient for display):**
```bash
strip_html() {
  sed 's/<[^>]*>//g' | sed 's/&nbsp;/ /g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g' | tr -s ' \n' | sed '/^[[:space:]]*$/d'
}
```

**The key guard needed in `extract_body()`:** Accept `multipart/alternative` as a recognized container type (not just `multipart/mixed`). The current code checks for a boundary in `$HDR_CONTENT_TYPE` regardless of the specific multipart subtype, so both `multipart/mixed` and `multipart/alternative` at the top level already trigger boundary extraction. The bug is the one-level-deep scan: a `Content-Type: multipart/alternative` part INSIDE `multipart/mixed` contains its own boundary that needs a second pass.

### Pattern 5: [EXTERNAL] Display Hint — Automatic Detection

**Detection signal:** `HDR_SENDER_ID` is empty (no `X-KM-Sender-ID` header) AND `SIG_STATUS="unsigned"` after `verify_signature` returns.

**No new CLI flag needed.** "from-external display hint" in the phase description means the display behavior — automatic detection is simpler and always correct.

**Current human-readable output (lines 1309–1327):**
```bash
from_display="${HDR_SENDER_ID:-${HDR_FROM}}"
printf '[%d] From: %-20s  Subject: %-20s  Sig: %-12s  Enc: %s\n' \
  "$index" "$from_display" ...
```

**Updated `from_display` logic:**
```bash
if [[ -z "$HDR_SENDER_ID" ]] && [[ "$SIG_STATUS" = "unsigned" ]]; then
  from_display="${HDR_FROM} [EXTERNAL]"
else
  from_display="${HDR_SENDER_ID:-${HDR_FROM}}"
fi
```

**JSON output addition (lines 1304–1306):**
```bash
# Add "external" field: true when no X-KM-Sender-ID header
local json_external="false"
[[ -z "$HDR_SENDER_ID" ]] && json_external="true"
# Add to printf format: ,"external":%s
```

The JSON format string at line 1304 becomes:
```bash
printf '{"index":%d,"from":"%s","sender_id":"%s","to":"%s","subject":"%s","signature":"%s","encrypted":%s,"external":%s,"body":"%s","attachments":%s}\n' \
  "$index" "$json_from" "$json_sender_id" "$json_to" "$json_subject" \
  "$json_sig" "$json_enc" "$json_external" "$json_body" "$attach_json"
```

**skills/email/SKILL.md JSON schema documentation (lines 82–95 of the skill):** Currently shows a JSON object without `"external"` field — must add it.

### Pattern 6: km-mail-poller Safe Phrase Gate

**Insertion point:** After the sender allowlist check at line 668 (`if ! $sender_allowed; then`), before `echo "[km-mail-poller] New mail: $key"` at line 670.

**SSM path (confirmed):** `/km/config/remote-create/safe-phrase` — used by km-send (line 763), email-create-handler Lambda (`cmd/email-create-handler/main.go`), and `internal/app/config/config.go`.

**Implementation design:**
1. Fetch the safe phrase ONCE before the outer `while true` loop, cache in `KM_SAFE_PHRASE_CACHED`
2. If SSM fetch fails, set `KM_SAFE_PHRASE_CACHED=""` (fail-open — skip the check)
3. Inside the per-file loop, after allowlist check passes:
   - If `sender_id` is non-empty (it's a sandbox sender) → skip phrase check
   - If `sender_id` is empty AND `KM_SAFE_PHRASE_CACHED` is non-empty → require phrase in body

**Bash code to add before the `while true` loop:**
```bash
# Safe phrase for external inbound validation (fail-open if SSM unreachable)
KM_SAFE_PHRASE_CACHED=""
KM_SAFE_PHRASE_CACHED=$(aws ssm get-parameter \
  --name "/km/config/remote-create/safe-phrase" \
  --with-decryption \
  --query 'Parameter.Value' \
  --output text 2>/dev/null || true)
if [ -n "$KM_SAFE_PHRASE_CACHED" ]; then
  echo "[km-mail-poller] Safe phrase loaded for external email validation"
else
  echo "[km-mail-poller] WARN: safe phrase not available, external email filter disabled" >&2
fi
```

**Gate to insert after allowlist block (between lines 668 and 670), alongside `sender_id` extraction:**
```bash
# External email safe phrase validation
# sender_id is already set above during allowlist check (line 644)
if [ -z "$sender_id" ] && [ -n "${KM_SAFE_PHRASE_CACHED:-}" ]; then
  body_check=$(head -c 4096 "$local_file" 2>/dev/null || true)
  if ! echo "$body_check" | grep -qP "KM-AUTH:\s*${KM_SAFE_PHRASE_CACHED}\b"; then
    echo "[km-mail-poller] External email from ${sender_email:-unknown} rejected: missing/invalid safe phrase, skipping $key" >&2
    rm -f "$local_file"
    mkdir -p "$MAIL_DIR/skipped"
    touch "$MAIL_DIR/skipped/$key"
    continue
  fi
  echo "[km-mail-poller] External email from ${sender_email:-unknown} accepted (safe phrase OK)"
fi
```

**Critical dependency:** The `sender_id` variable at line 644 is only defined when `KM_ALLOWED_SENDERS` is set. If `KM_ALLOWED_SENDERS` is empty (no allowlist configured), the allowlist block is skipped entirely and `sender_id` is never set. The safe phrase check must extract `sender_id` independently of the allowlist block. Restructure to always extract `sender_id` for external check purposes.

**Safer approach — always extract sender_id before either gate:**
```bash
# Always extract sender_id for external validation (even when no allowlist configured)
sender_id=$(head -c 8192 "$local_file" | grep -i "^X-KM-Sender-ID:" | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
sender_email=$(head -c 8192 "$local_file" | grep -i "^From:" | head -1 | \
  grep -oP '[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}' | head -1 | tr '[:upper:]' '[:lower:]')
```

This extraction should happen unconditionally after the To-header match check. The allowlist block already does this (lines 642–644) but only when `KM_ALLOWED_SENDERS` is set. Moving the extraction to be unconditional is the cleanest fix.

**Fail-open vs fail-closed (recommendation):** Fail-open when SSM unavailable — consistent with existing allowlist behavior which passes email through when `KM_ALLOWED_SENDERS` is unset. Log the skipped check.

**Bounce vs silent drop (recommendation):** Silent drop to `$MAIL_DIR/skipped/` — same as current allowlist rejection. km-mail-poller has no SES send capability for bouncing.

### Pattern 7: Skills Documentation Updates

**skills/email/SKILL.md — required changes:**
1. **Send Flags Reference table (lines 44–51):** Add `--no-sign` row:
   `| '--no-sign' | false | Skip Ed25519 signing and X-KM-* headers — use for external (non-sandbox) recipients |`
2. **Signing Behavior section (lines 52–62):** Add paragraph about `--no-sign`:
   - When set, no SSM key fetch, no signature, no X-KM-Sender-ID or X-KM-Signature headers
   - KM-AUTH is still auto-appended when sending to operator (even with --no-sign)
   - Use `--no-sign` only for genuine external (non-sandbox) recipients who cannot receive X-KM-* headers
3. **Add new "Sending to External Recipients" section** after Signing Behavior:
   - Example: `km-send --no-sign --subject "hello" --body /tmp/msg.txt --to user@gmail.com`
   - Note that inbound replies from external addresses must contain `KM-AUTH: <phrase>` to pass the sandbox filter
4. **Receiving Email — JSON schema (lines 82–95):** Add `"external": true/false` field with documentation
5. **Signature Verification table:** Add note that `"— unsigned"` + `"external": true` means the message is from a non-sandbox sender — it passed the safe phrase gate but has no cryptographic verification
6. **Error Handling table:** Add row for "External email not appearing" → safe phrase missing from sender's message

**skills/sandbox/SKILL.md — required changes:**
1. **Step 4: Verify Signing Key Access (line 66):** Add note that when sending to external addresses, use `--no-sign` to skip key fetch (the key is not needed for external email)
2. **Identity Summary (lines 76–83):** Add "External email policy: use `--no-sign` for non-sandbox recipients" note

**skills/operator/SKILL.md — no changes needed.** All operator communication is to the operator inbox, which is always signed. The operator skill does not cover external email scenarios.

**skills/user/SKILL.md — no changes needed.** The user skill covers operator-side `km email send/read` CLI commands, not the in-sandbox bash utilities.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| RFC 5322 header unfolding | Custom bash state machine | `awk` one-liner (see Pattern 3) | RFC 5322 continuation edge cases (tabs, encoded-word in Subject, multiple folds) — the awk approach is a single pipeline step |
| HTML to plaintext | Full HTML parser in bash | `sed 's/<[^>]*>//g'` for basic stripping | Good enough for display in km-recv; full DOM parsing is overkill |
| Nested MIME multipart parser | Recursive bash parser | Two-level boundary scan (plain bash) OR `python3 email.message_from_string()` | Two-level bash is sufficient for Gmail's actual structure; python3 is the escape hatch if edge cases arise |
| Safe phrase storage | New DynamoDB attribute or env var | Existing SSM path `/km/config/remote-create/safe-phrase` | Already used by km-send (line 763) and email-create-handler Lambda — single source of truth |
| New SES receipt Lambda for sandbox inbound | New Lambda + Terraform changes | km-mail-poller filter (bash, already deployed as systemd service) | Lambda would require new Terraform, IAM role, SES rule ordering changes — out of scope; km-mail-poller is already the right filter layer |

## Common Pitfalls

### Pitfall 1: Heredoc-within-heredoc quoting in Go template
**What goes wrong:** The bash scripts are inside `cat > /path << 'DELIMITER'` ... `DELIMITER` blocks. Any new bash code using a heredoc that accidentally introduces the outer delimiter string will terminate the block.
**How to avoid:** Use `printf` instead of heredocs for multi-line strings inside the scripts. The existing scripts already avoid inner heredocs for this reason — maintain that convention throughout Phase 57 changes.

### Pitfall 2: `set -euo pipefail` with undefined variables in `--no-sign` mode
**What goes wrong:** km-send has `set -euo pipefail` (line 693). If `--no-sign` skips SSM fetch but `PEM_FILE` or `SIGNATURE` variables are used later without initialization, the script will exit with "unbound variable" under `set -u`.
**How to avoid:** Initialize `PEM_FILE=""` and `SIGNATURE=""` before the arg-parse loop. In the `else` branch of the `if ! $NO_SIGN` block, explicitly set both to empty string.

### Pitfall 3: RFC 5322 unfolding must not be applied to the body
**What goes wrong:** If `unfold_headers` awk is applied to the entire raw email (headers + body), base64-encoded attachment lines that start with a space (after MIME encoding) would be corrupted.
**How to avoid:** Split the raw email into header section and body section BEFORE applying `unfold_headers`. Apply it only to the header section. Use the original `$raw` for body extraction.

### Pitfall 4: Safe phrase regex with special characters
**What goes wrong:** The safe phrase value from SSM might contain characters that are special in `grep -P` regex (e.g., `.`, `+`, `?`). Using the raw phrase as a regex pattern can cause false positives or false negatives.
**How to avoid:** Use `fgrep -q` (fixed string match) instead of `grep -qP` for the phrase check:
```bash
if ! echo "$body_check" | grep -qF "KM-AUTH: ${KM_SAFE_PHRASE_CACHED}"; then
```
`grep -F` treats the pattern as a fixed string, not a regex.

### Pitfall 5: km-mail-poller `sender_id` scoping when allowlist is disabled
**What goes wrong:** The current code only extracts `sender_id` inside the `if [ -n "${KM_ALLOWED_SENDERS:-}" ]` block (lines 641–668). If no allowlist is configured, `sender_id` is never set and the safe phrase gate cannot distinguish sandbox vs external senders.
**How to avoid:** Move `sender_id` extraction to be unconditional — always extract it right after the To-header match. The safe phrase gate then reads a reliably set variable.

### Pitfall 6: `multipart/alternative` nested inside `multipart/mixed` — boundary variable collision
**What goes wrong:** `extract_body()` uses a local variable named `boundary` for the top-level boundary. The second-level scan needs a different variable name or the outer boundary will be overwritten.
**How to avoid:** Use `boundary2` or `alt_boundary` for the inner `multipart/alternative` boundary. Ensure `in_text_plain`, `past_part_headers`, and `body_lines` are also separately initialized for the inner scan pass.

### Pitfall 7: `[EXTERNAL]` in `from_display` with `printf` format string
**What goes wrong:** `from_display` is passed to `printf '[%d] From: %-20s ...'`. If the value contains `%` characters (e.g., email addresses with percent-encoded parts), printf will interpret them as format directives.
**How to avoid:** The existing code already uses `%s` with the variable as an argument — this is safe as long as the variable is passed as a positional argument, not concatenated into the format string. The existing code does this correctly (line 1320). Maintain this pattern.

## Code Examples

### Current km-send argument parsing loop (verified, lines 709–722)
```bash
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
Add `--no-sign) NO_SIGN=true; shift ;;` after `--reply-to`.

### Current `emit_headers()` (verified, lines 818–828) — both headers are unconditional
```bash
printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"
printf 'X-KM-Signature: %s\r\n' "$signature"
```
Must change to conditional emission based on non-empty value.

### Current `parse_headers()` (verified, lines 1026–1036) — no RFC 5322 unfolding
```bash
parse_headers() {
  local raw="$1"
  HDR_SUBJECT=$(echo "$raw" | grep -i "^Subject:" | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  HDR_SENDER_ID=$(echo "$raw" | grep -i "^X-KM-Sender-ID:" | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  HDR_SIGNATURE=$(echo "$raw" | grep -i "^X-KM-Signature:" | head -1 | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
  # ...
}
```
HDR_SIGNATURE in particular will be truncated by SES if it exceeds ~998 chars (RFC 2822 line length limit). SES does fold long headers.

### RFC 5322 unfold awk (verified pattern, well-established)
```bash
unfold_headers() {
  awk 'BEGIN{line=""} /^[ \t]/{sub(/^[ \t]+/, " "); line = line $0; next} {if(line!="") print line; line=$0} END{if(line!="") print line}'
}
```

### Current `extract_body()` one-level multipart scan (verified, lines 1053–1092)
The existing loop finds `text/plain` inside the top-level boundary. For Gmail, the `multipart/alternative` wrapper is itself a boundary-delimited part, and `text/plain` is INSIDE that boundary. The fix requires a second pass on the `multipart/alternative` part's content.

### Current km-mail-poller allowlist enforcement (verified, lines 641–668)
The `sender_id` variable (line 644) is only set when `KM_ALLOWED_SENDERS` is non-empty. This must be lifted out to unconditional scope for the safe phrase gate.

### Current km-recv JSON output format (verified, lines 1290–1306)
```bash
printf '{"index":%d,"from":"%s","sender_id":"%s","to":"%s","subject":"%s","signature":"%s","encrypted":%s,"body":"%s","attachments":%s}\n' \
  "$index" "$json_from" "$json_sender_id" "$json_to" "$json_subject" \
  "$json_sig" "$json_enc" "$json_body" "$attach_json"
```
Must add `"external":%s` field.

## State of the Art

| Old Approach | Current Approach after Phase 57 | Impact |
|--------------|--------------------------------|--------|
| km-send always signs | km-send `--no-sign` skips signing block entirely | External (Gmail) recipients work |
| km-recv only parses unfolded headers | km-recv unfolds RFC 5322 folded headers before parse | Gmail / Outlook headers become parseable; HDR_SIGNATURE no longer truncated |
| km-recv handles only multipart/mixed top-level | km-recv handles multipart/alternative and nested structure | Gmail HTML emails become readable in km-recv |
| No external sender indicator | `[EXTERNAL]` in human display + `"external":true` in JSON | Agents and humans can see at a glance that email is from external sender |
| km-mail-poller applies allowlist only | km-mail-poller also enforces KM-AUTH safe phrase for external senders | External email requires operator authorization to enter sandbox mailbox |
| Plugin docs omit external email workflow | skills/email/SKILL.md documents --no-sign, external field, safe phrase requirement | Agents know when and how to send to external recipients |

**Confirmed SSM paths:**
- `/km/config/remote-create/safe-phrase` — safe phrase (used by km-send line 763, email-create-handler main.go line 264)
- `/sandbox/$KM_SANDBOX_ID/signing-key` — Ed25519 private key per sandbox (used by km-send line 874)

## Open Questions

1. **km-mail-poller: should the safe phrase check apply when `KM_ALLOWED_SENDERS` is set to `*` (allow all)?**
   - What we know: `*` in the allowlist means accept all senders without checking identity
   - What's unclear: Does `*` bypass safe phrase or should safe phrase still apply?
   - Recommendation: Safe phrase is independent of the allowlist. Even if `*` is set, external (unsigned) email should require the safe phrase. An `*` allowlist means "I trust all identity-verified senders" not "I accept all unauthenticated email without restriction." Apply safe phrase check to unsigned senders regardless of allowlist setting.

2. **km-recv: should the safe phrase check be mirrored in km-recv (belt-and-suspenders)?**
   - What we know: Phase description specifies km-mail-poller as the enforcement point. km-recv has a belt-and-suspenders allowlist check (lines 1241–1268) that mirrors km-mail-poller.
   - Recommendation: Phase 57 scope is km-mail-poller only. km-recv operates on already-filtered `$MAIL_DIR/new/` files. Adding duplicate enforcement in km-recv is a Phase 59+ concern.

3. **Multipart/alternative with base64 Content-Transfer-Encoding on the text/plain part**
   - What we know: km-send always uses `Content-Transfer-Encoding: 8bit` for text/plain. Gmail may use `quoted-printable` or `base64` encoding on some text/plain parts.
   - Recommendation: For Phase 57, handle the common case (8bit text/plain inside multipart/alternative). Add a note in the research that base64-encoded text/plain parts inside `multipart/alternative` are deferred — this is an edge case affecting very few emails.

## Validation Architecture

nyquist_validation is `true` in `.planning/config.json` — section required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` package |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... -run TestKmSend -v` |
| Full suite command | `go test ./pkg/compiler/... -v` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|--------------|
| km-send rendered userdata contains `--no-sign` flag handling | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignFlagPresent -v` | Wave 0 |
| km-send `--no-sign` omits `X-KM-Sender-ID` header pattern | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignOmitsXKMSenderID -v` | Wave 0 |
| km-send `--no-sign` omits `X-KM-Signature` header pattern | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignOmitsXKMSignature -v` | Wave 0 |
| km-send `--no-sign` skips SSM fetch (guards `ssm get-parameter` signing-key with `NO_SIGN`) | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignSkipsSSMSigningKey -v` | Wave 0 |
| km-send `--no-sign` still appends KM-AUTH (body check when sending to operator) | unit | `go test ./pkg/compiler/... -run TestKmSendNoSignKeepsKMAuth -v` | Wave 0 |
| km-recv rendered userdata contains `unfold_headers` awk function | unit | `go test ./pkg/compiler/... -run TestKmRecvUnfoldHeaders -v` | Wave 0 |
| km-recv rendered userdata handles `multipart/alternative` content type | unit | `go test ./pkg/compiler/... -run TestKmRecvMultipartAlternative -v` | Wave 0 |
| km-recv rendered userdata contains `EXTERNAL` display label | unit | `go test ./pkg/compiler/... -run TestKmRecvExternalLabel -v` | Wave 0 |
| km-recv JSON output format contains `external` field | unit | `go test ./pkg/compiler/... -run TestKmRecvExternalJSONField -v` | Wave 0 |
| km-mail-poller rendered userdata contains safe phrase SSM fetch | unit | `go test ./pkg/compiler/... -run TestKmMailPollerSafePhrase -v` | Wave 0 |
| km-mail-poller rendered userdata contains safe phrase gate for external senders | unit | `go test ./pkg/compiler/... -run TestKmMailPollerSafePhraseGate -v` | Wave 0 |
| km-mail-poller safe phrase is fail-open (skips check when SSM unavailable) | unit | `go test ./pkg/compiler/... -run TestKmMailPollerSafePhraseFailOpen -v` | Wave 0 |
| skills/email/SKILL.md documents `--no-sign` flag | manual | Review `skills/email/SKILL.md` for `--no-sign` | N/A |
| skills/email/SKILL.md documents `external` JSON field | manual | Review `skills/email/SKILL.md` for `"external"` | N/A |

### Test Patterns — follow existing style

All tests call `generateUserData(emailProfile(), "sb-email-X", nil, "my-bucket", false, nil, "sandboxes.example.com")` and use `strings.Contains(out, pattern)`. Add all new functions to the existing `pkg/compiler/userdata_test.go` — no new test file is needed.

Example pattern for new tests:
```go
func TestKmSendNoSignFlagPresent(t *testing.T) {
    p := emailProfile()
    out, err := generateUserData(p, "sb-nosign-1", nil, "my-bucket", false, nil, "sandboxes.example.com")
    if err != nil {
        t.Fatalf("generateUserData failed: %v", err)
    }
    if !strings.Contains(out, "--no-sign") {
        t.Error("expected --no-sign flag handling in km-send")
    }
    if !strings.Contains(out, "NO_SIGN=false") {
        t.Error("expected NO_SIGN=false initialization in km-send")
    }
}
```

### Wave 0 Gaps
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmSendNoSignFlagPresent`, `TestKmSendNoSignOmitsXKMSenderID`, `TestKmSendNoSignOmitsXKMSignature`, `TestKmSendNoSignSkipsSSMSigningKey`, `TestKmSendNoSignKeepsKMAuth` (5 functions)
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmRecvUnfoldHeaders`, `TestKmRecvMultipartAlternative`, `TestKmRecvExternalLabel`, `TestKmRecvExternalJSONField` (4 functions)
- [ ] `pkg/compiler/userdata_test.go` — add `TestKmMailPollerSafePhrase`, `TestKmMailPollerSafePhraseGate`, `TestKmMailPollerSafePhraseFailOpen` (3 functions)

All additions go in the existing `userdata_test.go` — no new test file needed.

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... -run TestKmSend -v && go test ./pkg/compiler/... -run TestKmRecv -v && go test ./pkg/compiler/... -run TestKmMailPoller -v`
- **Per wave merge:** `go test ./pkg/compiler/... -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

## Sources

### Primary (HIGH confidence)
- `pkg/compiler/userdata.go` lines 606–1353 — km-mail-poller (606–684), km-send (688–919), km-recv (941–1353) — directly read and verified line numbers
- `pkg/compiler/userdata_test.go` lines 1247–1422 — existing email test patterns — directly read
- `infra/modules/ses/v1.0.0/main.tf` lines 61–141 — SES receipt rule architecture (S3-only for sandbox_inbound) — directly read
- `cmd/email-create-handler/main.go` — safe phrase Lambda implementation using SSM path `/km/config/remote-create/safe-phrase` — directly read
- `pkg/aws/mailbox.go` — `KMAuthPattern = regexp.MustCompile(...)`, `ParseSignedMessage`, `MatchesAllowList` — directly read
- `skills/email/SKILL.md`, `skills/sandbox/SKILL.md`, `skills/operator/SKILL.md`, `skills/user/SKILL.md` — all directly read; user.md does not need Phase 57 changes
- `.planning/ROADMAP.md` lines 1246–1258 — Phase 57 goal and plan structure — directly read

### Secondary (MEDIUM confidence)
- RFC 5322 folded header behavior: well-established standard; awk unfolding is a widely-used approach verified by multiple sources
- AL2023 python3 availability: Amazon Linux 2023 ships with python3 by default (standard AMI)
- Gmail multipart/alternative structure: confirmed by RFC 2045/2046 (MIME) and widespread documentation

### Tertiary (LOW confidence)
- None — all critical claims verified from codebase source directly

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all components verified from direct source inspection with line numbers
- Architecture: HIGH — all patterns derived from existing code; line references confirmed
- Pitfalls: HIGH — derived from observed code structure and known bash/MIME edge cases
- Test patterns: HIGH — directly mirrors existing test file patterns with verified function signatures
- Skills doc locations: HIGH — `skills/` directory exists at repo root; `.claude/skills/` does not exist

**Research date:** 2026-04-27
**Valid until:** 2026-05-27 (stable scripts; only changes when Phase 57 is implemented)
