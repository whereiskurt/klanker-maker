### Phase 117: Composable multi-parent profile inheritance (deep-merge list-union extends)

**Goal:** A SandboxProfile can declare `extends:` as a single string OR an ordered list of base references; km deep-merges all bases + the child into one effective profile (maps recurse, scalars child-wins, lists concat+dedup), then validates the merged leaf. Replaces the typed-merger zoo with a generic map deep-merge so every section composes; `profiles/base/` fragments (metadata.abstract:true) collapse the ~80-line-per-profile duplication.
**Requirements**: none mapped (new architectural phase — must_haves derived from phase GOAL)
**Depends on:** Phase 116
**Plans:** 4/5 plans executed

Plans:
- [ ] 117-01-PLAN.md — extends string|[]string union type, fragment marker, initCommandsAppend + JSON schema, fix 3 call sites
- [ ] 117-02-PLAN.md — generic deepMerge engine + DAG resolve (diamond/memoized); delete the typed merger zoo
- [ ] 117-03-PLAN.md — wire Resolve into km validate/create; abstract-fragment skip; validate-all skips base/
- [ ] 117-04-PLAN.md — author profiles/base/ fragments; refactor learn.v2.* + dc34; byte-identity gate
- [ ] 117-05-PLAN.md — docs: OPERATOR-GUIDE § Composable inheritance, CLAUDE.md pointers, agent-tool-gating xref
