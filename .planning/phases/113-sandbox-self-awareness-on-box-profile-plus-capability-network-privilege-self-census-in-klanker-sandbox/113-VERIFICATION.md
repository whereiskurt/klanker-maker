---
phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox
verified: 2026-06-14T00:00:00Z
status: passed
score: 12/12 must-haves verified
re_verification: false
---

# Phase 113: Sandbox Self-Awareness Verification Report

**Phase Goal:** Give an on-box agent a single, reliable way to answer *who am I, what can I do, where do I sit on the network, and what is locked down and why* — grounded in both the declarative profile and live runtime probes.
**Verified:** 2026-06-14
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Every EC2 sandbox writes its rendered profile to `/opt/km/.km-profile.yaml` at boot | VERIFIED | Section 2.10 template block at userdata.go:453-464; `cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'` with `chown sandbox:sandbox` + `chmod 0644` |
| 2 | The on-box profile is byte-sourced from the same `*profile.SandboxProfile` that drives all other userdata fields (no re-marshal drift) | VERIFIED | `yaml.Marshal(p)` called immediately before `tmpl.Execute` at userdata.go:5749-5751; populates `params.ProfileYAML` from the exact pointer passed to `generateUserData` |
| 3 | The file is owned sandbox:sandbox, mode 0644, readable by the on-box agent | VERIFIED | Template lines 461-462: `chmod 0644 /opt/km/.km-profile.yaml` + `chown sandbox:sandbox /opt/km/.km-profile.yaml`; live UAT confirmed 0644 / sandbox:sandbox |
| 4 | The embedded YAML round-trips: parsing it back yields a SandboxProfile with the same key fields | VERIFIED | `TestUserdataProfileYAMLRoundTrip` exists and passes; full pkg/compiler suite: `ok github.com/whereiskurt/klanker-maker/pkg/compiler 7.013s` (exit 0) |
| 5 | Existing byte-identity golden tests pass after regeneration | VERIFIED | All three goldens contain `/opt/km/.km-profile.yaml` (4 occurrences each); full suite green |
| 6 | klanker:sandbox runs a six-section self-census (A–F) with graceful profile-absent fallback | VERIFIED | SKILL.md 390 lines; 6 H2 section anchors (A–F) confirmed; `PROFILE_AVAILABLE` guard present on every profile-derived block |
| 7 | Network section infers enforcement from runtime signals (not KM_ENFORCEMENT), runs exactly two curls | VERIFIED | `EBPF_ACTIVE=$(systemctl is-active km-ebpf-enforcer.service ...)` at SKILL.md line 178; `HTTPS_PROXY`/injected-CA proxy inference; exactly two `curl --max-time 5` calls (one allowed, one blocked); live UAT confirmed exactly 2 curls |
| 8 | Privilege section probes `sudo -n true`, cross-checks spec.execution.privileged, explains each restriction | VERIFIED | `if sudo -n true 2>/dev/null` at SKILL.md line 275; PROFILE_AVAILABLE-gated cross-check at lines 283-292; explanatory text ("no sudo because privileged: false; this is intentional") |
| 9 | Plugin version bumped to 0.4.10 in BOTH plugin.json and marketplace.json | VERIFIED | `jq .version .claude-plugin/plugin.json` = `0.4.10`; `jq '.plugins[0].version' .claude-plugin/marketplace.json` = `0.4.10`; strings match |
| 10 | `docs/operational-gotchas.md` has a Phase 113 section with `/opt/km/.km-profile.yaml` | VERIFIED | Section at line 527 "Sandbox self-awareness (Phase 113)" + deploy surface documented; `/opt/km/.km-profile.yaml` referenced throughout |
| 11 | `CLAUDE.md` has a Phase 113 summary block | VERIFIED | Lines 21+ in CLAUDE.md: "Phase 113 (2026-06-14) — Sandbox self-awareness..." + Where-to-look row at line 166 |
| 12 | No SandboxProfile schema change; `scripts/validate-all-profiles.sh` stays green | VERIFIED | 113-03-SUMMARY confirms 22/22 green; no schema file changes across the 10 phase commits; plan frontmatter has `requirements: []` |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/compiler/userdata.go` | ProfileYAML field + section 2.10 template + yaml.Marshal wiring | VERIFIED | `ProfileYAML string` at line 4943; section 2.10 at lines 453-464; `yaml.Marshal(p)` at line 5749; `yaml "github.com/goccy/go-yaml"` import at line 14 |
| `pkg/compiler/userdata_phase113_test.go` | TestUserdataProfileWriteBlockRendered + TestUserdataProfileYAMLRoundTrip | VERIFIED | 100-line file; both function names confirmed; full suite exit 0 |
| `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` | Regenerated golden containing 2.10 block | VERIFIED | 4 occurrences of `/opt/km/.km-profile.yaml` |
| `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` | Regenerated golden containing 2.10 block | VERIFIED | 4 occurrences of `/opt/km/.km-profile.yaml` |
| `pkg/compiler/testdata/h1_byte_identity_golden.txt` | Regenerated H1 golden containing 2.10 block | VERIFIED | 4 occurrences of `/opt/km/.km-profile.yaml` |
| `skills/sandbox/SKILL.md` | Six-section self-census, min 120 lines, graceful fallback | VERIFIED | 390 lines; 8 H2 headers (preamble + cross-refs + 6 sections); all required patterns present |
| `.claude-plugin/plugin.json` | version = 0.4.10 | VERIFIED | `jq .version` = `"0.4.10"` |
| `.claude-plugin/marketplace.json` | plugins[0].version = 0.4.10 | VERIFIED | `jq '.plugins[0].version'` = `"0.4.10"` |
| `docs/operational-gotchas.md` | Phase 113 section + `/opt/km/.km-profile.yaml` reference | VERIFIED | Section at line 527; deploy surface documented at line 573 |
| `CLAUDE.md` | Phase 113 summary block + Where-to-look row | VERIFIED | Summary at line 21; Where-to-look row at line 166 |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `generateUserData()` | `params.ProfileYAML` | `yaml.Marshal(p)` before `tmpl.Execute` | WIRED | userdata.go line 5749: `if profileYAMLBytes, marshalErr := yaml.Marshal(p); marshalErr == nil { params.ProfileYAML = string(profileYAMLBytes) }` |
| Template section 2.10 | `/opt/km/.km-profile.yaml` | `cat > heredoc` gated on `{{- if .ProfileYAML }}` | WIRED | Lines 453-464: template guard + heredoc + chmod + chown |
| `skills/sandbox/SKILL.md` section preamble | `/opt/km/.km-profile.yaml` | `cat` with `2>/dev/null` fallback → `PROFILE_AVAILABLE` | WIRED | Line 26: `KM_PROFILE=$(cat /opt/km/.km-profile.yaml 2>/dev/null)`; lines 27-32: PROFILE_AVAILABLE guard |
| `plugin.json` | `marketplace.json` | identical version string `0.4.10` | WIRED | Both files return `0.4.10` via jq |

### Requirements Coverage

No REQUIREMENTS.md IDs declared for this phase (`requirements: []` in all three plans). Phase is self-contained; requirements derived from plan must_haves.

### Anti-Patterns Found

None. Scanned `pkg/compiler/userdata_phase113_test.go`, `skills/sandbox/SKILL.md`, and the modified sections of `pkg/compiler/userdata.go`. No TODO/FIXME/PLACEHOLDER, no empty return stubs, no console.log-only handlers, no unconnected state.

### Human Verification Required

The live UAT was performed by the orchestrator and recorded in `113-UAT.md` (status: RESOLVED / PASSED). Key results already verified programmatically against the UAT record:

1. **On-box profile semantic equivalence** — `apiVersion`/`metadata.name` round-trip match confirmed. Three `false`-valued booleans omitted by `omitempty` are semantically equivalent (deserialize to `false`). Signed off.
2. **Section C network parsing fixes** — four bugs found during live UAT were fixed in commit `9e3918aa` and re-verified PASS on the live box. The fix (DNAT counter, enforcement parse, egress-allowlist scope, safe-active probe host selection) is committed and present in SKILL.md.
3. **Redaction sign-off** — "write VERBATIM, no redaction" confirmed in UAT. Profile carries SSM paths (not values); sandbox user already has IAM read on those paths.
4. **Exactly two curls** — UAT confirms `api.anthropic.com` → HTTP 404 (reachable through proxy) and `evil.example.com` → 000 (unreachable). No re-testing of live infrastructure needed.

These items are marked human-verified via the live UAT record, not re-run here.

### Gaps Summary

None. All 12 must-haves are fully verified across all three levels (exists, substantive, wired). The pkg/compiler test suite ran clean (`ok ... 7.013s`, exit 0). Git history confirms 10 real commits for this phase across Plans 01-03, with the Section C live-UAT fix folded in at `9e3918aa`. No blocker anti-patterns found.

---

_Verified: 2026-06-14_
_Verifier: Claude (gsd-verifier)_
