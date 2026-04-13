# Phase 53: Persistent Local Sandbox Numbering - Context

**Gathered:** 2026-04-13
**Status:** Ready for planning
**Source:** Design spec (docs/superpowers/specs/2026-04-13-persistent-local-sandbox-numbering-design.md)

<domain>
## Phase Boundary

Replace ephemeral positional numbering in `km list` with persistent local numbers assigned at sandbox creation time. Numbers count up monotonically from 1, survive create/destroy cycles, and only reset when zero sandboxes remain.

</domain>

<decisions>
## Implementation Decisions

### Storage
- Local JSON file at `~/.config/km/local-numbers.json`
- Schema: `{"next": int, "map": {"sandbox-id": number}}`
- Atomic write via tmp file + rename
- Missing file treated as empty state

### Number Assignment
- Numbers assigned at create time, counting up from 1
- `next` field tracks the next number to assign
- Numbers never reuse while any sandbox exists
- Counter resets to 1 only when map is completely empty (zero sandboxes)

### Display
- `km list` shows persistent numbers instead of positional index
- `km create` prints the assigned number

### Resolution Priority (unchanged)
- Alias first (DynamoDB GSI)
- Sandbox ID pattern second
- Local number third (replaces positional lookup)

### Reconciliation
- `km list` prunes entries for sandboxes no longer in DynamoDB
- Sandboxes without a local number (remote creates) get assigned next available number on first `km list`

### Cleanup
- Destroy removes sandbox entry from map
- If map becomes empty, reset `next` to 1

### Claude's Discretion
- Package naming and internal structure of `pkg/localnumber/`
- Exact error handling for file I/O edge cases
- Test strategy

</decisions>

<specifics>
## Specific Ideas

- New package: `pkg/localnumber/` with `Load`, `Save`, `Assign`, `Remove`, `Resolve`, `Reconcile` functions
- Touchpoints: `create.go`, `list.go`, `sandbox_ref.go`, destroy path
- Numbers are local-only, not synced to DynamoDB
- Aliases remain the global cross-machine identifier

</specifics>

<deferred>
## Deferred Ideas

None — design spec covers phase scope

</deferred>

---

*Phase: 53-persistent-local-sandbox-numbering*
*Context gathered: 2026-04-13 via design spec*
