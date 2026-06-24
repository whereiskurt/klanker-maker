# Phase 117 — Composable multi-parent profile inheritance (deep-merge + list-union extends)

> Source: operator request (2026-06-24). "I want to say this profile extends
> base/safenetwork.yaml and base/ir-tools.yaml and base/ml-tools.yaml etc. Take
> all the repeating stuff no one wants to see and make the smallest possible
> YAMLs. If there are duplicates, resolve to singles."

## Goal

A `SandboxProfile` can declare `extends:` as a **single string** (back-compat)
OR an **ordered list** of base references:

```yaml
# profiles/dc34.yaml — the whole leaf
extends: [base/safenetwork, base/ir-tools, base/ml-tools, base/agent-claude]
metadata: { name: dc34, prefix: dc34 }
spec:
  runtime: { instanceType: t3.large, additionalVolume: { size: 20, mountPoint: /data } }
  execution:
    env: { SANDBOX_MODE: goose-ebpf-gatekeeper, GOOSE_PROVIDER: aws_bedrock }
    initCommandsAppend:
      - "su - sandbox -c 'git clone --depth 1 https://github.com/whereiskurt/defcon.run.34 /workspace/defcon.run.34'"
```

km resolves it by **deep-merging** all bases + the child into one effective
profile, with **list-union de-duplication**, then validates the merged result.

## Motivation (the overlap that triggered this)

`profiles/dc34.yaml` vs `profiles/learn.v2.yaml` (+ the `learn.v2.chatty/polite/
codex/desktop` variants) share:
- **Byte-identical sections:** `lifecycle`, `network`, `iam`, `sidecars`,
  `observability`, `agent.claude` (tools/trustedDirectories/args), `cli`,
  `budget`, `artifacts`. ~80 lines each.
- **~90%-identical `execution.initCommands`** — same ~14-command head; deltas are
  only the claude-code/codex version pins + a few trailing clones/installers.

The four `learn.v2.*` variants are near-total copies of `learn.v2.yaml` — the
biggest duplication offenders and the ideal first children.

## Why the CURRENT `extends` can't do this

`pkg/profile/inherit.go` merges at **whole-section granularity** via reflection
(`mergeSpecSection`), with field-level mergers only for `notification` and
`agent` (the Phase 92 typed-merger zoo: `mergeNotificationSpec`,
`mergeAgentSpec`, …). Consequences:

| Section | Current inherit behavior |
|---|---|
| lifecycle, runtime, execution, sourceAccess, network, iam, sidecars, observability | **All-or-nothing** — child setting any field replaces the WHOLE section (so you cannot share an `initCommands` block and add one entry). |
| notification, agent | field-level merge (typed). |
| **budget, artifacts, email, cli** | **NOT merged at all** — child value used as-is; omit them → zero-value, NOT inherited. ⚠️ |

Also: `extends` is a single string; no multi-parent; lists always *replace*.

## Design / semantics

1. **`extends: string | []string`.** Custom YAML unmarshal for the union type.
   Single-string keeps working (now deep-merges — greenfield, see Safety).
2. **Multi-parent = a DAG.** Each base is itself resolved recursively (may carry
   its own `extends`). Diamond inheritance (D→[A,B], A→Core, B→Core) resolves
   **idempotently** (Core merged twice = same result after dedup). Extend the
   existing cycle-detection (`visited` map) + `maxInheritanceDepth` (currently 3,
   probably raise) to the DAG. Consider memoizing resolved bases.
3. **Precedence:** bases applied **left→right**, child applied **LAST**. Last/
   child wins on scalar collisions. **Order matters** (esp. `initCommands`).
4. **Deep merge of maps:** recursive key-union at EVERY nesting depth — `env`,
   `configFiles`, `metadata.labels`, and all nested spec blocks.
5. **Lists = concat + de-dup**, order-preserving, first-occurrence kept. Uniform
   across `initCommands`, `sourceAccess.github.allowedRepos`/`allowedRefs`,
   `network.egress.allowedDNSSuffixes`/`allowedHosts`, `email.allowedSenders`,
   `agent.claude.tools.autoApprove`, `execution.rsyncPaths`, `artifacts.paths`.
   Object-list entries (`runtime.additionalSnapshots`) de-dup by deep-equality.

## Implementation choice (key architectural decision)

Replace the typed per-section reflection merge with a **single generic recursive
deep-merge over the decoded `map[string]any`**:

```
for each profile in [base1, base2, …, child] (precedence order):
    parse YAML → map[string]any
acc := deepMerge(base1, base2, …, child)   // maps recurse; scalars last-wins;
                                            // lists concat+dedup
yaml.Unmarshal(marshal(acc)) → typed SandboxProfile
Validate(merged)
```

This **collapses the entire hand-written merger zoo** (`mergeSpecSection`,
`mergeNotificationSpec`, `mergeAgentSpec`, `mergeAgentClaudeSpec`, …) into ONE
function and makes every present + future field compose for free — the same way
`yq`'s `*`/`*+` operators work. Trade-off: no type-awareness during merge, but
our rule ("all lists concat+dedup") is uniform → type-awareness not needed.

## Fragments (partial bases)

`base/*.yaml` are PARTIAL — a fragment may set only `spec.network`. Bases must
NOT be validated standalone (they'd fail required-field checks); only the final
merged leaf is validated. Mark fragments so `km validate` and
`scripts/validate-all-profiles.sh` skip them.

**Path resolution:** `extends` entries resolve relative to the declaring file's
directory (support both `base/foo` and `base/foo.yaml`), falling back to
builtins / searchPaths. Today `load()` treats `extends` as a builtin name or
`<dir>/<name>.yaml`.

## Back-compat / safety

- **No profile uses `extends` today** (`grep -rn 'extends:' profiles/*.yaml` →
  empty) → zero production behavior change. Document that single-string `extends`
  now deep-merges.
- **No-`extends` profiles bypass merge entirely** → the frozen `learn.v2.yaml`
  byte-identity golden (`pkg/compiler` `TestUserdataLearnV2Phase92ByteIdentity`,
  `userdata_phase92_byte_identity_test.go`) is preserved. The Plan-04 refactor of
  `dc34` + `learn.v2.*` onto bases MUST byte-diff compiled userdata before/after
  to prove equivalence.

## Rough plan breakdown (for plan-phase to refine)

- **01 schema:** `extends` string|list union + custom unmarshal; fragment marker;
  relative-path resolution (`base/foo[.yaml]`).
- **02 deep-merge engine:** generic map-level recursive merge (maps recurse,
  scalars last-wins, lists concat+dedup); DAG cycle/depth/diamond handling;
  delete the typed mergers; exhaustive unit tests (idempotence, dedup, ordering,
  diamond, scalar-override, deep map nesting).
- **03 wire-in:** `profile.Resolve` + `km validate`/`km create`; fragments skip
  standalone validation; update `scripts/validate-all-profiles.sh` to skip
  `base/`.
- **04 author bases + refactor:** `base/{safenetwork,ir-tools,ml-tools,
  agent-claude,observability-standard,sidecars-all,budget-standard,…}.yaml`;
  refactor `dc34` + `learn.v2.*` onto them; prove compiled-output equivalence via
  userdata byte-diff (the byte-identity golden is the gate).
- **05 docs:** `OPERATOR-GUIDE.md` § Composable inheritance; `CLAUDE.md` Where-to-
  look + profile-spec note; `docs/agent-tool-gating.md` cross-ref; `km validate`/
  `km doctor` messaging for fragments.

## Open design questions (capture in PLAN; pick sane defaults, don't block)

- **A. Override/replace escape-hatch.** Is union+dedup always acceptable, or do we
  need a per-key "replace not union" directive in v1? Security allowlists usually
  want union, but a child wanting to **narrow** a base's `allowedHosts` can't via
  union. *Default:* union+dedup everywhere; document the narrowing limitation;
  consider a `!replace` YAML tag or `__replace:` key as a v2 follow-up.
- **B. Fragment marker mechanism.** `kind: SandboxProfileFragment` vs
  `metadata.abstract: true` vs `base/` dir convention. *Default lean:*
  `metadata.abstract: true` (explicit, location-independent, easy to skip in
  validate-all).
- **C. Middle-of-list override problem.** `dc34` vs `learn` pin DIFFERENT
  claude-code/codex versions inside the shared init block. Append/union can't
  override a middle line. *Default:* keep version-specific installs OUT of shared
  bases (each leaf's `initCommandsAppend`); a small `${VAR}` templating pass is a
  deferred follow-up.
- **D. `initCommandsAppend` field vs pure list concat.** If lists already
  concat+dedup, a child could just set `initCommands` with only its uniques and
  they'd append — but that conflicts with the "child replaces" intuition for a
  STANDALONE (no-extends) profile. *Default lean:* introduce an explicit
  `execution.initCommandsAppend` (and possibly a general `+key` append convention)
  so intent is unambiguous and a non-extending profile's `initCommands` still
  means "the complete list." Resolve in Plan 01/02.

## Validation gates

- `go test ./pkg/profile/... -count=1` green incl. new merge tests.
- `go test ./pkg/compiler/... -run ByteIdentity` green (learn.v2 unchanged).
- `scripts/validate-all-profiles.sh` green with `base/` fragments skipped and
  refactored leaves still valid.
- `km validate profiles/dc34.yaml` resolves multi-parent + validates merged leaf.
- Userdata byte-diff: compiled output of refactored `dc34` / `learn.v2.*` ==
  pre-refactor output (or a reviewed, intentional diff).
