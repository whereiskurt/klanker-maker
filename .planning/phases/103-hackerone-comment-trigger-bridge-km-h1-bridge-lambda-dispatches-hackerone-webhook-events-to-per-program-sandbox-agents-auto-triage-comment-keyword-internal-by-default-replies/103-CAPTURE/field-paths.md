# Phase 103 — HackerOne webhook field-path pinning

**Captured:** 2026-06-09
**Capture provenance:** **SYNTHETIC FALLBACK (pre-authorized).** The two bodies in this
directory (`report_created.json`, `report_comment_created.json`) are NOT a live webhook
capture. They are modeled on the HackerOne docs webhook payload shape (top-level `data` ->
`data.activity` + `data.report`), with the INNER report-object field paths set to the
**live-confirmed** values recorded in `103-CONTEXT.md` § "Live sanity test" (2026-06-09,
read-only customer-API check, HTTP 200).

**Why synthetic now:** the operator's free HackerOne **Sandbox program** (the only live
target for this phase — touches no real researchers/programs) is being provisioned in
parallel. The real webhook capture + envelope-wrapper confirmation is **deferred to Plan 10
(Wave 6) E2E**, where the Sandbox program fires a real `Test request` and a real
report_comment. Until then, Plan 03's payload parser MUST be **wrapper-tolerant** per
RESEARCH.md Open Question 1 (see "Parse-tolerance directive" below).

---

## Confidence legend
- **LIVE-CONFIRMED** — verified against the live customer REST API report-object (read-only,
  2026-06-09). The webhook nests the report under a `data.report` wrapper, but the inner
  report-object paths are the same JSON:API shape.
- **DOCS-SHAPED** — taken from the HackerOne docs webhook payload example; not yet pinned
  against a real webhook delivery. Confirm in Plan 10.
- **LOW** — unconfirmed; downstream must treat as best-effort / fast-follow.

---

## Pinned paths (relative to the webhook top-level JSON object)

| Field | JSON path | Confidence | Notes |
|---|---|---|---|
| **Envelope wrapper** | `data.activity` + `data.report` | DOCS-SHAPED | Top-level `data` object holds both. Whether the report is `data.report` vs `data.report.data` (extra JSON:API `data` nesting) is UNCONFIRMED — pin in Plan 10. Parser must tolerate both. |
| **Program handle (OQ1 — routing key)** | `data.report.relationships.program.data.attributes.handle` | LIVE-CONFIRMED | Matched the RESEARCH.md OQ1 candidate exactly against the live report object. This is the resolve key (`h1.programs:` lookup). |
| **Report id** | `data.report.id` | LIVE-CONFIRMED | Report-object top-level `id`. Thread-continuity key (+ target). |
| **Report title** ({{title}} pre-expansion) | `data.report.attributes.title` | LIVE-CONFIRMED | |
| **Report state** ({{state}} pre-expansion) | `data.report.attributes.state` | LIVE-CONFIRMED | e.g. `new`, `triaged`. |
| **Activity actor username (loop-guard + allowlist key)** | `data.activity.relationships.actor.data.attributes.username` | LIVE-CONFIRMED | The `allow:` deny-by-default authz key AND the self-loop guard identity. |
| **Activity internal flag (SAFETY-CRITICAL visibility)** | `data.activity.attributes.internal` | LIVE-CONFIRMED | Boolean. Internal comments carry `internal:true`. Confirms whether an incoming comment is internal/external. |
| **Comment body / message** | `data.activity.attributes.message` | LIVE-CONFIRMED | The @-handle scan + `/command` parse source on `report_comment_created`. Empty string on `report_created`. |
| **Activity type** | `data.activity.type` | DOCS-SHAPED | e.g. `activity-comment`, `activity-report-created`. Useful secondary discriminator alongside the `X-H1-Event` header. |

---

## Headers (from `_synthetic_headers` in each body; casing per HackerOne docs)

| Header | Value shape | Confidence | Notes |
|---|---|---|---|
| `X-H1-Event` | event type string (e.g. `report_created`, `report_comment_created`) | DOCS-SHAPED | Event type read from header, NOT duplicated in body. |
| `X-H1-Delivery` | GUID | DOCS-SHAPED | Dedup key -> `{prefix}-slack-bridge-nonces` with `h1-delivery:` prefix, 24h TTL. |
| `X-H1-Signature` | `sha256=<hexdigest>` HMAC-SHA256 of raw body | DOCS-SHAPED | Same scheme as GitHub `X-Hub-Signature-256`. Reuse constant-time verify with header-name swap. |

Exact header **casing** observed in a real delivery is UNCONFIRMED (Plan 10). The Lambda
lowercases all headers anyway, so casing is non-load-bearing — recorded for completeness.

---

## State-change endpoint (OQ2)

**Confidence: LOW — DEFERRED.** Not confirmable from a webhook capture (it is an outbound
customer-API call, not a webhook field). The customer-API reference lists report state
transitions under the report `activities`/state surface; candidates are
`POST /reports/{id}/state_changes` vs `PATCH /reports/{id}`. **Not pinned here.**

**Directive:** `km-h1 state` may ship as a **fast-follow** — it is the least-critical verb
(the in-scope safety-critical paths are `comment` internal-by-default and read). Confirm the
exact endpoint against the live HackerOne Sandbox customer-API during Plan 10 / when `km-h1
state` is implemented.

---

## Parse-tolerance directive for Plan 03 (consequence of synthetic capture)

Because the wrapper placement is DOCS-SHAPED (not live-pinned), `pkg/h1/bridge/payload.go`
MUST:
1. Locate the report object tolerantly: accept BOTH `data.report.{...}` and
   `data.report.data.{...}` (JSON:API double-`data` nesting). Prefer the deepest object that
   carries `relationships.program`.
2. Treat a missing program handle as a **hard resolve-miss** (drop with a log line), never a
   panic — a real-delivery wrapper surprise must fail safe, not crash.
3. Read the event type from the `X-H1-Event` header, falling back to `data.activity.type`
   only as a secondary discriminator.
4. Re-pin every DOCS-SHAPED row above against the real HackerOne Sandbox delivery in Plan 10
   and tighten the parser if the live wrapper differs.

---

## Provenance summary

- **OQ1 (program-handle path):** RESOLVED — `data.report.relationships.program.data.attributes.handle` (LIVE-CONFIRMED inner path; wrapper DOCS-SHAPED).
- **OQ2 (state endpoint):** DEFERRED LOW-confidence — confirm in Plan 10; `km-h1 state` may be a fast-follow.
- **Capture type:** SYNTHETIC FALLBACK. Real HackerOne **Sandbox** program webhook capture + envelope-wrapper + header-casing confirmation is **deferred to Plan 10 (Wave 6) E2E**. No production program is or will be used as a target.
