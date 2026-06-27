# klankermaker.ai Setup Wizard — Design (Sub-project A)

**Date:** 2026-06-27
**Status:** Design — approved shape, pending spec review
**Author:** brainstormed with operator

## 1. Context & goal

Help newcomers on the internet **try klanker-maker**: land on a public page, answer
an interview, and walk away with everything needed to stand up a real install — the
AWS access config, a prefilled `km-config.yaml`, and an ordered, readable runbook of
`km` commands. The wizard is the project's **onboarding front door**, hosted at
`klankermaker.ai`.

This is **sub-project A** of a larger "self-setup from zero" milestone. The milestone
decomposes into four separable subsystems with very different risk/testing profiles:

| # | Sub-project | Risk | This doc |
|---|---|---|---|
| **A** | **Web wizard** (klankermaker.ai): interview → config + runbook bundle | low (pure rendering) | ✅ specced here |
| B | `km quota request` + quota-status gate (ServiceQuotas API) | low (idempotent, mockable) | placeholder only |
| C | `km account` lifecycle: vend → zero → graduate → reap (Organizations) | **high** (destructive, slow, real money) | placeholder only |
| D | Resume / orchestration model (progress state, `km setup status`) | medium | contract only |

A is built first because its emitted **runbook is the contract** B, C, and D must satisfy.

## 2. Non-goals (YAGNI)

- A **does not execute anything**, hold credentials, or call AWS. It is interview-and-emit.
- A **does not build** `km account vend` (C) or `km quota request` (B). Where the user's
  answers call for those, A emits a clearly-marked **forward-compatible placeholder**
  (console instructions today; the real command swaps in when B/C ship).
- A **does not implement** the rich resume state machine (D). It defines only the
  gate-segmented artifact contract and relies on per-part idempotency + `km doctor`.
- A does **not** require account creation. The newcomer brings their account(s) /
  account IDs. (Creation is C, a later sub-project.)

## 3. Core principles

- **Interview-and-emit, client-side only.** No backend, no creds in the browser,
  nothing transmitted. The generated bundle is built in-browser and downloaded. The
  privacy claim ("everything stays in your browser; here is the source") is a feature,
  and it is only honestly true because of this principle.
- **Real `km` is the arbiter of correctness.** The wizard never re-derives km's truth;
  a CI contract test feeds representative wizard outputs through real `km` so drift fails
  the build.
- **Reviewability over convenience.** The emitted artifact is a *readable* script you
  open before running — never a `curl … | bash` opaque pipe. For a public site about to
  configure someone's AWS account with broad creds, reviewability is the trust story.

## 4. Architecture (approach #1: declarative question-graph + CI contract test)

The interview logic already exists once, inside `km configure` (Go), and km's config
schema changes almost every phase. Reimplementing the interview in JS would create a
**second brain that drifts**. Approach #1 avoids that:

- The interview is authored **as data** — a declarative **question-graph** (questions,
  help text, branch conditions, the `km-config` key each answer maps to, validation).
- A generic, pretty static **renderer** walks the graph and, on completion, runs a pure
  **emitter** that produces the downloadable bundle.
- A **CI contract test in the km repo** pipes representative emitted outputs (one per
  path/preset) through real `km validate` / a `km configure --check`, so any schema drift
  **fails the build**. Real km judges the *output*; the question-graph is just data.

> **North star (deferred, approach #2):** later, `km configure`'s TUI could be refactored
> to be driven by the *same* question-graph, making it the single source of truth for the
> *interview itself* (not just the output). The hand-authored graph in A is deliberately
> the seed that #2 would promote. Out of scope for A.

### 4.1 The two layers

The graph is organized into two layers so the wizard does not balloon into a giant
feature-matrix as km grows:

- **Layer 1 — gated foundation (the spine).** Accounts, auth (IAM/SSO), Org/SCP, email
  (domain + DNS), quota. This is what the segmented-script gate structure is *about* —
  the must-be-sequenced, sometimes-must-wait work. Routing questions drive it.
- **Layer 2 — declarative capability catalog (the toppings).** Slack, GitHub, H1, checks,
  desktop, GPU-serving. Each is a self-contained **capability descriptor** (data): a few
  questions, the `km` commands it emits, and any *fast* in-console step. They append to
  the **final** part because almost none touch DNS.

**Key invariant:** only **two** capabilities inject a multi-day gate — **email (DNS)** and
**GPU model-serving (quota)**. Every other capability is domain-free and fast. This is
exactly why the fast path is fast.

Adding a future km capability = adding **one descriptor to the graph data** — no wizard
redesign — and the CI contract test validates its emitted config against real km.

## 5. Components

1. **Question-graph schema** — the data model: foundation routing nodes + capability
   descriptors + preset definitions. Versioned; the contract test pins it to a km version.
2. **Renderer (static site)** — reads the graph, drives the branching UI, validates input
   client-side, generates the bundle in-browser. Dependency-light; hostable on
   S3 + CloudFront.
3. **Emitter (pure function)** — `answers → bundle`. Deterministic; snapshot-testable.
4. **Capability descriptor format** — the Layer-2 plug-in shape.
5. **CI contract harness** — in the km repo; runs emitted outputs through real `km`.

## 6. The emitted bundle

A small **downloadable bundle** (not a single self-executing megascript — see §7 on gates):

- `km-config.yaml` — prefilled from answers.
- `aws-config.snippet` — the `~/.aws/config` `klanker-*` profile block(s) (see §8).
- `klanker-setup-N-<name>.sh` — **one script part per human-gated segment** of the path.
  `N − 1` = number of gates on that path.
- `README` / `INDEX` — lists the parts in order and the gate that sits between each, plus
  how to obtain `km`.

Each script part:
- is **readable and commented**;
- is **idempotent / safe to re-run** (each stage checks "already done?" and resumes);
- **Step 0 of part 1** fetches + **checksum-verifies** a **pinned** km release (the version
  the wizard targets — reproducible, not a moving "latest");
- writes/uses the config via **visible heredocs**;
- runs the relevant `km` commands;
- ends at its gate with explicit `✋ STOP: do X, then run part N+1`, leaning on
  `km doctor`-style readiness checks at the gate.

## 7. Gates & segmentation

The early **routing questions select a path**; the path determines the **gates**; the
bundle has **one part per gate-bounded segment**. A fully-ready user gets a single part;
true-zero full-topology gets ~3–4.

Canonical gates, in order, for the full-topology-from-zero path:

| Gate | Human action | km command after | Slow? |
|---|---|---|---|
| **0 · Auth** (prereq) | `aws sso login` or land IAM keys | — (part 1 won't start) | minutes |
| **1 · Accounts** | only if no accounts / Org vend wanted | `km account vend` *(C placeholder)* | minutes |
| **2 · Quota** | request `L-DB2E81BA` etc., wait for grant | — (poll `km doctor`) | **hours–days** |
| **3 · DNS** | delegate zone, wait for propagation + SES validation | — (poll `km doctor`) | **hours–days** |
| *(final)* | nothing | `km bootstrap` → `km init` → `km create` | — |

The artifact's segmentation **is** the seam where A meets D. A defines the contract
(segmented parts + `✋` pause markers + re-run idempotency); D later adds a richer progress
model (`km setup status`, a progress file).

## 8. AWS auth config emission

The wizard branches on **IAM vs SSO** and emits the matching `~/.aws/config`:

- **SSO path** — an `[sso-session …]` block + `[profile klanker-…]` blocks
  (`sso_account_id`, `sso_role_name`, `sso_region`, `region`) + the `aws sso login` step.
- **IAM path** — `[profile klanker-…]` blocks + instructions to land access keys
  (`aws configure --profile …` or `~/.aws/credentials`). **Optionally** emits a
  **scoped operator policy JSON** as an alternative to bare `AdministratorAccess`
  (least-privilege operator seat — see the operator-policy discussion).
- **Profiles emitted** scale with topology: **full** topology → three profiles
  (`klanker-management` / `klanker-application` / `klanker-terraform`); **minimal** →
  just `klanker-application`. Matches `configure.go` treating the management account ID
  as optional (blank = skip SCP).

## 9. Presets (seed set)

Presets are named `{foundation routing + capability set}` bundles over the same graph:

| Preset | Foundation | Capabilities | Gates | ~Parts |
|---|---|---|---|---|
| **⚡ Quick start** | existing account · **no email/domain** | {Slack, GitHub, H1, checks} as desired | none slow | **1–2** |
| **🏛 Full** | email + Org/multi-account | everything | DNS (+ quota if GPU) | 3–4 |
| **🛠 Custom** | pick foundation à la carte | check the catalog | depends | depends |

⚡ Quick start = **both slow capabilities off**, any subset of fast ones on.

## 10. Capability catalog (seed: 7)

| Capability | Slow gate? | Fast in-console step | Emits |
|---|---|---|---|
| **Email** | ✋ DNS (days) | — | SES/Route53 + `km bootstrap --shared-ses` |
| **GPU model-serving** | ✋ G-quota (hrs–days) | — | gpu profile + `km create` |
| **Slack** | none | create App, paste manifest | `km slack init` |
| **GitHub** | none | `km github manifest`, install App | `km github init` |
| **H1** | none | mint webhook secret | `km h1 init` |
| **Checks** | none | — | `km check deploy <starter set>` |
| **Desktop / VS Code** | none | — | `runtime.desktop` / `vscode` profile fields |

The catalog is open by design; new km phases add descriptors without wizard changes.

## 11. Error handling & edge cases

- **Client-side validation** at input time: account-ID format (12 digits), region,
  domain syntax, `resource_prefix` charset, SSO start-URL format.
- **Drift** is caught by the CI contract test (build fails), not by runtime surprise.
- **Reproducibility**: the emitted script pins the km version + verifies its checksum.
- **Trust**: nothing is transmitted; the page is static and source-visible; the artifact
  is downloaded and readable before execution.
- **Partial/abandoned interview**: no server state to leak; closing the tab loses nothing
  sensitive (it was never sent anywhere).

## 12. Testing strategy

- **Unit (pure):** question-graph traversal + emitter are pure functions → **snapshot the
  emitted bundle per path/preset** (golden bundles). Fast, deterministic, no AWS.
- **Contract (km repo CI):** feed each preset's emitted `km-config.yaml` through real
  `km validate` / `km configure --check`; a `km`-schema change that the graph hasn't
  caught up to **fails CI**. This is the anti-drift guarantee.
- **Renderer smoke:** a headless pass that walks each preset end-to-end and asserts a
  bundle is produced with the expected file set.

## 13. Tech stack (recommendation, not binding)

Dependency-light static site (the graph is data, so the framework is thin — vanilla or a
small framework like Svelte/Astro). Hostable on S3 + CloudFront (fits the project's own
AWS footprint). "Looks awesome" is a frontend-design concern handled at build time; the
architecture here is deliberately framework-agnostic.

## 14. Relationship to B / C / D

- **A first.** Its runbook/bundle format is the **contract** for the later sub-projects.
- **B** swaps the Quota placeholder for `km quota request` + a `km doctor` quota gate.
- **C** swaps the Accounts placeholder for `km account vend` (and later zero/graduate/reap).
- **D** enriches the re-run idempotency into a real progress/resume model.

## 15. Deferred / open

- Static-site framework choice (implementation detail).
- Approach #2 (sharing the graph with `km configure`'s TUI) — north star, not now.
- Exact `km configure --check` flag surface for the contract test (small km-side add;
  confirm during planning whether it already exists or needs a thin wrapper).
