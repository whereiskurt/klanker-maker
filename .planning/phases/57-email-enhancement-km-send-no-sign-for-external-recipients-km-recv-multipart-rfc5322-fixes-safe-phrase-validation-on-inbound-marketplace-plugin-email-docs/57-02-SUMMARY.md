---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
plan: "02"
subsystem: compiler/userdata
tags: [km-recv, rfc5322, multipart, mime, external-display, json, bash, heredoc]

requires:
  - phase: 57-00
    provides: 5 RED test stubs for km-recv in userdata_phase57_test.go
  - phase: 57-01
    provides: km-send --no-sign flag (no dependency, parallel wave)

provides:
  - km-recv RFC 5322 folded header unfolding via unfold_headers() awk function
  - km-recv two-level multipart/alternative body extraction for Gmail layout
  - km-recv [EXTERNAL] human-display hint + "external" boolean in JSON output

affects:
  - phase: 57-03 (km-mail-poller safe phrase gate — unrelated tests remain RED)
  - phase: 57-04 (skills docs need to document "external" JSON field)

tech-stack:
  added: []
  patterns:
    - "unfold_headers() awk one-liner: RFC 5322 continuation join applied to header section only"
    - "Split-unfold-recombine in process_messages(): raw_headers | unfold_headers + original body = raw_for_parse"
    - "Two-level extract_body(): outer scan captures alt_boundary; second pass scans inside alt_boundary"
    - "strip_html() sed pipeline: tag removal + entity decode + whitespace collapse"
    - "[EXTERNAL] from_display: conditional on HDR_SENDER_ID empty AND SIG_STATUS=unsigned"
    - "json_external field: [ -z HDR_SENDER_ID ] detection, no CLI flag needed"
    - "Backtick avoidance in Go raw string (Pitfall 1): replaced backtick chars in bash comments with -- and plain text"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "unfold_headers applied ONLY to header section (not body) -- base64 attachment lines starting with space would be corrupted otherwise (Pitfall 3 from RESEARCH.md)"
  - "alt_boundary separate variable from outer boundary -- avoids boundary variable collision in second-level scan (Pitfall 6 from RESEARCH.md)"
  - "from-external implemented as automatic detection (no CLI flag) -- absence of X-KM-Sender-ID AND SIG_STATUS=unsigned is always correct; matches RESEARCH.md Pattern 5 deviation note"
  - "Backtick characters in bash comments inside Go raw string literal cause syntax error -- replaced with -- and plain text to satisfy Go raw string constraint"
  - "[ -z ] / [ = ] POSIX test used for ExternalDisplay guard -- matches surrounding code style (uses [ ] not [[ ]])"

requirements-completed:
  - PHASE57-KMRECV-RFC5322
  - PHASE57-KMRECV-MULTIPART
  - PHASE57-KMRECV-EXTERNAL

duration: 235s
completed: 2026-04-28T20:36:33Z
---

# Phase 57 Plan 02: km-recv RFC 5322 + multipart/alternative + [EXTERNAL] Summary

**Three surgical edits to the KMRECV heredoc in userdata.go fix inbound Gmail email parsing: RFC 5322 folded header unfolding, two-level multipart/alternative body extraction, and automatic [EXTERNAL] display for unsigned external senders**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-04-28T20:32:38Z
- **Completed:** 2026-04-28T20:36:33Z
- **Tasks:** 3
- **Files modified:** 1

## Accomplishments

- All 5 Phase-57 km-recv tests turned GREEN: FoldedHeaders, MultipartAlternative, NestedMultipart, ExternalDisplay, ExternalJSONField
- All 6 pre-existing TestKmRecv* tests remain GREEN: PresentWhenEmailSet, AbsentWhenNoEmail, ContainsDynamoDBLookup, ContainsOpensslVerify, ContainsMailDir, ContainsSPKIDERPrefix
- `make build` succeeds: km v0.2.411

## The 3 Surgical Edits Made to the KMRECV Heredoc

### Task 1: unfold_headers() + header-section split in process_messages()

**Edit 1A** — added unfold_headers() function immediately before parse_headers():

```bash
unfold_headers() {
  awk 'BEGIN{line=""} /^[ \t]/{sub(/^[ \t]+/, " "); line = line $0; next} {if(line!="") print line; line=$0} END{if(line!="") print line}'
}
```

**Edit 1B** — replaced `parse_headers "$raw"` in process_messages() with split-unfold-recombine:

```bash
local raw_headers raw_body raw_for_parse
raw_headers=$(printf '%s' "$raw" | awk '/^[[:space:]]*$/{exit} {print}')
raw_body=$(printf '%s' "$raw" | awk 'found{print} /^[[:space:]]*$/{found=1}')
raw_for_parse="$(printf '%s' "$raw_headers" | unfold_headers)

${raw_body}"
parse_headers "$raw_for_parse"
```

extract_body() continues to receive the original `$raw` — base64 attachment lines are preserved.

### Task 2: strip_html() helper + two-level extract_body()

**Edit 2A** — added strip_html() before extract_body():

```bash
strip_html() {
  sed 's/<[^>]*>//g' | sed 's/&nbsp;/ /g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g' | tr -s ' \n' | sed '/^[[:space:]]*$/d'
}
```

**Edit 2B** — extended extract_body() with:
- `alt_boundary` capture variable during first-level scan when `Content-Type:.*multipart/alternative` is seen
- Second-level scan inside `alt_boundary` for text/plain (with fresh local variables alt_in_text_plain, alt_past_part_headers, alt_body_lines)
- HTML fallback: captures text/html inside alt_boundary, pipes through strip_html if no text/plain found
- Original first-level and single-part fallback paths unchanged

### Task 3: [EXTERNAL] display hint + "external" JSON field

**Edit 3A** — added json_external field to JSON output:

```bash
local json_external="false"
[ -z "$HDR_SENDER_ID" ] && json_external="true"
# printf format: ,"external":%s between ,"encrypted":%s and ,"body":"%s"
```

**Edit 3B** — replaced `from_display="${HDR_SENDER_ID:-${HDR_FROM}}"` with:

```bash
if [ -z "$HDR_SENDER_ID" ] && [ "$SIG_STATUS" = "unsigned" ]; then
  from_display="${HDR_FROM} [EXTERNAL]"
else
  from_display="${HDR_SENDER_ID:-${HDR_FROM}}"
fi
```

## Test Results

```
=== RUN   TestUserData_KmRecv_FoldedHeaders
--- PASS: TestUserData_KmRecv_FoldedHeaders (0.00s)
=== RUN   TestUserData_KmRecv_MultipartAlternative
--- PASS: TestUserData_KmRecv_MultipartAlternative (0.00s)
=== RUN   TestUserData_KmRecv_NestedMultipart
--- PASS: TestUserData_KmRecv_NestedMultipart (0.00s)
=== RUN   TestUserData_KmRecv_ExternalDisplay
--- PASS: TestUserData_KmRecv_ExternalDisplay (0.00s)
=== RUN   TestUserData_KmRecv_ExternalJSONField
--- PASS: TestUserData_KmRecv_ExternalJSONField (0.00s)
PASS

TestKmRecvPresentWhenEmailSet     PASS
TestKmRecvAbsentWhenNoEmail       PASS
TestKmRecvContainsDynamoDBLookup  PASS
TestKmRecvContainsOpensslVerify   PASS
TestKmRecvContainsMailDir         PASS
TestKmRecvContainsSPKIDERPrefix   PASS
PASS
```

## make build Output

```
go build -ldflags '-X .../version.Number=v0.2.411 ...' -o km ./cmd/km/
Built: km v0.2.411 (d717fa4)
```

## Task Commits

1. **Task 1: unfold_headers() awk function + header-section split** - `5c4fb17`
2. **Task 2: two-level extract_body() + strip_html helper** - `d717fa4`
3. **Task 3: [EXTERNAL] display hint + external JSON field** - `aed2d65`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Backtick characters in bash comments broke Go raw string literal**
- **Found during:** Task 2
- **Issue:** The new extract_body() comments used backtick characters (e.g., the regex matches `boundary=` regardless...) inside the Go backtick-delimited raw string constant `userDataTemplate`. A backtick terminates the Go raw string, causing a Go syntax error at the comment line.
- **Fix:** Replaced backtick chars in two bash comments with -- and plain text: "the regex matches boundary= regardless of subtype" and "Content-Type: multipart/alternative"
- **Files modified:** pkg/compiler/userdata.go
- **Commit:** d717fa4

## Notes

- `--from-external` was implemented as automatic detection (no CLI flag) — absence of X-KM-Sender-ID AND SIG_STATUS=unsigned is always the correct signal; aligns with RESEARCH.md Pattern 5 deviation note in 57-02-PLAN.md
- Body extraction (extract_body) continues to receive original `$raw` — the unfold_headers pass is only applied to raw_for_parse used by parse_headers (Pitfall 3 honored)
- The 4 .eml fixtures from Plan 00 exist on disk and are reserved for manual UAT in Plan 04

## Self-Check: PASSED
