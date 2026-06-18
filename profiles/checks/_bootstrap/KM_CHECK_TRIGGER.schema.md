# KM_CHECK_TRIGGER — Canonical Env-Var JSON Schema

**Source:** Phase 116 Plan 04 (single source of truth)
**Consumers:** `_km_check_bootstrap.py` (Lambda runtime eval), Plan 116-05 (`km check deploy` CLI bake), Plan 116-06 (`ttl-handler` CheckDispatch consumer)

---

## Overview

`KM_CHECK_TRIGGER` is a JSON object baked into each check Lambda's environment at
`km check deploy` / `km check sync` time. It encodes the operator's fire decision
(the predicate + what to do when it fires) without any operator-filesystem access at
runtime.

**Empty / absent trigger → never fires (capture-only mode):**
- Unset `KM_CHECK_TRIGGER` — treated as `{}` — no trigger is evaluated.
- `{}` (empty object) — no `when_py` key — no trigger evaluated.
- In both cases: stdout is still captured to S3; the bootstrap returns `{triggered: false}`.

---

## Schema (snake_case throughout — matches viper/operator config convention)

```json
{
  "check":            "<string>  — check name; redundant with KM_CHECK_NAME but duplicated here for self-containment",
  "when_py":          "<string>  — REQUIRED to enable triggering; the Python predicate body (see below)",
  "alias":            "<string>  — REQUIRED when when_py is present; the target sandbox alias",
  "prompt":           "<string>  — prompt template delivered to the sandbox; may use {{reason}} and {{out.<field>}}",
  "profile":          "<string>  — optional; profile slug used when on_absent=cold-create and the alias has no sandbox",
  "on_absent":        "cold-create | skip  — default: cold-create; what to do when alias resolves to no sandbox",
  "cooldown_seconds": "<int>     — optional; Stage B ttl-handler enforces cooldown via nonces table (key: check-trigger:<name>); default 0 (no cooldown)"
}
```

### Field details

#### `when_py` (string | absent)

A Python predicate **body** (not a full function). At runtime the bootstrap wraps it:

```python
def _pred(out):
    <body>          # each line indented 4 spaces
```

- `out` is the parsed JSON output dict from the snippet's stdout.
- Must `return` either a `bool` or a `(bool, reason)` tuple.
- Standard library is importable inside the predicate.
- Exception inside the predicate → logged, treated as **not triggered** (fail-closed for dispatch safety).

**Absent / empty string `when_py` → no trigger evaluated (capture-only).**

Inline example (baked into KM_CHECK_TRIGGER by km check deploy):
```
crit = [i for i in out.get('items', []) if i.get('sev') == 'crit']
return (len(crit) >= 3, f"{len(crit)} critical findings")
```

`@file` note: `when_py` in `km-config.yaml` may start with `@` (e.g. `@predicates/critical.py`).
The CLI resolves and inlines the file content **operator-side at deploy/sync time**.
The Lambda env var always contains the resolved inline string — never a path.
Editing a `@file` therefore requires `km check sync` to re-bake.

#### `alias` (string, REQUIRED when `when_py` is set)

The target sandbox alias for dispatch. The Stage B `ttl-handler` resolves this alias
via `DynamoAliasResolver.ResolveByAliasWithStatus` to find the sandbox id, then
resume-or-cold-creates.

#### `prompt` (string)

Prompt template delivered to the sandbox agent. Two substitution tokens:

- `{{reason}}` — the predicate's reason string (the second element of a `(bool, reason)` tuple; empty string if predicate returned plain `bool`).
- `{{out.<field>}}` — replaces with `str(out['<field>'])` for each top-level key in the output dict.

`@file` note: same operator-side resolution as `when_py`. Inline at runtime.

#### `profile` (string, optional)

Profile slug (e.g. `github-review`) used when `on_absent: cold-create` and the
alias resolves to no existing sandbox. Passed as `profile_name` in the CheckDispatch
event. Empty string when not set.

#### `on_absent` (enum: `cold-create` | `skip`, default `cold-create`)

What the Stage B dispatcher does when the alias has no sandbox (or the row is absent):
- `cold-create` — `PutSandboxCreate` with the specified `profile` and expanded prompt.
- `skip` — log and drop the event; no sandbox created.

#### `cooldown_seconds` (int, default 0)

Enforced by Stage B (`ttl-handler`), NOT by the bootstrap. The nonces table key is
`check-trigger:<check-name>`. A value of 0 means no cooldown (fires every invocation
that passes the predicate). The bootstrap passes this value through in the
CheckDispatch event detail; ttl-handler enforces it.

---

## CheckDispatch event emitted by the bootstrap (Stage A → Stage B contract)

When `when_py` evaluates to truthy, the bootstrap emits exactly ONE event:

```json
{
  "Source": "km.sandbox",
  "DetailType": "CheckDispatch",
  "EventBusName": "<default bus, or KM_EVENT_BUS env var if set>",
  "Detail": {
    "event_type":       "check-dispatch",
    "check_name":       "<KM_CHECK_NAME>",
    "alias":            "<trigger.alias>",
    "prompt":           "<fully expanded prompt string>",
    "profile_name":     "<trigger.profile or empty string>",
    "on_absent":        "<trigger.on_absent, default cold-create>",
    "reason":           "<predicate reason, may be empty string>",
    "cooldown_seconds": <int>,
    "auto_start":       true
  }
}
```

**Key contract notes (for Plan 116-06 consumer):**
- `event_type` is always the string `"check-dispatch"` (ttl-handler routes on this).
- `check_name` (not `check`) is the run-identifying field — matches `KM_CHECK_NAME`.
- `alias` is the resolved sandbox alias (not the sandbox id).
- `auto_start: true` signals ttl-handler to auto-resume a stopped/paused sandbox.
- All keys are snake_case (matches the existing EventBridge envelope conventions).
- `cooldown_seconds` is passed through; Stage B enforces the nonces-table check.

---

## Bootstrap return value

The Lambda handler returns a dict (serialised as JSON invoke response):

```json
{
  "triggered":    true | false,
  "reason":       "<string>",
  "output_key":   "check-runs/<name>/<ts>/output.json"
}
```

`output_key` is present whenever stdout was successfully written to S3. On a subprocess
non-zero exit the output (possibly empty) is still written and `output_key` is set;
`triggered` is false.

---

## Non-JSON stdout guard

If the snippet's stdout does not parse as JSON:
- The raw stdout is still written verbatim to S3 (capture-only).
- The event `check_output_not_json: <name>` is logged to stdout (CloudWatch).
- The bootstrap returns `{triggered: false, reason: "non-JSON output", output_key: "..."}`.
- **No CheckDispatch event is emitted.** This is a hard guard, not configurable.
