# README → Wiki Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the deep reference content out of the 995-line `README.md` into the GitHub Wiki, leaving a lean ~100-line landing-page README that opens with a security-focused blurb.

**Architecture:** A *split*, not a delete. The bulk of README sections are extracted verbatim into 7 themed Wiki pages (+ `_Sidebar`) in the separate `klanker-maker.wiki.git` repo. A `README.md` must remain at repo root because `.goreleaser.yaml:53` bundles it into release tarballs. Wiki work happens in a scratchpad clone and is pushed directly; the README slim-down is committed on branch `docs/readme-to-wiki` in the main repo.

**Tech Stack:** Markdown, git (two repos), `sed`/`grep` for mechanical extraction. No build/test toolchain — verification is grep-based structural checks and link audits.

## Global Constraints

- A `README.md` MUST continue to exist at repo root — it is a release artifact (`.goreleaser.yaml:53`). Never delete it.
- Documentation prose moves **verbatim** — do not rewrite the substance of moved sections (out of scope). The only *new* prose is the approved security blurb and the Wiki-link callouts.
- `OPERATOR-GUIDE.md` and `docs/` are NOT moved — Wiki pages link to them in-repo.
- Wiki repo: `https://github.com/whereiskurt/klanker-maker.wiki.git`. It currently holds a 35-byte placeholder `Home.md`.
- Main-repo work stays on branch `docs/readme-to-wiki` (already created). Do not push/merge the main repo unless the user asks.
- Scratchpad dir for the Wiki clone: `/private/tmp/claude-501/-Users-khundeck-working-klankrmkr/499d8231-9b6c-48d8-b718-248bd02738d6/scratchpad`
- Security blurb copy (verbatim, approved in spec `docs/superpowers/specs/2026-06-24-readme-to-wiki-design.md`):

  > **Built for security teams.** You're on a security team responsible for hundreds of repos, and you need to move fast — triage, patch, review, and reason about vulnerabilities — without the investigation itself becoming the next breach. Klanker Maker gives you isolated, policy-governed sandboxes where untrusted code, dependencies, and AI agents run inside a contained blast radius.
  >
  > Isolation is the product. Every sandbox is **default-deny on the network**: an explicit allowlist controls which hosts it can reach, which secrets it can read, and how much it can spend. These are intentional design choices to make **data exfiltration** and **supply-chain compromise** hard by construction — a malicious dependency, a poisoned build step, or a compromised agent has nowhere to phone home and nothing ambient to steal. Patch fast, review at scale, and rationalize about vulns without trusting the thing you're investigating.

## Section → Wiki page map

README section line ranges (re-derive with `grep -n '^## ' README.md` before extracting; current map for README at HEAD = 995 lines):

| README section | Lines | Destination |
|---|---|---|
| What Klanker Maker Is | 76–91 | **lean README** + Wiki `Home` |
| How It Compares | 92–117 | **lean README** |
| Quick Start | 118–159 | **lean README** (trimmed) + Wiki `Getting-Started` (full) |
| Core Capabilities | 197–214 | **lean README** |
| License & Project Status | 973–995 | **lean README** |
| Why This Exists | 160–196 | Wiki `Home` |
| Cloud-Native Control Plane | 215–246 | Wiki `Architecture` |
| Slack-Native Operations | 247–294 | Wiki `Integrations` |
| GitHub App Integration | 295–329 | Wiki `Integrations` |
| Multi-Agent Orchestration via Signed Email | 330–354 | Wiki `Integrations` |
| AWS Account Architecture | 355–390 | Wiki `Architecture` |
| Security Model | 391–447 | Wiki `Security-and-Network` |
| Network Enforcement | 448–528 | Wiki `Security-and-Network` |
| Budget Enforcement | 529–574 | Wiki `Security-and-Network` |
| SandboxProfile | 575–672 | Wiki `Profiles-and-Agents` |
| Built-in Profiles | 673–685 | Wiki `Getting-Started` |
| Substrates | 686–699 | Wiki `Getting-Started` |
| Non-Interactive Agent Execution | 700–733 | Wiki `Profiles-and-Agents` |
| Scheduling and Recurring Operations | 734–759 | Wiki `Profiles-and-Agents` |
| AMI Lifecycle | 760–775 | Wiki `Profiles-and-Agents` |
| CLI Reference | 776–871 | Wiki `CLI-Reference` |
| Architecture | 872–922 | Wiki `Architecture` |
| Documentation | 923–941 | Wiki `Home` ("Further reading") |
| Roadmap | 942–972 | Wiki `Home` ("Further reading") — points to in-repo `ROADMAP.md` |
| Table of Contents | 47–75 | DROPPED (replaced by `_Sidebar`) |

**Spec reconciliation:** the spec Part 3 mentions a "Documentation" Wiki page; to keep exactly the 7 pages in the Part 2 table, Documentation + Roadmap are folded into a "Further reading" section at the bottom of `Home` (which is still "a Wiki page that links out to docs/"). 7 content pages + `_Sidebar`.

---

### Task 1: Set up and verify the Wiki working copy

**Files:**
- Create (working copy only): `<scratchpad>/wiki/` (clone of `klanker-maker.wiki.git`)

**Interfaces:**
- Produces: a clean, push-verified clone at `$WIKI` for Tasks 2–4.

- [ ] **Step 1: Clone the Wiki repo into the scratchpad**

```bash
SCRATCH="/private/tmp/claude-501/-Users-khundeck-working-klankrmkr/499d8231-9b6c-48d8-b718-248bd02738d6/scratchpad"
WIKI="$SCRATCH/wiki"
rm -rf "$WIKI"
git clone https://github.com/whereiskurt/klanker-maker.wiki.git "$WIKI"
```

Expected: `Cloning into '...'` then exit 0; `$WIKI/Home.md` exists (placeholder).

- [ ] **Step 2: Verify push access with a no-op empty commit**

```bash
cd "$WIKI"
git commit --allow-empty -m "chore: verify wiki push access"
git push origin HEAD
```

Expected: push succeeds. **If push fails on auth**, STOP and report to the user — they chose direct-push (option A); do not silently fall back. (Read-side clone is already confirmed working.)

- [ ] **Step 3: Undo the no-op so history stays clean**

```bash
cd "$WIKI"
git reset --hard HEAD~1
git push --force origin HEAD
```

Expected: remote back to the original placeholder commit. (Force-push is safe here — the no-op was the only new commit and nobody else is editing the Wiki mid-migration.)

- [ ] **Step 4: No commit in the main repo for this task** (working copy only). Proceed.

---

### Task 2: Author the 7 Wiki content pages + `_Sidebar`

**Files (in `$WIKI`):**
- Create/overwrite: `Home.md`
- Create: `Getting-Started.md`, `Architecture.md`, `Security-and-Network.md`, `Integrations.md`, `Profiles-and-Agents.md`, `CLI-Reference.md`
- Create: `_Sidebar.md`

**Interfaces:**
- Consumes: `README.md` (unmodified at HEAD) as the verbatim content source; the security blurb from Global Constraints.
- Produces: 7 content pages + sidebar; internal links still in README-anchor form (rewritten in Task 3).

Use this helper to extract a verbatim section range (inclusive) from the repo README:

```bash
REPO="/Users/khundeck/working/klankrmkr"
ext() { sed -n "${1},${2}p" "$REPO/README.md"; }   # ext START END
```

- [ ] **Step 1: Build `Home.md`** (security blurb → What it is → Why → Further reading)

```bash
cd "$WIKI"
{
  echo "# Klanker Maker"
  echo
  echo '> **Built for security teams.** You'\''re on a security team responsible for hundreds of repos, and you need to move fast — triage, patch, review, and reason about vulnerabilities — without the investigation itself becoming the next breach. Klanker Maker gives you isolated, policy-governed sandboxes where untrusted code, dependencies, and AI agents run inside a contained blast radius.'
  echo '>'
  echo '> Isolation is the product. Every sandbox is **default-deny on the network**: an explicit allowlist controls which hosts it can reach, which secrets it can read, and how much it can spend. These are intentional design choices to make **data exfiltration** and **supply-chain compromise** hard by construction — a malicious dependency, a poisoned build step, or a compromised agent has nowhere to phone home and nothing ambient to steal. Patch fast, review at scale, and rationalize about vulns without trusting the thing you'\''re investigating.'
  echo
  ext 76 91     # What Klanker Maker Is
  echo
  ext 160 196   # Why This Exists
  echo
  echo "## Further reading"
  echo
  ext 924 941   # Documentation section body (skip the '## Documentation' header line 923)
  echo
  echo "Roadmap: see [ROADMAP.md](https://github.com/whereiskurt/klanker-maker/blob/main/ROADMAP.md) in the repo."
} > Home.md
```

Expected: `Home.md` written; `head -3 Home.md` shows the title + start of the blurb.

- [ ] **Step 2: Build `Getting-Started.md`**

```bash
cd "$WIKI"
{
  echo "# Getting Started"
  echo
  ext 118 159   # Quick Start (full)
  echo
  ext 673 685   # Built-in Profiles
  echo
  ext 686 699   # Substrates
} > Getting-Started.md
```

- [ ] **Step 3: Build `Architecture.md`**

```bash
cd "$WIKI"
{
  echo "# Architecture"
  echo
  ext 355 390   # AWS Account Architecture
  echo
  ext 872 922   # Architecture
  echo
  ext 215 246   # Cloud-Native Control Plane
} > Architecture.md
```

- [ ] **Step 4: Build `Security-and-Network.md`**

```bash
cd "$WIKI"
{
  echo "# Security & Network"
  echo
  ext 391 447   # Security Model
  echo
  ext 448 528   # Network Enforcement
  echo
  ext 529 574   # Budget Enforcement
} > Security-and-Network.md
```

- [ ] **Step 5: Build `Integrations.md`**

```bash
cd "$WIKI"
{
  echo "# Integrations"
  echo
  ext 247 294   # Slack-Native Operations
  echo
  ext 295 329   # GitHub App Integration
  echo
  ext 330 354   # Multi-Agent Orchestration via Signed Email
} > Integrations.md
```

- [ ] **Step 6: Build `Profiles-and-Agents.md`**

```bash
cd "$WIKI"
{
  echo "# Profiles & Agents"
  echo
  ext 575 672   # SandboxProfile
  echo
  ext 700 733   # Non-Interactive Agent Execution
  echo
  ext 734 759   # Scheduling and Recurring Operations
  echo
  ext 760 775   # AMI Lifecycle
} > Profiles-and-Agents.md
```

- [ ] **Step 7: Build `CLI-Reference.md`**

```bash
cd "$WIKI"
{
  echo "# CLI Reference"
  echo
  ext 777 871   # CLI Reference body (skip the '## CLI Reference' header line 776)
} > CLI-Reference.md
```

- [ ] **Step 8: Build `_Sidebar.md`** (nav)

```bash
cd "$WIKI"
cat > _Sidebar.md <<'EOF'
### Klanker Maker Wiki

- [Home](Home)
- [Getting Started](Getting-Started)
- [Architecture](Architecture)
- [Security & Network](Security-and-Network)
- [Integrations](Integrations)
- [Profiles & Agents](Profiles-and-Agents)
- [CLI Reference](CLI-Reference)

---
- [Repo](https://github.com/whereiskurt/klanker-maker)
- [Operator Guide](https://github.com/whereiskurt/klanker-maker/blob/main/OPERATOR-GUIDE.md)
EOF
```

- [ ] **Step 9: Verify every page has its top-level title and is non-trivial**

```bash
cd "$WIKI"
for f in Home Getting-Started Architecture Security-and-Network Integrations Profiles-and-Agents CLI-Reference; do
  printf '%-24s lines=%s firsthdr=%s\n' "$f" "$(wc -l < "$f.md")" "$(grep -m1 '^# ' "$f.md")"
done
```

Expected: each file >15 lines and prints a `# ` title. No file is empty.

- [ ] **Step 10: No main-repo commit** (Wiki commit happens in Task 4 after link rewrite). Proceed.

---

### Task 3: Rewrite internal cross-links and audit for dangling references

**Files (in `$WIKI`):** all 7 content pages.

**Interfaces:**
- Consumes: the page set from Task 2.
- Produces: pages whose intra-doc `](#anchor)` links resolve to Wiki pages; an audit showing zero unresolved in-page anchors.

The moved sections contain README-relative links of two kinds: (a) `](#some-heading)` anchors that pointed at other README sections now living on other Wiki pages, and (b) `](docs/...)` / `](OPERATOR-GUIDE.md)` repo-relative links that must become absolute GitHub URLs (Wiki pages are served from a different path).

- [ ] **Step 1: Inventory the links that need rewriting**

```bash
cd "$WIKI"
echo "=== in-page anchors (#...) ==="
grep -rnoE '\]\(#[a-z0-9-]+\)' *.md | sort -u
echo "=== repo-relative links (docs/, OPERATOR-GUIDE, *.md) ==="
grep -rnoE '\]\((docs/[^)]+|OPERATOR-GUIDE\.md|ROADMAP\.md|[A-Za-z0-9_./-]+\.md)[^)]*\)' *.md | sort -u
```

Expected: a finite list. Record it; each entry must be resolved in Step 2.

- [ ] **Step 2: Rewrite repo-relative links to absolute GitHub URLs**

For every `](docs/…)`, `](OPERATOR-GUIDE.md)`, `](ROADMAP.md)` found, rewrite the target to `https://github.com/whereiskurt/klanker-maker/blob/main/<path>`. Apply with targeted `sed` per distinct path, e.g.:

```bash
cd "$WIKI"
# Example pattern — repeat once per distinct repo-relative path discovered in Step 1:
sed -i '' 's#](docs/multi-agent-email.md)#](https://github.com/whereiskurt/klanker-maker/blob/main/docs/multi-agent-email.md)#g' *.md
sed -i '' 's#](OPERATOR-GUIDE.md)#](https://github.com/whereiskurt/klanker-maker/blob/main/OPERATOR-GUIDE.md)#g' *.md
```

(Do NOT blanket-rewrite `](#...)` anchors with these commands — those are handled in Step 3.)

- [ ] **Step 3: Resolve in-page `#anchor` links to the correct Wiki page**

For each `](#heading)` from Step 1, determine which Wiki page now owns that heading (use the Section→page map) and rewrite to `](<Page>#heading)` (same-page links stay as `](#heading)`). Example:

```bash
cd "$WIKI"
# Example — a link to the Security Model section from a page other than Security-and-Network.
# NOTE: the link text contains '#', so use a delimiter that is NOT '#' (here '@'):
sed -i '' 's@](#security-model)@](Security-and-Network#security-model)@g' *.md
# Same-page anchors (target heading lives on the same file) are left unchanged.
```

Resolve every entry from the Step 1 anchor list this way.

- [ ] **Step 4: Audit — no dangling in-page anchors remain**

```bash
cd "$WIKI"
echo "=== anchors still pointing at a bare #target ==="
# Build the set of heading slugs present per file and confirm every same-file #anchor exists.
fail=0
for f in *.md; do
  while IFS= read -r a; do
    slug="${a#*(#}"; slug="${slug%)}"
    if ! grep -qiE "^#+ .*" "$f" || ! grep -qE "^#+ " "$f"; then :; fi
    # confirm the slug exists as a heading anchor somewhere in the wiki
    if ! grep -rhE '^#+ ' *.md | sed -E 's/^#+ //; s/[^a-zA-Z0-9 -]//g; s/ /-/g' | tr '[:upper:]' '[:lower:]' | grep -qx "$slug"; then
      echo "DANGLING in $f -> #$slug"; fail=1
    fi
  done < <(grep -oE '\]\(#[a-z0-9-]+\)' "$f")
done
[ "$fail" -eq 0 ] && echo "OK: no dangling same-doc anchors"
```

Expected: `OK: no dangling same-doc anchors`. If any `DANGLING` line prints, fix it (the target heading moved to another page → prefix with that page name) and re-run.

- [ ] **Step 5: Confirm no remaining bare repo-relative `.md` links leaked through**

```bash
cd "$WIKI"
grep -rnE '\]\((docs/|OPERATOR-GUIDE\.md|ROADMAP\.md)' *.md && echo "LEAK — rewrite these" || echo "OK: no repo-relative leaks"
```

Expected: `OK: no repo-relative leaks`.

---

### Task 4: Publish the Wiki

**Files (in `$WIKI`):** all pages.

**Interfaces:**
- Consumes: finalized pages from Tasks 2–3.
- Produces: pushed Wiki — pages live at `github.com/whereiskurt/klanker-maker/wiki`.

- [ ] **Step 1: Stage and review the diff**

```bash
cd "$WIKI"
git add -A
git status
git diff --cached --stat
```

Expected: 8 files added/modified (`Home.md` modified, 6 new pages, `_Sidebar.md` new).

- [ ] **Step 2: Commit**

```bash
cd "$WIKI"
git commit -m "docs: migrate full reference manual from README into themed wiki pages"
```

- [ ] **Step 3: Push**

```bash
cd "$WIKI"
git push origin HEAD
```

Expected: push succeeds.

- [ ] **Step 4: Verify the live render of the Home page**

```bash
curl -sSL -o /dev/null -w '%{http_code}\n' https://raw.githubusercontent.com/wiki/whereiskurt/klanker-maker/Home.md
```

Expected: `200`, and the fetched content begins with `# Klanker Maker` + the security blurb. (If 404, the push didn't land — recheck Step 3.)

---

### Task 5: Slim `README.md`, verify release-bundling intact, and commit

**Files:**
- Modify: `/Users/khundeck/working/klankrmkr/README.md` (on branch `docs/readme-to-wiki`)

**Interfaces:**
- Consumes: the published Wiki (for the docs link).
- Produces: a ~80–120 line landing-page README; `.goreleaser.yaml` still references `README.md` (unchanged).

The lean README keeps the repo's existing head (title, tagline, diagram, profile-contract example) and a small set of sections; everything else is gone (now in the Wiki). Target order:

1. Title `# Klanker Maker (km)` + bold tagline (current lines 1–3)
2. **Security blurb** (NEW — verbatim from Global Constraints)
3. Diagram + profile-contract example (current lines 7–43)
4. **📖 Full documentation → Wiki** callout (NEW)
5. `## What Klanker Maker Is` (current 76–91)
6. `## How It Compares` (current 92–117)
7. `## Quick Start` — **trimmed** to install + create + destroy (~12 lines), ending with "Full guide → Wiki Getting-Started"
8. `## Core Capabilities` (current 197–214)
9. `## License & Project Status` (current 973–995)

- [ ] **Step 1: Snapshot current README length (baseline for the shrink check)**

```bash
cd /Users/khundeck/working/klankrmkr
wc -l README.md   # expect 995
```

- [ ] **Step 2: Assemble the lean README**

```bash
cd /Users/khundeck/working/klankrmkr
ext() { sed -n "${1},${2}p" README.md.orig; }
cp README.md README.md.orig
{
  ext 1 5            # title + tagline + compiles-paragraph
  echo
  # --- security blurb (NEW) ---
  echo '> **Built for security teams.** You'\''re on a security team responsible for hundreds of repos, and you need to move fast — triage, patch, review, and reason about vulnerabilities — without the investigation itself becoming the next breach. Klanker Maker gives you isolated, policy-governed sandboxes where untrusted code, dependencies, and AI agents run inside a contained blast radius.'
  echo '>'
  echo '> Isolation is the product. Every sandbox is **default-deny on the network**: an explicit allowlist controls which hosts it can reach, which secrets it can read, and how much it can spend. These are intentional design choices to make **data exfiltration** and **supply-chain compromise** hard by construction — a malicious dependency, a poisoned build step, or a compromised agent has nowhere to phone home and nothing ambient to steal. Patch fast, review at scale, and rationalize about vulns without trusting the thing you'\''re investigating.'
  echo
  ext 7 43           # diagram + profile-contract intro + yaml + bash example
  echo
  echo '> 📖 **Full documentation lives in the [Klanker Maker Wiki](https://github.com/whereiskurt/klanker-maker/wiki)** — architecture, security model, network enforcement, Slack/GitHub/email integrations, the SandboxProfile reference, and the full CLI reference.'
  echo
  echo '---'
  echo
  ext 76 91          # ## What Klanker Maker Is
  echo
  ext 92 117         # ## How It Compares
  echo
  # --- trimmed Quick Start (NEW, replaces full 118-159) ---
  cat <<'QS'
## Quick Start

```bash
# Build the CLI
make build

# Create a sandbox from a profile, run an agent, tear it down
./km create profiles/g1.yaml
./km agent run g1 --prompt "investigate the OOM in api-server" --wait
./km destroy g1 --yes
```

Full setup (AWS bootstrap, `km init`, Slack/GitHub wiring) → **[Getting Started](https://github.com/whereiskurt/klanker-maker/wiki/Getting-Started)** in the Wiki.
QS
  echo
  ext 197 214        # ## Core Capabilities
  echo
  ext 973 995        # ## License & Project Status
} > README.md
rm -f README.md.orig
```

- [ ] **Step 3: Verify the README shrank into target range and has the new elements**

```bash
cd /Users/khundeck/working/klankrmkr
n=$(wc -l < README.md); echo "lines=$n"
[ "$n" -lt 160 ] && echo "OK: shrank" || echo "TOO LONG — trim further"
grep -q "Built for security teams" README.md && echo "OK: blurb present" || echo "MISSING blurb"
grep -q "klanker-maker/wiki" README.md && echo "OK: wiki link present" || echo "MISSING wiki link"
grep -c '^## ' README.md   # expect ~5 sections
```

Expected: `lines` well under 160 (~100), blurb present, wiki link present, ~5 `##` sections.

- [ ] **Step 4: Verify the release bundle still references README (regression guard)**

```bash
cd /Users/khundeck/working/klankrmkr
grep -n 'README.md' .goreleaser.yaml && echo "OK: still bundled" || echo "BROKEN: README no longer in release"
test -f README.md && echo "OK: README exists at root"
```

Expected: `.goreleaser.yaml` still lists `README.md`; file exists. (We did not touch `.goreleaser.yaml` — this just confirms.)

- [ ] **Step 5: Sanity-check there are no dangling in-README anchors after the cut**

```bash
cd /Users/khundeck/working/klankrmkr
for a in $(grep -oE '\]\(#[a-z0-9-]+\)' README.md | sed -E 's/\]\(#//; s/\)//'); do
  grep -qiE "^#+ " README.md && \
  ( grep -rhE '^#+ ' README.md | sed -E 's/^#+ //; s/[^a-zA-Z0-9 -]//g; s/ /-/g' | tr '[:upper:]' '[:lower:]' | grep -qx "$a" \
    || echo "DANGLING README anchor: #$a" )
done; echo "anchor check done"
```

Expected: no `DANGLING` lines (the dropped TOC was the main anchor source; if any leftover anchor points at a removed section, delete or repoint that link).

- [ ] **Step 6: Commit on the branch**

```bash
cd /Users/khundeck/working/klankrmkr
git add README.md
git commit -m "docs: slim README to a lean landing page; full manual now in the Wiki

Security-focused lead blurb up top; deep reference (architecture, security
model, network enforcement, integrations, SandboxProfile, CLI reference)
relocated to the GitHub Wiki. README.md retained for goreleaser bundling.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Expected: commit on `docs/readme-to-wiki`. Do NOT push/merge unless the user asks.

---

## Notes for the executor

- **Order matters:** Tasks 2–3 read `README.md` at its full 995-line state. Do NOT run Task 5 (which rewrites README) until the Wiki is published in Task 4, or the extraction ranges in Task 2 will be invalid.
- **Line ranges are a snapshot.** If `git status` shows README already changed, re-run `grep -n '^## ' README.md` and recompute ranges before extracting.
- **Direct-push was the user's explicit choice (option A).** If any Wiki push fails on auth, stop and report — do not stage-and-hand-off without asking.
