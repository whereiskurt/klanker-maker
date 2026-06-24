# Move deep docs to the GitHub Wiki, leave a lean README

**Date:** 2026-06-24
**Status:** Approved (design), pending spec review

## Goal

Declutter the repo home page. `README.md` is currently 995 lines / 76 headings —
a full manual. Move the deep reference content into the GitHub Wiki and leave a
lean landing-page README behind.

This is a **split, not a delete**: a `README.md` file must remain at the repo
root because `.goreleaser.yaml:53` bundles it into every release tarball.
Deleting it would break the release build.

## Constraints (ground truth)

- `README.md` is a release artifact — `.goreleaser.yaml:53` lists `README.md` in
  the bundled files. A root `README.md` must continue to exist.
- The Wiki is a **separate git repo** (`klanker-maker.wiki.git`), not part of
  this repo's history or PRs. It only becomes clonable once at least one page has
  been created (the user reports the Wiki is enabled).
- `OPERATOR-GUIDE.md` (1707 lines) is a separate file, also bundled in releases.
  It is NOT moved — Wiki pages link to it.

## Part 1 — Lean README.md (stays at repo root)

Target ~80–120 lines. Keep:

- **Security-focused lead blurb** (new — see below). This opens the README and
  the Wiki `Home` page.
- One-paragraph pitch ("What Klanker Maker Is")
- "How It Compares" table (the hook)
- Quick Start (install + first sandbox) — a short version
- Core Capabilities bullet list
- A prominent **📖 Full documentation → Wiki** link near the top
- License & Project Status

Everything else moves to the Wiki.

### Security-focused lead blurb (draft copy)

> **Built for security teams.** You're on a security team responsible for
> hundreds of repos, and you need to move fast — triage, patch, review, and
> reason about vulnerabilities — without the investigation itself becoming the
> next breach. Klanker Maker gives you isolated, policy-governed sandboxes where
> untrusted code, dependencies, and AI agents run inside a contained blast
> radius.
>
> Isolation is the product. Every sandbox is **default-deny on the network**:
> an explicit allowlist controls which hosts it can reach, which secrets it can
> read, and how much it can spend. These are intentional design choices to make
> **data exfiltration** and **supply-chain compromise** hard by construction —
> a malicious dependency, a poisoned build step, or a compromised agent has
> nowhere to phone home and nothing ambient to steal. Patch fast, review at
> scale, and rationalize about vulns without trusting the thing you're
> investigating.

This is the positioning paragraph, not a feature list — the existing "Security
Model", "Network Enforcement", and "Budget Enforcement" sections (moving to the
Wiki `Security-and-Network` page) remain the detailed reference.

## Part 2 — Wiki pages (`klanker-maker.wiki.git`)

7 content pages + a sidebar. Section groupings (from the README's 24 `##`
sections):

| Wiki page | README sections it absorbs |
|---|---|
| `Home` | index + What Klanker Maker Is, Why This Exists |
| `Getting-Started` | Quick Start, Built-in Profiles, Substrates |
| `Architecture` | AWS Account Architecture, Architecture, Cloud-Native Control Plane |
| `Security-and-Network` | Security Model, Network Enforcement, Budget Enforcement |
| `Integrations` | Slack-Native Operations, GitHub App Integration, Multi-Agent Orchestration via Signed Email |
| `Profiles-and-Agents` | SandboxProfile, Non-Interactive Agent Execution, Scheduling and Recurring Operations, AMI Lifecycle |
| `CLI-Reference` | CLI Reference |
| `_Sidebar` | nav linking all pages |

(README's "Core Capabilities" and "How It Compares" stay in the lean README;
"Table of Contents", "Documentation", and "Roadmap" sections are replaced — see
Content handling.)

## Part 3 — Content handling

- Content moves **verbatim** — same prose, re-homed. No rewriting of the
  substance.
- Internal `#anchor` links are rewritten to point at the right Wiki page (or a
  `Page#heading` anchor where the target moved to another Wiki page).
- The README's "Table of Contents" is dropped (the `_Sidebar` replaces it).
- The README's "Documentation" section becomes a Wiki page that links out to the
  in-repo `docs/` files and `OPERATOR-GUIDE.md`; the "Roadmap" section becomes a
  pointer to in-repo `ROADMAP.md`.
- `OPERATOR-GUIDE.md` is untouched; Wiki pages link to it in the repo.

## Part 4 — Publishing

- Clone `https://github.com/whereiskurt/klanker-maker.wiki.git` into the
  scratchpad. **Verify the clone succeeds first** — if the Wiki repo is empty /
  uninitialized, stop and report rather than guessing.
- Write the 8 files, commit, push to the Wiki repo.
- The lean-README change is committed on a **branch** in the main repo
  (repo convention: branch off `main`, do not push/merge unless asked).

## Known trade-offs (accepted)

- The lean README's short Quick Start duplicates the fuller Quick Start in the
  Wiki `Getting-Started` page. Intentional and standard for a landing page.
- Wiki content is not covered by the main repo's PR review / history.

## Out of scope

- Moving or restructuring `OPERATOR-GUIDE.md` or `docs/`.
- Any change to release tooling beyond keeping `README.md` present.
- Editing the substance of the documentation prose.
