# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in `km-config.yaml` (default `km`). `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN`). See `OPERATOR-GUIDE.md` § Multi-instance support and the `klanker:init` skill.

**Phase 134 (2026-09-04) — `km herdr`: persistent remote attach for sandbox agent panes (code-complete; live UAT pending):**
- `km herdr start|status <sandbox-id>` — a **sibling of `km vscode`, not a
  `km tunnel` mode.** `km tunnel` carries a network path from the workstation
  *into* the sandbox, and its security note (km's egress enforcement can't see
  traffic crossing the tunnel) follows from that direction; `km herdr` points
  the other way — the operator reaches *in* to a box already theirs, over the
  same SSM+SSH transport `km vscode` already terminates. Filing it under
  `tunnel` would attach a caveat that is false here. Reuses `km vscode`'s
  transport wholesale (`connectPrep`, `upsertSandboxHost`,
  `runReconnectingPortForward`) and differs only in pre-flight (adds a herdr
  binary/version check to the SSM round trip) and banner. Default
  `--local-port 2224` — `2222` is `km vscode`, `2223` is both `km tunnel`
  modes.
- **`profiles/base/tools/herdr.yaml`** — opt-in fragment, pure
  `initCommandsAppend`, pulling the binary from S3 over the instance role. Its
  real purpose is narrower than it looks, and **narrower still since #106**:
  `profiles/base/userinit.yaml` already installs the same pinned 0.8.2 on any
  profile that extends it, to `/home/sandbox/.local/bin/herdr` (sandbox-owned,
  not on root's `PATH`), so this fragment is redundant there. Before #106 that
  install was `curl herdr.dev/install.sh | sh`, and the fragment's stated
  justification was `base/network/locked`-style profiles that cannot reach
  `herdr.dev` — **that justification is now false.** #106 repointed userinit at
  a SHA-pinned `github.com` release asset, and `base/network/locked` allows
  `.github.com`/`.githubusercontent.com`; a leading-dot entry matches the apex
  as well as subdomains (`IsHostAllowed`, and the DNS matcher agrees), so
  userinit's fetch succeeds under lock. What actually remains is a profile that
  does **not** extend `base/userinit` — which has no herdr at all — or one whose
  egress is narrower than `locked`. For those, S3 needs no allowlist entry at
  all, unlike `base/security/wiz.yaml`'s `.wiz.io` suffix.
- **A real bug found AFTER this fragment first shipped, live-tested and
  fixed same-day:** the original `initCommandsAppend` line referenced
  `${KM_ARTIFACTS_BUCKET}` bare, on the assumption that the bootstrap exports
  it and `/tmp/km-init.sh` inherits it as a child process. **Proven false on a
  real sandbox** — `/tmp/km-init.sh` is generated with a plain `#!/bin/bash`
  shebang and run as `/tmp/km-init.sh` (a child process, not sourced), and
  `KM_ARTIFACTS_BUCKET` is `export`ed exactly once anywhere in the userdata
  template, inside the heredoc writing `/etc/profile.d/km-identity.sh` for
  future *login* shells only — never into the boot script's own environment.
  The bare form measurably expanded to `s3:///binaries/herdr` and failed. Fixed
  by sourcing `km-identity.sh` first when the variable is unset — the same
  trap `km herdr start`'s SSM-run ensure-install hit independently, since SSM
  is equally a non-login shell.
- **The deploy story is "no template or schema change," NOT "no rendered-output
  change" — getting that distinction wrong once is why it's stated here
  precisely.** Adding an `initCommandsAppend` line *does* alter one thing in
  rendered userdata: Phase 113's `/opt/km/.km-profile.yaml` dump is a
  `yaml.Marshal` of the whole resolved profile — `InitCommands` included — by
  reflection, so no template token names it and grepping the template for it
  finds nothing. The delta stays confined to that one dump and never reaches
  executable bootstrap logic, which is what actually licenses "no
  create-handler rebuild" — a claim about the *template* and the *schema*
  being unchanged, not about the bytes being identical.
- **`km-presence` signal 8** — a detached Herdr pane doing real work now keeps
  the sandbox awake; a detached, quiet one is still correctly reaped. **It
  detects the WORK, never the server**: `herdr server stop` is the only thing
  that ends a server, so "a server process exists" would latch a box awake
  forever, the identical trap `pgrep vscode-server` would be for VS Code —
  which is precisely why there's no herdr systemd unit either (see the
  fragment's own comments). The discriminator, found only by capturing a real
  idle pane rather than reading Herdr's (wrong) published CLI docs:
  `foreground_process_group_id != shell_pid`. A naive
  `len(foreground_processes) > 0` check is **always true** — the pane's own
  shell always appears in that list — and would have silently disabled idle
  teardown fleet-wide; caught before shipping by
  `TestHerdrPaneIsBusy_TrapIdleShellIsNotBusy` and
  `TestSignal_HerdrPaneBusy_NegativeAllIdle`, the load-bearing negative cases.
  Agent-agnostic by construction: signal 5's `pgrep` name allowlist costs an
  edit and a fleet recreate per new agent, signal 8 costs neither.
  `runuser -u sandbox -- bash -lc '...'` (not `sudo`/`su`, matching Phase 132's
  15 dispatch sites) is mandatory, not stylistic — herdr lives on the sandbox
  user's `PATH`, not root's, so a non-login `runuser` alone would fail idle
  *permanently*, and a permanently-false signal passes its own negative-case
  test trivially.
- **The lifecycle trap this whole phase exists to close:** Herdr sessions do
  not survive a reboot. `km pause` (hibernate) preserves RAM, so panes and
  their processes survive; `km stop` kills every pane's process outright. That
  makes `teardownPolicy: stop` on an idle timeout the destructive path — an
  idle reaper that used to just cost a `km resume` now costs the running work
  too, silently, unless signal 8 is what's actually holding the box awake.
- **`km doctor` warns when Herdr and km both manage `~/.ssh/config`** — Herdr
  rewrites it by default (keepalive fallbacks), km's `UpsertHost` owns the
  `Host km-<id>` block, two writers one file. **km deliberately does not write
  the fix itself** (`manage_ssh_config = false` in Herdr's own
  `config.toml`) — that file is workstation-global and km does not own it;
  silently editing it to win a conflict is how you get a bug report about km
  breaking an unrelated remote host. Comment-aware: a commented-out
  `# manage_ssh_config = false` must not read as opted-out.
- **`pane_history` stays off.** Off by default upstream; km's fragment does
  not turn it on. It persists pane scrollback — secrets, tokens, prompts
  included — to disk on the box for restart convenience, which is the wrong
  trade for km to make on an operator's behalf.
- **Deploy = `make build` + `km init --sidecars`.** **NOT**
  `km init --dry-run=false` — no Terraform module, IAM policy, DynamoDB table,
  Lambda, or userdata *template* change (see the dump-vs-template distinction
  above). `km init --sidecars` **exits non-zero in a worktree that has never
  run `make build-lambdas`** (its last step tars `build/*.zip`, absent in a
  fresh worktree) but only AFTER every sidecar including `binaries/herdr` has
  already uploaded and the prior `infra.tar.gz` is left intact — verified
  live; `grep 'Uploaded herdr'` in the output is the check that matters, not
  the exit code. `km herdr start|status` and the S3 mirror reach EXISTING
  sandboxes immediately (ensure-install over SSM); the fragment reaches new
  sandboxes only; **signal 8 needs `km destroy && km create`** — `km-presence`
  is fetched at boot. `km herdr status` names which situation a given sandbox
  is in.
- See `docs/herdr-remote-attach.md` for the full operator runbook and
  `docs/superpowers/specs/2026-09-04-herdr-remote-attach-design.md` for the
  design.

**Phase 133 (2026-09-04) — Brokered secret unsealing: secrets leave the shell (Wave 1 of 2; complete, live-UAT'd 31/31):**
- **The problem: malware dumping `env` collected every SOPS secret on the box.**
  `spec.secrets.sopsFile` decrypted at boot into `/etc/sandbox-secrets.env`
  (`0440 root:sandbox`), auto-exported by `/etc/profile.d/zz-sandbox-secrets.sh`
  into every login shell. Agent turns dispatch through `runuser -u sandbox --
  bash -lc` — a login shell — so the whole bundle sat in the agent's environment,
  in every `km shell`, and in the environ of anything either one ever forked.
  `cat /proc/*/environ`, or just reading the file, yielded everything.
- **The finding that shaped the whole design: the instance role permanently
  holds BOTH halves of the decrypt** — `kms:Decrypt` on the sandbox-secrets
  KMS alias and `s3:GetObject` on the ciphertext object. Moving decryption from
  boot-time to call-time removes the *dump*; it does not remove the
  *authority* — anything that can reach IMDS can still re-derive the bundle
  itself. AWS has no notion of which uid on an instance is calling it, so a
  uid-granular fence can only exist at the OS/network layer. That fence
  (`spec.secrets.fenceIMDS`, `km-creds`, a self-assuming `ec2spot/v1.7.0`) is
  the harder half of the problem and is **Wave 2, not shipped in this wave.**
- **Decrypt-per-request, `[]byte` not `string`, zeroed after — with an honest
  limit stated in code and docs, not glossed over.** `km-secretsd` (new root
  daemon, `/run/km/secrets.sock`, `0660 root:sandbox`, `SO_PEERCRED`) performs
  a live `kms:Decrypt` on every unseal and zeroes its buffers immediately
  after writing the response. Values are `[]byte` everywhere the broker
  controls them — Go strings are immutable and runtime-copyable, so a
  `map[string]string` could never actually be zeroed and the claim would be
  decorative. The zeroing does NOT reach the YAML decoder's own intermediate
  string allocations, nor `json.Encoder`'s internal buffer (which holds a
  base64 copy of every value while writing the wire response) — both are GC
  garbage the broker's code cannot touch. Cost accepted: ~10ms/unseal, and a
  network partition makes secrets unavailable — loud (`km-env: cannot reach
  the secrets broker`), not silent.
- **Shims make `km-env` innermost; wrapping `dispatch_as_sandbox` was
  rejected.** `claude`/`codex` (or any `grants`-named consumer) are
  intercepted by root-owned `/opt/km/shims/{name}` (first on PATH), which
  `exec`s `/opt/km/bin/km-env exec --as <name> -- <real binary>`. The
  rejected alternative — auto-unsealing at the `dispatch_as_sandbox`
  chokepoint itself — was one template edit and no shims, but it would hand
  the WHOLE turn's `bash -lc` the bundle, so anything the agent shells out to
  (including a tool it was tricked into running) inherits it: a smaller
  version of the exact problem being fixed, not a fix. `km-env` has
  deliberately no export/eval form — `km-env exec` `execve`s one command and
  is gone; `km-env list` prints key names only, never values — pinned by
  `TestNoShellExportVerbExists`, which scans every non-test `.go` file in
  `cmd/km-env/` (not just `main.go`) for a banned-token list; it is a lexical
  grep guard, honestly documented as unable to catch a verb built from a
  constant or matched by regex.
- **The nvm PATH race, and the one assertion that actually proves interception
  is live.** nvm's own `profile.d` script prepends `~/.nvm/versions/node/*/bin`
  to PATH, and the tmux dispatch site uses non-login `bash -c` that never
  sources `profile.d` at all — either would leave a shim silently inert, the
  same "dead under the default config, invisible to string-presence tests"
  shape Phases 131/132 already hit. Fixed by making all FIVE
  `dispatch_as_sandbox` definitions (four SQS pollers + the tmux twin) set
  `PATH=/opt/km/shims:$PATH` explicitly rather than trusting profile.d
  ordering, guarded by `pkg/secrets/wiring_guard_test.go` (name-agnostic —
  covers a sixth poller added later without an edit). The boot selftest's
  assertion 5 — `runuser -u sandbox -- bash -lc 'command -v claude'` must
  resolve to the shim, not the real binary — is the single highest-value
  check in the whole design: every OTHER selftest failure is loud, but a lost
  PATH race boots clean, the daemon runs, the shim exists, and `claude` just
  runs with no secrets and dies on a confusing 401 with nothing else noticing.
- **Boot-fatal, resume-red: the same check, two dispositions, for a structural
  reason.** `km-secretsd selftest` runs at boot as a plain userdata command
  under `set -euo pipefail` (non-zero aborts the boot, same disposition as the
  Phase 89 `sops decrypt` FATAL it replaces) AND on every resume via
  `km-secrets-check.service` (`Type=oneshot`). Userdata never re-runs on
  stop/start — cloud-init is per-instance — so a resumed box can meet a
  rotated bundle or a revoked grant with no boot left to abort; the unit just
  fails red and a `secret_selftest` audit event records why. Not a
  compromise: it is what systemd's model makes possible in each position.
- **`grants` is blast-radius hygiene and audit legibility, NOT containment —
  stated plainly rather than implied.** A `grants` key is simultaneously the
  binary intercepted on PATH and the identity presented to the broker (one
  name, two jobs, deliberately). Absent `grants`, `claude`+`codex` each get
  the full bundle by default — identical effective grant to pre-Phase-133,
  just scoped to one process instead of every shell. But a bare `km-env exec
  -- sh` still gets the full bundle (no `--as`, no narrowing), and any caller
  reachable as uid `sandbox` can claim `--as claude` for itself — `km-env`
  states an identity, it does not authenticate one. **The broker is a logged
  door, not a wall**: uid `sandbox` must reach the socket for the feature to
  work at all, so peer credentials (uid/pid/exe) are recorded for
  ATTRIBUTION, not authorization, because `km-env` is the executable in every
  legitimate call and every illegitimate one alike. What changes is the
  character of a theft — passive, traceless file/environ read becomes an
  active, authenticated-in-the-uid-sense, pid-attributed request that writes
  `secret_unseal`/`secret_unseal_refused` to the CloudWatch audit stream and a
  discrete `kms:Decrypt` to CloudTrail, joinable against Phase 132's
  `km-netpolicy execs` on pid. Detection and forensics, not prevention. **On
  `privileged: true` none of it holds** — sudo can stop `km-secretsd`,
  rewrite the shims, or read the ciphertext and call KMS directly with the
  instance role's own permanent authority. The real control is
  `privileged: false`, identical in kind to the Wiz-sensor limitation.
- **Schema, purely additive, no `apiVersion` bump:** `spec.secrets.grants:
  {consumer: [keys]}`. `fenceIMDS` is a Wave 2 field name only — it is **NOT**
  in `SecretsSpec` or the JSON schema yet, and because the schema's `secrets`
  object is `additionalProperties: false`, a profile setting
  `spec.secrets.fenceIMDS` today is actively **rejected** by `km validate`,
  not silently ignored. `pkg/profile/types.go`'s `SecretsSpec` doc comment and
  the JSON schema's `secrets` description were updated in this task to stop
  describing the removed `/etc/sandbox-secrets.env` behavior — both had
  drifted true-at-Phase-89, false-since-this-phase.
- **Byte-identity preserved for the dormant case:** a profile with no
  `spec.secrets.sopsFile` renders userdata identical to before this phase —
  nothing in the broker/shim/selftest sections gates on anything else. A
  profile WITH `sopsFile` changes substantially (the entire Phase 89
  env-injection block is replaced), as intended.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.
  NOT `--sidecars`. Do not split the deploy.** The userdata changes (both
  systemd units, the shim generator, all five PATH prepends) ride in the
  create-handler zip, which `--sidecars` does not rebuild. `km-secretsd` and
  `km-env` are uploaded to `s3://<bucket>/sidecars/` by
  `buildAndUploadSidecars` via new `sidecarBuilds()` entries — shipping only
  one half means every SOPS sandbox either 404s on the gated `s3 cp` (units
  render, binaries missing, boot aborts under `set -e`) or boots with no
  interception at all (binaries present, units/shims never rendered) — the
  identical lockstep Phase 131 documented for `km ebpf-attach`'s required
  flags. `TestSidecarBuilds_CoversEverySecretsBinary` and
  `TestUserdataDownloadsMatchSidecarBuilds` (`internal/app/cmd/init_sidecars_test.go`)
  pin the pairing mechanically. Existing sandboxes keep pre-Phase-133 behavior
  until `km destroy && km create` — the binaries are fetched at boot, and
  there is no in-place migration for a running box.
- **Wave 2 (below) fences that authority at the OS layer.** Leave
  `spec.secrets.fenceIMDS` off and everything described above still holds
  exactly as written — the instance role's decrypt authority is unfenced.
- See `docs/brokered-secrets.md` for the full operator runbook (grants,
  `km-env` usage, reading `secret_unseal` events, the boot check,
  troubleshooting, deploy surface, and the security posture verbatim) and
  `docs/sandbox-secrets.md` for the superseded-note pointing at it.

**Phase 133 Wave 2 (2026-09-04) — The IMDS fence: `spec.secrets.fenceIMDS` (code-complete; live UAT pending):**
- **Wave 1 removed the dump; this removes the unmediated authority.** The
  instance role permanently holds both halves of the decrypt (`kms:Decrypt` on
  `alias/{prefix}-sandbox-secrets`, `s3:GetObject` on the ciphertext object), so
  anything on the box reaching IMDS could re-derive the whole bundle regardless
  of `grants`. **AWS has no notion of which uid on an instance is calling**, so a
  uid-granular fence can only live at the OS/network layer — that constraint is
  why the design is shaped as it is. Opt-in, dormant by default, no `apiVersion`
  bump.
- **A role CANNOT name its own ARN in its own trust policy — the design spec
  §4.4 said it could, and it is wrong.** IAM resolves a principal ARN to a
  unique principal id when the policy is *saved*, so CreateRole fails outright:
  `MalformedPolicyDocument: Invalid principal in policy`. Verified live against
  the application account before any code was written. **The replacement, also
  verified live end to end**, is the account-root principal narrowed by an
  `aws:PrincipalArn` condition naming the role, PLUS a matching identity-based
  `sts:AssumeRole` grant — root-principal delegation authorizes nothing on its
  own, so **both halves or neither**. It is exactly as narrow: `aws:PrincipalArn`
  is a global condition key AWS populates on every request, unlike the
  `aws:RequestTag`-on-`RunInstances` trap Phase 126 recorded. The spike also
  confirmed the session-policy Deny actually bites (`AccessDenied ... with an
  explicit deny in a session policy`) rather than being a policy that merely
  reads correctly. Spec §4.4 and both §11 open questions were corrected in place.
- **The fence is a systemd unit (`km-imds-fence.service`), not a bare userdata
  command, and that is the second non-obvious finding.** There is **no iptables
  persistence anywhere in this repo** and userdata does not re-run on stop/start
  — but `km-secretsd.service` and every shim ARE enabled units and do come back.
  A userdata-only `iptables -A` would leave a resumed box with a live broker,
  working agents, a clean-looking boot, and **no fence**, with nothing saying so.
  `km-secrets-check.service` is ordered `After=km-imds-fence.service` so
  assertion 6 cannot race the rule it asserts. (Pre-existing and deliberately
  out of scope: the Phase-6 nat-table DNAT rules have the same
  evaporate-on-resume property under `enforcement: proxy`.)
- **FILTER table, not nat; REJECT, not DROP.** The nat-table IMDS rule is a
  *different* rule with the opposite purpose (it `RETURN`s IMDS so IMDSv2 keeps
  working) and exists only under `enforcement: proxy` — a fence written there
  would be silently absent under `ebpf` and `both`, the other two modes. `DROP`
  makes every SDK probe wait out a full connect timeout, which reads as a hang
  rather than a policy.
- **`km-creds` asks the broker because it is itself fenced.** It runs as uid
  `sandbox` and so cannot reach IMDS for the credentials an `AssumeRole` needs;
  `km-secretsd` (root, unfenced) does the self-assume. `km-creds` is deliberately
  the dumbest component in the phase — one `credential_process` line in
  `/home/sandbox/.aws/config` and **no helper binary changed at all**, because a
  config-file `credential_process` outranks the IMDS provider in every AWS SDK's
  chain and userdata sets no `AWS_ACCESS_KEY_ID` for that user.
- **The two Denies are conditioned or exact, never blanket, and the `Allow` is
  load-bearing.** A session policy INTERSECTS with the role's identity policies,
  so a document of only Denies would grant nothing and break every helper the
  instant the fence came up. An unconditional `kms:Decrypt` Deny would take the
  helpers' SSM SecureString reads with it (a different key, granted under
  `kms:ViaService = ssm`) — precisely the breakage the fence exists to avoid.
  **Self-assume rather than a parallel role** makes the narrowed credentials
  *definitionally* the instance role minus two Denies, so the two can never
  drift; `pkg/secrets.SessionPolicy` is the single definition.
- **Assertion 6's third clause is a NEGATIVE CONTROL and must stay one** — the
  fence is proven by proving the narrowed credentials FAIL to decrypt the bundle.
  `iam simulate-principal-policy` is deliberately not used and must never be
  substituted: AWS reports an unsatisfiable condition identically to a missing
  statement, so a simulator says what a policy SAYS, not what AWS ENFORCES (see
  [[project_cross_account_gpu_launch]]). Each of the three clauses fails the
  check alone and names itself in the detail.
- **BOTH selftest invocations carry `KM_FENCE_IMDS`** — found by reading the
  rendered bash, not by a test (the test came after). Without it `runSelftest`
  builds a broker with the fence disabled and **skips assertion 6 entirely**,
  reporting a clean pass over an unfenced box. The resume unit is the one that
  matters: whether `km-imds-fence.service` came back is exactly what a resumed
  box has to prove.
- **`IsFenceIMDSEnabled` carries the `sopsFile` requirement itself**, so the IAM
  input (`service.hcl`) and the userdata block read ONE predicate and cannot
  disagree about whether a box is fenced. The field is `*bool`, not `bool`,
  because §10.3 flips the default in a follow-on phase and an operator's explicit
  `false` must stay distinguishable from silence.
- **The ec2spot pin has THREE tracking sites, not two.** Beyond
  `locals.substrate_module_versions` and `pkg/compiler/ec2spot_timeout_test.go`'s
  `ec2spotModuleDir`, `pkg/terragrunt/substrate_version_pin_test.go` also
  hardcodes it. All three must move together; the third is caught only by running
  `go test ./...`, not the compiler package alone.
- **`km-creds`'s STS session name carries the install's `resource_prefix`**, not
  a literal `km-`. It lands in CloudTrail and in the assumed-role ARN, so two
  installs sharing an account would otherwise both emit `km-fenced-...` with no
  way to tell whose sandbox called. Caught by `pkg/hygiene`'s
  `TestGoSourceNamesUseResourcePrefix`, which is exactly what that guard is for.
- **NOT containment against a determined caller, stated plainly.** Uid `sandbox`
  can still invoke `km-creds`, exactly as it can still speak the broker protocol
  directly. What the fence removes is the **unmediated** path to the role — the
  `curl 169.254.169.254` that needed no km component and left no km record.
  Everything remaining is brokered, audited (`secret_credentials` with uid, pid
  and exe) and narrowed. **On `privileged: true` none of it holds**: sudo flushes
  the rule as easily as it stops the daemon. The real control is
  `privileged: false`.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.**
  NOT `--sidecars` — the `ec2spot/v1.7.0` IAM change needs a full terragrunt
  apply and the userdata rides in the create-handler zip. **Do not split the
  deploy**: `km-creds` is uploaded by `buildAndUploadSidecars` while the
  `~/.aws/config` invoking it is rendered by the create-handler, so half a deploy
  either fetches a binary nothing references or points `credential_process` at a
  missing binary and **loses AWS access for uid `sandbox` entirely**. Existing
  sandboxes keep Wave 1 behaviour until `km destroy && km create`.
- Worked example: `profiles/brokered-secrets-demo.yaml`. See
  `docs/brokered-secrets.md` § The IMDS fence for the runbook, the two Denies
  verbatim, the honest limits, and per-clause troubleshooting.

**Idle-reap fail-safe + three missing ttl-handler DynamoDB grants (2026-09-02, v0.8.14):**
- **A live desktop sandbox was stopped as idle two minutes after its last presence
  heartbeat, against a 30-minute `idleTimeout`.** `IdleDetector.isIdle`
  (`pkg/lifecycle/idle.go`) polls the sandbox's own CloudWatch audit stream for the
  most recent event, and on `err != nil || len(events) == 0` fell back to
  `now - startTime > IdleTimeout`. Once a box had been up longer than its idle
  window — always true on a long-lived sandbox — **any single degraded poll fired
  the one-shot reap regardless of real activity.** Both triggers are live: a
  transient `GetLogEvents` error, and the empty page AWS documents `GetLogEvents`
  can return for a stream that has events (very reachable at `Limit: 1`). The read
  error was swallowed with no log line, so nothing on the box explained it after
  the fact. **Fixed by remembering `lastSeenActivity`** (newest event observed on a
  SUCCESSFUL poll) and using it as the degraded-read baseline. **The over-correction
  is deliberately fenced:** only a detector that has NEVER observed an event falls
  back to `startTime`, so a silent sandbox is still reaped and nothing leaks — pinned
  by `TestIdleDetector_NoEventsEverStillReapsFromStartTime` and
  `..._DegradedPollStillReapsWhenLastActivityIsStale` beside the two-shape regression
  test. Note this is the OPPOSITE disposition from `km-presence`'s seven signals,
  which deliberately fail idle: a signal is re-evaluated every 60s and has six
  siblings, whereas `OnIdle` fires once and is irreversible.
- **The evidence lives in CloudWatch, not the box.** The reap leaves
  `{"event_type":"idle","teardown_policy":"stop"}` in `/aws/lambda/{prefix}-ttl-handler`
  and `StateTransitionReason: "User initiated"` on the instance; CloudTrail names
  `km-ttl-handler` as the caller. Diagnose a surprise stop by diffing that timestamp
  against the sandbox's `/km/sandboxes/{id}/` audit stream — presence heartbeats are
  emitted ONLY when a signal is active, so their gaps are the idle history.
- **Three DynamoDB tables the ttl-handler is handed as env had no IAM grant**, found
  while tracing the above and all hidden by soft-failing call sites — the same shape as
  [[project_ttl_handler_submodule_teardown_iam]]. `{prefix}-budgets`:
  `RecordPauseStart` has 403'd on every stop/pause/idle-stop since it shipped (its error
  is a `log.Warn "(non-fatal)"`, so the only symptom was compute budgets counting paused
  time as running time), plus `RecordResumeClose` and `handleBudgetAdd`.
  `{prefix}-schedules`: `handleCreate`'s `PutSchedule` had its error **discarded**
  (`_ =`), so a deferred `km at <time> create` fired correctly and never appeared in
  `km at list`. Both grants added to `infra/modules/ttl-handler/v1.0.0` (edited in place,
  additive), keyed off `var.budget_table_name` / `var.schedules_table_name` — the SAME
  variables the env block uses, so env and IAM cannot disagree. `PutSchedule` now warns.
- **`handleBudgetAdd` was broken twice over.** Past the missing grant it hand-rolled an
  `UpdateItem` against key `sandbox_id`/`sk`=`"budget"` and attributes
  `compute_limit`/`ai_limit` — **none of which exist**: the live table's key schema is
  `PK`/`SK` and the limits row is `SK=BUDGET#limits` with `computeLimit`/`aiLimit`. So
  every scheduled `km at ... budget-add` would have thrown a `ValidationException` even
  with IAM. Now routed through a new exported `awspkg.AddBudgetLimits` (the ADD/delta
  counterpart to `SetBudgetLimits`) so the item shape lives in ONE place, with a test
  pinning it.
- **`TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant`** makes the pairing mechanical:
  it compares the set of `var.*_table_name` in the module's Lambda environment block
  against the set in its IAM resource ARNs. Name-agnostic, so a table added later is
  covered without editing the test — verified to fail when a grant is removed.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.** NOT
  `--sidecars`: the two new IAM policies need a full terragrunt apply. The idle-detector
  fix rides in the `km-audit-log` sidecar, fetched at boot, so **existing sandboxes keep
  the old detector until `km destroy && km create`**; the IAM and ttl-handler fixes apply
  to every sandbox immediately. No SandboxProfile schema change, no userdata change.

**Phase 132 (2026-08-29) — Exec capture: every command a sandbox ran, joined against the flow census (live UAT run 2026-08-29/30 — found and fixed three defects in pre-existing code; complete, with one gap open and part of the checklist not yet re-run against all three fixes together):**
- New on-box verbs on the already-shipped `km-netpolicy` binary: `execs [--since
  10m] [--uid N] [--failed] [--json]`, `who <host>`, `execs save`. A new eBPF
  producer (`pkg/ebpf/exec/`) attaches four tracepoints —
  `sys_enter_execve`/`sys_enter_execveat` (read argv, stash an in-flight record
  keyed by `pid_tgid` in a `BPF_MAP_TYPE_LRU_HASH`), `sys_exit_execve` (emit,
  stamped with the return code), and `sched_process_exit` — and writes
  `pkg/execlog`'s JSONL store, the process-side mirror of Phase 131's
  `pkg/flowlog`. `execs` reports what ran; `who` joins that trace against the
  flow census on pid to answer which process reached a given host — the join
  neither store could answer alone. Live-verified: a real VulnHunter run against
  `defcon.run.34` produced 12,973 exec records with real argv/ppid/uid/cgroup_id.
- **`sys_enter_execve`, not `sched_process_exec`, is the attach point** because
  argv arrives there directly as a `char *const *`, read with
  `bpf_probe_read_user_str` in a bounded loop; reading it at
  `sched_process_exec` means walking `current->mm->arg_start` via CO-RE for no
  gain. **`sched_process_exit` exists for exactly one reason: pid reuse.**
  Joining a flow to "the exec that used this pid" is wrong after wraparound —
  a flow could be attributed to a process that only inherited the number.
  The exit tracepoint gives exact process lifetimes, turning the join into
  *"the exec for this pid whose lifetime contains the flow's timestamp"* — a
  fact, not a heuristic — and the join must never be implemented without it.
- **Its own daemon (`km-execlog.service`), provisioned OUTSIDE the enforcement
  conditional — the same trap Phase 131 already hit, closed the same way.**
  The entire eBPF section of userdata is gated on `enforcement: ebpf`/`both`;
  building exec capture inside `km ebpf-attach` would make it dead in `proxy`
  mode, the schema default, silently — identical in shape to Phase 131's flow
  recording being dead under `proxy` from a root-owned `0755` directory. So
  capture runs in its own root daemon loading only tracepoints (no cgroup
  programs, no resolver, no enforcement), unconditionally, on every sandbox.
  Observation that only reports what already happened cannot widen a policy by
  being present — the same reasoning that makes the flow census itself
  unconditional. Userdata goldens change for every profile; there is no
  byte-identical case.
- **The store is root-only (`0700`/`0600`) for a reason that runs the opposite
  direction from the flow directory's `1777`.** Two decisions force it in
  combination: capture is unredacted (§3.4 — a redactor that silently
  under-matches implies a guarantee that doesn't hold, and
  `captures/{sandbox_id}/` already carries pcaps with strictly more secret
  material than argv ever will), and capture is uid-unfiltered by design
  (§3.3 — the decisive case is `privileged: true`, where the moment an agent
  `sudo`s its most interesting activity *becomes root activity*, and a uid
  filter at capture time would blind the trace to precisely the escalation
  worth seeing). Combined: root's unredacted argv would be readable by the
  sandbox user on exactly the *unprivileged* profiles meant to be the tighter
  ones. The exec log is an operator forensics artifact; the sandbox user has
  no reason to read it. Reach it via `km shell --root` or S3 after `execs save`.
- **`who` needed a live kernel to discover it could not attribute anything at
  all — the "proxy path never records a pid" design assumption was wrong, and
  the real bug was two layers deep.** (a) Agent traffic was never inside the
  eBPF enforcement cgroup: cgroup v2 only permits self-migration by a process
  that can write the *common ancestor's* `cgroup.procs`, root-owned, so every
  `sudo -u sandbox` dispatch site had been silently failing its own join
  (`2>/dev/null || true`) since `ebpf`/`both` shipped — joining *before* the
  `sudo` doesn't help either, since PAM/logind migrates the process straight
  back. Fixed in `0825b8ed` across 15 dispatch sites (Slack ×3, GitHub ×4,
  HackerOne ×4, Webhook ×2, `km-queue-runner` ×1) by joining as root in a
  subshell and dropping with `runuser`, which preserves the cgroup where
  `sudo`/`su` do not. **Consequence beyond this phase: the eBPF
  `connect4`/`sendmsg4`/`sockops`/`egress` programs had been inert for agent
  traffic since `ebpf`/`both` enforcement was introduced** — only an
  interactive `km shell` session (which joins via `km-session-entry` →
  `km-sandbox-shell`, never `sudo`) was ever actually inside the cgroup. What
  had been enforcing a profile's allowlist for every poller-dispatched and
  `km agent run` turn was the DNS resolver and the HTTP proxy alone, neither of
  which is cgroup-scoped. **This fix switches BPF enforcement on for agent
  traffic for the first time ever** — a profile whose allowlist was subtly
  too loose to be caught by DNS/HTTP alone but that the BPF allow-trie would
  have caught may now behave differently on the next `km destroy && km create`.
  Not a regression: the eBPF layer is doing its job for the first time. (b)
  Even inside the cgroup, proxied traffic emits no eBPF ring-buffer event at
  all — the sandbox user's `HTTPS_PROXY` sends it to the HTTP proxy over
  loopback explicitly, so `connect4` only ever sees a loopback connect, never
  a deny or redirect. **So the pid has to come from the proxy itself, not the
  ring buffer.** Fixed in `53762681`: `socket_pid_map` is now pinned (it never
  was), and `sidecars/http-proxy/httpproxy/pidresolve.go` resolves local TCP
  source port → socket cookie (`src_port_to_sock`, from `sockops`) → pid
  (`socket_pid_map`, populated by `connect4` on every invocation regardless of
  verdict) and `recordFlow` (`proxy.go`) stamps it on all 17 flow-write sites
  across `proxy.go`/`ses.go`/`intercept.go`, **on every verdict — allow
  included**, not only deny/redirect. Fails soft everywhere: an absent pin
  directory (`proxy` enforcement has none), a map miss, or any error just
  costs a pid, never a request. **This needs `spec.network.enforcement: ebpf`
  or `both`** — `km-http-proxy` runs under all three modes, but the pins it
  reads only exist once `km-ebpf-enforcer` has started; under pure `proxy`
  there is no BPF loaded at all, so attribution is unconditionally absent
  there, and `who` says so rather than printing `(none)`. The one narrowing
  that survives from the original design: traffic that bypasses the proxy
  entirely is still seen only by the ring buffer, which still never emits an
  event for an allowed `connect4` — that allow-is-unattributable limitation is
  real, it just no longer describes proxied traffic, which is nearly
  everything an agent does.
- **Every eBPF-sourced flow record had a byte-reversed destination IP, and
  `connect4`-layer events also a byte-reversed port — corrupting the Phase 131
  census and the `ebpf_network_deny` audit log too, not just this phase's own
  read path.** Proven live: `curl https://github.com/` recorded as
  `addr=3.114.82.140 port=47873` (true: `140.82.114.3:443`). `uint32ToIP`
  decoded the struct little-endian and then re-encoded big-endian, reversing
  the wire bytes a second time; a separate `normalizeDstPort` bug affected only
  `connect4` (`sendmsg4`/`egress_skb` already convert to host order on the BPF
  side — swapping those too would have corrupted every DNS-redirect's port 53
  into 52021). Both fixed in `d36a727b`, Go-side only — `pkg/ebpf/bpf.c` is
  untouched, so no BPF regeneration or sidecar reupload is needed for this
  fix specifically. Anyone who saved a census or capture before this fix
  should treat its `src=ebpf` address/port pairs as unreliable.
- **Open gap: `km-netpolicy execs save` run by hand fails with
  `KM_ARTIFACTS_BUCKET is not set`.** `execs_save.go` reads its S3 target
  straight from the process environment (matching `uploadCapture`'s
  precedent, correct for a daemon and wrong for an operator verb);
  `/etc/km/netpolicy.env`, which an interactive `km shell --root` session
  sources, carries `KM_FLOWLOG_DIR` and its siblings but none of
  `KM_ARTIFACTS_BUCKET`/`KM_SANDBOX_ID`/`KM_EXEC_DIR`. `ExecStopPost` still
  works (the systemd unit supplies its own environment); `sudo systemctl
  restart km-execlog` is today's workaround for an on-demand save. Fix is to
  route those three through `buildOpts`'s env→`/etc/km/netpolicy.env` `pick()`
  chain, or write them into that file in userdata — not yet done.
- **argv is bounded (`MAX_ARGS=20`, `ARGSIZE=128`), `truncated` set rather than
  silently clipped; a hard power-off loses whatever hadn't reached disk.**
  `km-execlog.service` saves via **`ExecStopPost`, not `ExecStop`** — for
  `Type=simple`, systemd runs `ExecStop` *before* signalling the main process,
  which would upload the trace before the daemon's SIGTERM handler drains its
  buffered-but-unwritten tail; `ExecStopPost` runs after the process has
  actually exited, once that drain has happened. That makes `km stop`,
  `km pause`, and the ACPI shutdown before an EC2 terminate all save without
  anyone remembering — but only on a graceful stop; live-verified for
  `systemctl stop` and pause/resume, not yet for a real terminate.
  `StartLimitIntervalSec`/`StartLimitBurst` live in `[Unit]`, not `[Service]`
  — since systemd v230 the `[Service]` section only recognizes the
  pre-rename `StartLimitInterval=` spelling, so the renamed keys placed there
  are silently dropped as unknown, leaving the 10s manager default in effect
  (which a 5s `RestartSec` never actually breaches) and hiding a
  permanently-broken tracer behind an endless restart loop instead of one
  visibly failed unit. Two rotations discard the earliest generation (typically
  the package-install phase) — live-verified at 0 rotations under 1.3MB of
  real traffic, not yet under enough volume to force one. On `privileged: true`
  the agent can stop `km-execlog.service` like any other unit; the real
  control is `privileged: false`, identical in kind to the Wiz-sensor
  limitation. A kernel-side ring-buffer drop that never stalls the tracer's
  own drain loop is invisible — `cilium/ebpf` exposes no drop counter for
  `BPF_MAP_TYPE_RINGBUF`, so a stalled consumer is the only failure mode this
  can actually detect.
- **`AWS_REGION` is present on the unit from the first commit — the bug
  `216d4664` just fixed on `km-capture.service`, not repeated here.** Its
  absence fails the SDK with *"Invalid region: region was not a valid DNS
  name,"* which reads as a bucket problem rather than a config one, and
  degrades silently because the upload (`execs save`, modelled on
  `capture stop`'s `uploadCapture`) is deliberately best-effort.
- **`infra/modules/ec2spot/v1.6.0`** (new immutable dir, never an edit to
  `v1.5.0`) adds `execs/${sandbox_id}/*` to the existing `s3:PutObject`
  statement alongside `transcripts/`, `captures/`, and `learn/`, plus the pin
  bump in `locals.substrate_module_versions`.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.**
  NOT `--sidecars` — the `ec2spot` IAM change needs a full terragrunt apply,
  and the userdata installing `km-execlog.service` rides in the create-handler
  zip, which `--sidecars` does not rebuild. **Do not split the deploy**: the
  `km-netpolicy` binary carrying the new verbs is uploaded by
  `buildAndUploadSidecars` while the unit invoking `execs-daemon` is rendered
  into userdata by the create-handler — ship one half without the other and
  `km-execlog.service` crash-loops on an unknown verb, the identical lockstep
  Phase 131 documented for `km ebpf-attach`'s required flags. Existing
  sandboxes gain nothing until `km destroy && km create`.
- **Recorded debt:** `pkg/execlog` duplicates `flowlog.Writer`'s rotation,
  `.rotations` counter, and one-shot write-failure warning rather than sharing
  them — deliberate, because PR #89 (the Phase 131 branch this ships on) was
  open and under review and `pin` correctness depends on those exact rotation
  semantics; churning them inside a reviewed-but-unmerged PR traded a real
  risk for a cosmetic gain. **Follow-up, with an owner:** once #89 merges,
  extract a shared `pkg/jsonlstore` and move both `flowlog` and `execlog` onto
  it.
- **None of the three defects above were reachable by code review** — each
  needed a real kernel. `pkg/ebpf/audit` is linux-tagged: `go test ./...` on
  macOS runs 6 of its tests, Docker runs 15, and a green suite on a dev
  machine does not cover the package at all — which is part of why the
  endianness bug survived as long as it did, since the helper and its test
  agreed with each other and both disagreed with the kernel.
- See `docs/exec-capture.md` for the full operator runbook,
  `docs/egress-census.md` for where the endianness fix also landed,
  `docs/superpowers/specs/2026-08-29-exec-capture-design.md` for the design,
  and `.planning/phases/132-exec-capture/132-UAT.md` for the full live-UAT
  record.

**Phase 131 (2026-08-28) — Live egress census, allowlist pinning, on-demand packet capture (complete):**
- New on-box verbs on the already-shipped `km-netpolicy` binary: `observed`,
  `flows [--since 10m] [--denied] [--json]`, `profile`, `pin [--dry-run]
  [--exact] [--yes] [--allow-empty] [--accept-truncated]`, `capture
  start|stop|status|list`. Four producers — the eBPF audit consumer, the DNS
  proxy, the HTTP proxy, and the eBPF resolver — now append what they already
  decide (verdict + destination) to one JSONL file each under
  `/var/lib/km/flows/`, merged at read time by a new `pkg/flowlog` package. `pin` is the mirror image of `km-netpolicy deny`: `effective allow =
  profile allowlist ∩ pin₁ ∩ pin₂ ∩ …`, its own kernel-append-only file
  (`/var/lib/km/netpolicy/allow.pins`, `chattr +a`), same guarantee, same
  absence of a removal verb. Packet capture runs in a root-owned
  `km-capture.service` daemon (pure-Go `AF_PACKET`, no cgo, no libpcap — the
  sidecar cross-compile requires `CGO_ENABLED=0`) so the sandbox user never
  needs `CAP_NET_RAW` even unprivileged.
- **Unconditional — no profile field, no dormant case.** Observation runs on
  every sandbox regardless of `learnMode` or anything else in the profile,
  the one deliberate exception to the platform's default disposition, on the
  same reasoning as `km-netpolicy deny`'s own un-gating: a facility that can
  only narrow, and that only reports what already happened, cannot widen a
  policy by being present. Userdata goldens change for **every** profile as a
  result — there is no byte-identical case to check against.
- **`pin` is irreversible; there is no un-pin verb, deliberately.** Recovering
  from a too-tight pin is `km destroy && km create`, not a command. The
  default pin narrowing is **collapsed** (DNS suffixes always eTLD+1; hosts
  collapsed too unless `--exact`), not the tightest possible reading, because
  the risk is asymmetric: too loose costs a follow-up `pin` (cheap, always
  available); too tight has no recovery path at all. `--dry-run` shows both
  candidate lists before anything is written; a non-dry-run pin requires
  `--yes`; an empty census additionally requires `--allow-empty` because it
  is mathematically deny-all and that is easy to trigger by accident.
- **The five-site pin-wiring trap, closed by a mechanical test.** Every
  consumer of the runtime deny store — dns-proxy, http-proxy, the eBPF
  resolver's allowlist, both proxy units' env, the eBPF enforcer's own CLI
  flag — had to gain a matching pin-store read, mirroring the five sites
  commit `0c7f9880` had to touch to un-gate `km-netpolicy` itself in the first
  place: ship the writer without a reader and the verb reports success while
  nothing enforces it. `pkg/netpolicy/wiring_guard_test.go` makes the pairing
  mechanical rather than remembered — **including `ebpf_attach.go` itself**,
  which the guard originally omitted, mechanising four sites and leaving the
  fifth (the one the plan actually missed) remembered. **The boot-time pre-seed loop in
  `internal/app/cmd/ebpf_attach.go` was the site most likely to be missed and
  the most load-bearing** — it resolves `--allowed-hosts` and seeds IPs into
  the BPF allow-trie on every start (boot, resume), bypassing the live
  resolver check pins otherwise gate. It originally consulted denies but not
  pins, so a reboot or `km resume` on a pinned box would have re-seeded a
  pinned-out host's IP straight into the trie and silently bypassed the pin —
  now gated via `shouldSeedHost(host, denier, pinner)`, deny checked first,
  pins narrowing in addition. This is exactly why pins live under `/var/lib`
  and not `/run`: they are designed to survive a reboot, and this was the one
  path that could have quietly un-survived them.
- **The HTTP proxy is four sites, not one.** Flow recording had to land in
  `proxy.go`, `ses.go` (SES MITM), `intercept.go` (Phase 129 operator
  intercepts), plus construction in `main.go`. SES and intercepts were
  initially missed; had they stayed missed, a `pin` taken from a census
  blind to them would have **denied SES and every operator-declared intercept
  host** the moment it was applied — silently breaking sandbox email and
  MITM intercept rules on the very box that pinned. The clearest illustration
  in this phase of why the flow census has to be structurally complete, not
  just "the obvious places."
- **Known, accepted gap:** `transparent.go` (the BPF-redirected `ebpf`/`both`
  L7 path) records no flow of its own. Not a pin regression — the eBPF
  `connect4` program captures the destination **before** rewriting it and
  emits a redirect event carrying the *original* destination, so the flow
  store still sees the real host and it is still pinnable. What's missing is
  only *which* intercept rule fired, not the fact that traffic reached one.
- **The `learn/` S3 grant gap is fixed here, riding along.** The sandbox
  role's only S3 write grant was `transcripts/${sandbox_id}/*`
  (`ec2spot/v1.4.0`) — `learn/` (the S3 flush target `km shell --learn` has
  used since it existed) never had a grant and every `PutObject` there had
  been 403ing since day one. Invisible because the failure is a
  `logger.Warn` with `s3Key="skipped"` and `fetchEC2ObservedJSON` falls back
  to an SSM RunCommand read, so `km shell --learn` kept working through the
  outage and nobody noticed the primary path was dead — the same
  soft-logger-hides-drift shape as the ttl-handler teardown gap (see
  [[project_ttl_handler_submodule_teardown_iam]]). **The missing grant was
  the bug; the soft failure was correct and was deliberately left alone** —
  fixed via a new immutable `infra/modules/ec2spot/v1.5.0` (never edit
  `v1.4.0` in place) adding `s3:PutObject` on `captures/${sandbox_id}/*` (new,
  for `capture stop`'s upload) and `learn/${sandbox_id}/*` (repair) in the
  same statement, plus the pin bump in `locals.substrate_module_versions`.
- **The census is empty in two of three modes unless FOUR producers exist, not
  three.** The design's premise that "the eBPF ring buffer emits a record per
  connection" is false against `pkg/ebpf/bpf.c`: `emit_event` fires only for
  `ACTION_DENY` and `ACTION_REDIRECT`; the connect4 allow path returns without
  emitting, deliberately (one event per allowed connection is real CloudWatch
  volume). And under `ebpf`/`both` the bootstrap leaves `km-dns-proxy` disabled,
  so there was **no name producer at all** — `PinCandidates` skips address-only
  rows, so the pinnable set was EMPTY on exactly the modes where a pin bites
  hardest. Fixed by making the **eBPF resolver a flow producer** (`SrcResolver`,
  `flows.resolver.jsonl`), which is symmetric — every enforcement point that
  decides is now a producer — and adds no per-connection volume.
- **`/var/lib/km/flows` is `1777`, and 0755 made flow recording DEAD in the
  default mode.** km-dns-proxy and km-http-proxy run `User=km-sidecar`; the eBPF
  enforcer and resolver run as root. A root-owned `0755` directory EACCESed every
  proxy write, and both producers deliberately discard that error on the egress
  hot path — so under `proxy` enforcement, the DEFAULT and the only mode where
  those two are the sole recorders, `observed` printed `(none)` on a box that had
  reached plenty. Invisible to every test on the branch: userdata tests assert
  string presence, producer tests write into a `t.TempDir()` the test owns.
  `flowlog.Writer` now also logs its OWN first write failure once
  (`flowlog_write_failed`), so the next silent-store failure is loud.
- **A pin can never seal `.amazonaws.com` — a compiled-in fuse in
  `pkg/netpolicy`.** Under `ebpf`/`both`, `/etc/resolv.conf` points at the on-box
  resolver and **DNS is neither uid- nor cgroup-scoped**, so it answers for root
  too (`amazon-ssm-agent` above all) — while root's destinations barely reach the
  census, since the audit consumer only watches the sandbox cgroup. The first
  `pin` therefore NXDOMAINed `.amazonaws.com` for root: SSM dies, and with no
  un-pin verb, no `km shell` and no remote destroy, the only recovery is
  terminating the instance. **The spec's own UAT step 7 would have triggered it.**
  The carve-out is the ONE deliberate exception to "pins narrow everything", is
  not operator-overridable, and applies to pins only — `km-netpolicy deny
  .amazonaws.com` still blocks, because a deny is a named act where a pin denies
  everything nobody named.
- **`--exact` was inert until the pin file gained scopes.** `PinCandidates`
  always returns collapsed eTLD+1 suffixes as its DNS list (an exact DNS
  allowlist stops resolving almost immediately), and `pin` wrote ONE flat
  generation that both matchers read — so the collapsed `.github.com` beside the
  literal `api.github.com` matched every subdomain on the host side and the flag
  narrowed nothing. Entries are now tagged `dns:` / `host:` (untagged = both);
  `Pinner.AllowsDNS` / `AllowsHost` gate their own matcher, and `Pinner.Allows`
  (both scopes, the strictest answer) is what the boot pre-seed loop uses, since
  seeding an IP into the trie bypasses both per-query checks at once.
- **Rotation can silently truncate the census that feeds an irreversible
  operation.** One prior generation is kept, so the SECOND rotation discards the
  earliest records — usually the package-install phase. A per-producer
  `.rotations` counter now records it; `pin` warns on any rotation and refuses
  outright past the first loss without `--accept-truncated`.
- **A finished capture collects itself.** A capture that hit its own duration or
  `--max-size` bound left `active` set forever: `status` claimed it was still
  capturing and `start` kept refusing. It now clears and uploads on its own.
  `--max-size` **stops** the capture; there is no ring rotation (docs and spec
  promised one) — a ring keeps the last N bytes and discards the start of the
  trace, which is usually the part a failure question needs.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.**
  NOT `--sidecars` — the new unit (`km-capture.service`), the new env vars on
  both proxy units, and the new append-only pin file all ride in the
  create-handler-rendered userdata, which `--sidecars` does not rebuild.
  Existing sandboxes gain nothing until `km destroy && km create`.
- **The sidecar/userdata lockstep here is tighter than any prior phase.**
  `km ebpf-attach` gains two REQUIRED flags (`--netpolicy-pins`, `--flowlog-dir`)
  that the create-handler-rendered userdata now always passes, but the enforcer
  binary comes from `s3://<bucket>/sidecars/km` (uploaded by
  `buildAndUploadSidecars`) while the userdata comes from the create-handler zip.
  Run `make build-lambdas` + `km init --lambdas` **without** the sidecar upload
  and every `ebpf`/`both` sandbox boots an enforcer that rejects an unknown flag
  and crash-loops — **no network enforcement at all** on a box that otherwise
  looks healthy. The full `km init --dry-run=false` does both halves; do not
  split the deploy.
- See `docs/egress-census.md` for the full operator runbook (census, pin
  model, collapsed-vs-`--exact`, capture bounds, troubleshooting) and
  `docs/egress-deny-lists.md` § Allow pins for the deny/pin relationship.

**Wake-up re-credential + `km create` positional alias (2026-08-26):**
- **`km resume` now forces a GitHub installation-token re-mint** instead of waiting up to 45
  minutes for the per-sandbox refresher's next tick. `awspkg.ForceGitHubTokenRefresh` reads the
  refresher's own EventBridge schedule (`{prefix}-github-token-{id}`), takes its `Target.Input`
  **verbatim**, and hands it to `{prefix}-github-token-refresher-{id}` via a synchronous
  `RequestResponse` invoke. Wired into BOTH resume paths: `runResume` (local) and the ttl-handler's
  `handleResume` (`km resume --remote`, `km at … resume`).
- **Reading the payload back off the schedule is load-bearing, not convenience.** It carries
  `installation_id`, the per-sandbox KMS key ARN, `allowed_repos` and the *compiled* permissions
  map — all decided at create time. Rebuilding it here is exactly the duplication that (per
  `pkg/github/token.go` `TokenRefreshEvent`) kept this feature silently broken for its entire
  lifetime once already.
- **The real value is surfacing, not re-minting.** The 45-minute schedule is never stopped by
  pause/stop — only destroy removes it — so a healthy install already has a ≤45-min-old token at
  wake-up, and the on-box helpers (`km-git-askpass`, `km-git-credential-helper`, `km-github`) read
  SSM at call time and never cache. The failure mode this catches is a refresher that has been
  erroring on **every** tick: today those errors reach only CloudWatch and the operator meets them
  later as a git 401. The synchronous invoke turns that into an error printed at the moment the box
  is woken, naming the log group.
- **Best-effort by construction.** Both call sites are non-fatal (the instance is already starting;
  failing the command afterwards would leave a running box and a red exit code) and bounded at 30s.
  A sandbox with no `sourceAccess.github` has no schedule — `ErrNoGitHubRefresher` is **silent**,
  not a warning, so the line that matters doesn't get trained out of operators.
- **Autonomous wake-ups are covered too.** The Phase 109/114 bridge auto-resume path calls
  `EC2Resumer.StartSandbox` directly and never routes through `handleResume`, so all four
  resumers (`pkg/{github,slack,h1,webhook}/bridge`) gained an optional
  `TokenRefresher awspkg.GitHubTokenRefresher` field, invoked immediately after a successful
  `StartInstances` via `awspkg.BestEffortRefreshGitHubToken`. Wired unconditionally in the four
  bridge `main.go`s with `awspkg.NewGitHubTokenRefresher(cfg, prefix)`; **nil is a no-op**, so a
  bridge running older wiring resumes exactly as before.
- **The refresh result is SWALLOWED inside `StartSandbox`, and that is load-bearing.** The
  bridges branch on `StartSandbox`'s error, and on the Phase 109 terminal path a failure
  **deletes the sandbox's DynamoDB row and cold-creates a replacement**. Letting a token-refresh
  failure surface there would destroy a healthy running sandbox over an expired credential. The
  instance is already started by that point; the only correct response is to log. Pinned by a
  test in each of the four packages, plus `TestBestEffortRefreshGitHubToken_NeverPropagates`
  (the helper returns nothing, so there is no error path to mishandle).
- **No refresh happens unless a box actually started** — not on `StartInstances` failure, and not
  on the `ErrNoResumableInstance` orphan path that falls through to cold-create. Also tested per
  package.
- **Bridge IAM:** each of the four bridge modules' `ec2_resume` policy gains
  `GitHubTokenScheduleRead` (`scheduler:GetSchedule` on
  `schedule/default/{prefix}-github-token-*`) and `GitHubTokenRefreshOnResume`
  (`lambda:InvokeFunction` on `{prefix}-github-token-refresher-*`), both prefix-scoped so a
  bridge can never refresh a sibling install's sandbox.
- **`km create <profile.yaml> [alias]`** — the alias may now be a second positional instead of
  `--alias`. Whichever argument ends in `.yaml`/`.yml` is the profile, so order is irrelevant.
  Ambiguity is always an error, never a guess: two YAML-looking args, zero YAML-looking args, or a
  positional alias combined with `--alias` all fail with a message naming the conflict — silent
  precedence between two alias sources would create the sandbox under a name nobody typed. The
  one-positional form is deliberately extension-agnostic so every path shape that worked before
  still works.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.** NOT `--sidecars`:
  five Lambda roles gain new IAM statements (ttl-handler plus the four bridges), and an env/IAM
  change needs a full terragrunt apply. The ttl-handler's matching `scheduler:GetSchedule` was
  already granted by `SchedulerCleanup`; the operator policy already holds `lambda:*` +
  `scheduler:GetSchedule` on `{prefix}-*`, so local `km resume` needs no IAM change. **Order
  matters**: `make build-lambdas` before `km init`, or the bridges deploy with the new IAM but
  the old code and no refresh happens. No SandboxProfile schema change, no userdata change, **no
  sandbox recreate** — this is entirely control-plane.

**Operator container image is now plan-capable; profiles layer instead of replacing (2026-08-28):**
- The `containers/operator` image goes from "`--remote` client" to "`--remote` client **plus
  read-only platform inspection**": `km init --dry-run`, `km init --plan`, and
  `km bootstrap --plan` all work in-container, running real terragrunt plans against a real
  account. **Applying is still out, and for a mechanical reason, not a policy one** —
  `km init --dry-run=false` shells out to `go build` at run time (`buildAndUploadSidecars`,
  `uploadCreateHandlerToolchain`) and pushes ECR images, so supporting it means a Go toolchain,
  the full source tree, and a docker socket, at which point it is a dev environment, not a client.
- **Three things had to ship together; any two of them is a worse failure than none.** (1)
  `terraform`+`terragrunt` installed from the release tarball (previously discarded as unusable —
  correct while no `infra/` shipped). (2) `infra/` baked to `/klanker-maker/infra` **plus a stub
  `CLAUDE.md` at `/klanker-maker`** — every unit under `infra/live` resolves paths via
  `dirname(find_in_parent_folders("CLAUDE.md"))`, so without a file by that name terragrunt aborts
  before naming a single module. The stub is written by the Dockerfile, NOT copied: terragrunt
  reads only the dirname, and the real CLAUDE.md is ~90KB of internal phase history. (3) Prebuilt
  Lambda zips at `/klanker-maker/build`.
- **The zips are the non-obvious requirement.** Nine modules read theirs via
  `filebase64sha256("${repo_root}/build/<name>.zip")` and **terraform evaluates that at PLAN
  time**, so without them `--plan` fails outright on the first Lambda module rather than degrading.
  `findRepoRoot()` finds no `go.mod` in the image and falls back to cwd, so `/klanker-maker` IS the
  repo root and everything lands where `${local.repo_root}/...` expects. `region.hcl` is gitignored
  and absent from clone and archive alike; `ensureRegionHCL` writes it on first run.
- **`KM_LAMBDAS`** (`auto`|`local`|`release`, default `auto`) picks the zip source: `local` uses
  `build/*.zip` from the build context so a working clone can build a plan-capable image without
  cutting a release first — without it this Dockerfile would break every `make operator-image`
  between a change landing and the next tag. `auto` prefers local and always announces which it
  took. `publish-image.yml` pins `release` explicitly: a published image taking its zips from a
  dirty runner must be impossible by configuration, not by the runner happening to be clean.
- **Three real bugs found and fixed on the way, all correct independent of containers:**
  - **`buildLambdaZips` deleted the zips it then skipped.** `os.Remove(zipPath)` ran BEFORE the
    source-existence check, so in a repo-less install the first `km init --plan` wiped the very
    zips the modules depend on and every run after it failed. Warn-and-continue at the call site
    hid it. Fixed by reordering; pinned by a test.
  - **`runInitDryRun` printed the wrong module paths** — built `regionDir` from the raw region
    (`infra/live/us-east-1/...`) while every applying path uses `compiler.RegionLabel(region)`
    (`infra/live/use1/...`), contradicting its own banner one line above.
  - **`profile_search_paths` was broken at both ends.** Its shipped default `~/.km/profiles` never
    resolved (`filepath.Join` does not expand `~`, so it looked for a literal `~` directory) AND
    the key was absent from the v2→v merge-list, so an operator setting it in `km-config.yaml` was
    silently ignored (the `project_config_key_merge_list` footgun). Both fixed; `expandTildePaths`
    leaves relative entries like `./profiles` alone on purpose — making them absolute at load time
    would pin them to wherever km was launched.
- **Profiles LAYER, they do not replace.** Mounting at `/root/.km/profiles` puts your profiles
  ahead of the baked library while `extends: base/...` still finds the shipped fragments — which is
  what makes a short profile of your own usable at all. The image sets
  `KM_PROFILE_SEARCH_PATHS="/root/.km/profiles /klanker-maker/profiles /work"`. **Whitespace-, not
  comma-separated** (viper `AutomaticEnv` + `GetStringSlice` splits on whitespace; a comma value
  becomes one path matching nothing) and **absolute on purpose** — the built-in default's
  `./profiles` is relative to cwd, so it silently stops meaning the baked library the moment you
  `cd /work`. Both pinned by tests.
- **New mounts** (`make operator-shell` wires all of them, each guarded on its source existing —
  docker silently creates a *directory* at a missing bind source): `~/.km` → `/root/.km` (the
  per-sandbox ed25519 keys, absent which `km vscode`/`desktop`/`tunnel` rekey every run and strand
  an `authorized_keys` entry), `./profiles` → `/root/.km/profiles`, `$PWD` → `/work`.
- **Image grows 1.12GB → 1.78GB** (zips 215M, terraform 82M, terragrunt 70M, infra 2M).
- **Applying from the container is DECIDED AGAINST (2026-08-29), not blocked.** Do not
  re-open it on the grounds that the plumbing looks tractable — it is tractable, and that
  was considered. It is **not** a permissions boundary (the image holds whatever `~/.aws`
  carries, and plan is not even read-only at the AWS level: nothing passes `-lock=false`,
  so it takes DynamoDB state locks), and it is **not** an unsolved mechanical problem —
  prebuilt release artifacts would fix it exactly as they fixed the Lambda zips, and
  `cmd/create-handler` already proves the pattern in production, running real
  `terragrunt apply` with no Go toolchain off `toolchain/{km,terraform,terragrunt,
  infra.tar.gz}`. The reason is **semantic**: `buildAndUploadSidecars` compiles with
  `Dir = repoRoot`, so a native `km init --dry-run=false` deploys the WORKING TREE,
  uncommitted edits included, while a container one could only ever deploy THE RELEASE.
  One command name over two materially different operations means someone eventually
  runs it expecting the first and silently gets the second. If it is ever wanted it
  needs its own verb (*deploy this release*), not this flag.
- **Why the plan tier escaped that problem:** plan only needs an artifact to EXIST so
  `filebase64sha256` can hash it, and a hash that disagrees with what is deployed is
  honest noise in the diff. Apply has no such tolerance — what it uploads becomes the
  deployed Lambda. That asymmetry is the whole reason one tier shipped and the other did not.
- **Deploy = cut a release.** The image is built by `publish-image.yml` on `release: published`
  and needs the new `km_vX.Y.Z_lambdas.tar` asset; a release predating it fails the build with a
  message saying so rather than producing an image that silently cannot plan. Nothing here touches
  a sandbox, a Lambda, or any AWS resource — no `km init`, no recreate.
- See `containers/operator/README.md`.

**Runtime egress narrowing is now unconditional (2026-08-27, v0.8.8):**
- **`km-netpolicy` and its whole mechanism ship on EVERY sandbox.** The
  `spec.network.egress.runtimeDeny` gate is removed from all **five** sites: the sidecar
  `s3 cp` + symlink, `KM_NETPOLICY_FILE` on **both** proxy units, the append-only deny file
  (+`chattr +a` +`/etc/km/netpolicy.env`), and the eBPF enforcer's `--netpolicy-file`.
- **The gate was backwards.** `km-netpolicy` can only ever ADD denies (no removal verb, file
  is kernel-append-only), so its presence can never widen a policy — while the boxes where
  narrowing matters most are exactly the wide-open ones (`allowedDNSSuffixes: ["*"]`, learn
  mode) that no profile would have thought to opt in. Gating it meant the lock-it-down tool
  was absent precisely where locking down was most valuable, and gaining it cost a
  `km destroy && km create`.
- **All five sites or none — a partial change is WORSE than the gate.** Shipping only the
  binary would let `km-netpolicy deny x` report success while nothing read the result, and
  `list` would print `(none)`. Both were real bugs in the original phase. The eBPF site is
  the easiest to forget and the most load-bearing: under `ebpf`/`both` the bootstrap leaves
  `km-dns-proxy` disabled and the resolver serves DNS, so without `--netpolicy-file` a deny
  would report success while the host stayed resolvable.
- **`spec.network.egress.runtimeDeny` is DEPRECATED and a no-op**, still accepted so existing
  profiles keep validating. Deliberately NOT removed from the schema — breaking every
  operator's file to delete a harmless key is a poor trade.
- **This deliberately breaks the "dormant ⇒ byte-identical" rule** (the second such departure,
  after Phase 129's rickroll deletion). Three goldens gained 54–55 lines each, verified
  purely additive and netpolicy-only. The **frozen** pre-92 baseline was hand-patched by
  writing `stripSubagentStopScript(generated)`, NOT via `CAPTURE_PRE92_BASELINE=1` — that flag
  writes the UNSTRIPPED output and corrupts the golden (see
  [[project_frozen_byte_identity_golden_capture_trap]]).
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`** (the
  create-handler zip renders userdata; NOT `--sidecars`, which does not rebuild it).
  **Existing sandboxes need `km destroy && km create`** to gain it — the binary is fetched at
  boot. See `docs/egress-deny-lists.md`.

**Phase 130 (2026-08-26) — `km tunnel`: reverse-tunnelled kubectl into a sandbox, against a cluster only the operator's laptop can reach (code-complete; live UAT pending):**
- `km tunnel k8s <sandbox-id> --context <ctx>` drops the operator into an interactive sandbox
  shell where `kubectl` works against a cluster reachable only from their own workstation
  (OpenVPN route on the laptop, VPN credential cannot leave it). **No VPN credential, SSO
  refresh token, or AWS credential ever reaches the sandbox.**
- **`km tunnel` is a FAMILY, and the subcommand split is deliberate.** The reusable part is
  the transport (SSM forward to sshd + `-R` reverse forwards inside the SSH session); modes
  differ only in what they carry and what they provision on the box. Modes are subcommands
  rather than mutually-exclusive `--k8s`/`--socks` booleans specifically so a second mode
  does not produce one help page that is the union of two unrelated option sets with no way
  to tell which flag applies where. Two modes ship: `k8s` and `socks`.
- **`km tunnel socks <id>`** gives the box a SOCKS5 proxy on loopback egressing via the
  operator's workstation. `ssh -R <port>` **with NO destination** makes the ssh CLIENT a
  SOCKS 4/5 proxy — it is `-R` with no destination, **NOT `-D`**, which is the forward
  direction (a local proxy egressing via the remote) and the opposite of what a sandbox
  needs. No broker, nothing written on the box. `socks5h` (resolve-at-proxy) is the correct
  scheme: names resolve operator-side over the VPN, the same property that makes `k8s` work.
  **A far wider path than `k8s` by design** — two sockets to one cluster versus arbitrary
  access to everything the VPN reaches — accepted deliberately by the operator (2026-08-26);
  do not re-litigate. It dies with the shell like every other mode.
- **`--set-proxy-env` is OFF by default and that is a considered choice, not caution:** km
  meters Bedrock/Anthropic/OpenAI spend by MITM-ing those hosts in its own http-proxy, and
  traffic sent through SOCKS never reaches it, so **AI spend silently stops being metered**
  and `km status` under-reports. When the flag IS set, `NO_PROXY` must carry
  **`169.254.169.254`** — IMDS is how the box gets its IAM role credentials, and proxying
  link-local metadata through the operator's laptop breaks every AWS call the sandbox makes.
  Both are pinned by tests.
- **The deploy surface is `make build` ALONE** — no profile field, no sidecar, no userdata
  change, no Lambda rebuild, no `km init`, and **no `km destroy && km create`**. It works on
  sandboxes already running. Three facts make that true and are worth knowing before anyone
  "adds the missing plumbing": `IsVSCodeEnabled` (`pkg/profile/types.go:973`) **defaults to
  true**, so every box already has sshd and a keypair at `~/.km/keys/<id>`; userdata never
  writes an `sshd_config`, so stock `AllowTcpForwarding`/`AllowStreamLocalForwarding yes` +
  `GatewayPorts no` apply and are exactly right; and the box kubeconfig + shim are written
  at **connect time over SSH**, which is also what lets a different cluster be targeted
  with no recreate.
- **SSM has no reverse-forward primitive** (both `AWS-StartPortForwardingSession` variants
  are strictly local→remote), so `-R` rides inside SSH which rides inside the existing SSM
  forward. The load-bearing property: **`ssh -R` resolves and dials its target CLIENT-side**,
  so the cluster hostname goes over the laptop's VPN and the sandbox needs no route, no DNS,
  and never consults km's NXDOMAIN-by-default resolver.
- **Credentials proxy Kubernetes' own ExecCredential protocol — neither side reimplements
  OIDC.** Box kubeconfig runs a `curl --unix-socket` shim; a broker on the laptop runs the
  operator's *existing* exec plugin (`kubelogin`) and returns its stdout **verbatim**. The
  broker is deliberately the dumbest component in the phase — no parsing, caching, retry, or
  rewriting — because the plugin's real behaviour is unverifiable from the dev machine. It
  also never reads the request body: the box's `KUBERNETES_EXEC_INFO` describes the fake
  `127.0.0.1` endpoint, so forwarding it would be actively misleading (`provideClusterInfo:
  true` is unsupported; kubelogin does not need it).
- **`ExitOnForwardFailure=yes` is the single most important line in the phase.** A failed
  `-R` bind is only a WARNING by default, so without it the operator gets a perfectly good
  shell attached to a dead tunnel and an inexplicable `connection refused` from kubectl —
  and two operators tunnelling to one sandbox is exactly how that happens. Pinned by a test,
  alongside `StreamLocalBindUnlink=yes` (a stale socket otherwise blocks the rebind).
- **`interactiveMode` is REQUIRED under `client.authentication.k8s.io/v1`** — kubectl rejects
  the exec config outright with `interactiveMode must be specified` before the plugin ever
  runs. Set to `Never` unconditionally (it is also a valid optional field under `v1beta1`
  since k8s 1.23, so one value is correct for both); found during a dry-run, not by a test.
- **Two live-UAT bugs, fixed in v0.8.7 — both operator-side RENDERING bugs, nothing on the
  box, in AWS, or in a Lambda:**
  - **The exec `apiVersion` must be MIRRORED from the operator's kubeconfig, never chosen.**
    `RenderBoxKubeconfig` hardcoded `client.authentication.k8s.io/v1`, but the broker
    deliberately never sets `KUBERNETES_EXEC_INFO`, so the plugin cannot know which version
    was asked for and emits **its own default** (`v1beta1` for `kubectl oidc-login`).
    kubectl enforces an **exact** match, so this failed with `plugin returned version
    client.authentication.k8s.io/v1beta1`. `Resolve` already carried `exec.apiVersion`
    "verbatim" and the renderer ignored it at the one place it mattered. **Hardcoding
    `v1beta1` instead is the same bug pointed the other way** — mirror, don't choose.
    Version-shopping kubectl on the sandbox cannot fix it: every version enforces the match.
  - **The cluster CA has to travel.** `clusterSpec` never read
    `certificate-authority-data`, so the box fell back to its system trust store and every
    handshake died with `x509: certificate signed by unknown authority`. **`tls-server-name`
    says which NAME to verify against; the CA says which certificate to TRUST** — km was
    emitting one half of a pair, and no stock trust store has ever heard of a private
    corporate root or an EKS-managed CA. A `certificate-authority` **path** is read on the
    operator's machine and base64-inlined, because that path does not exist on the sandbox.
    `insecure-skip-tls-verify` is **MIRRORED only, never invented** — kubectl rejects a
    config carrying both it and a CA, and silently disabling verification to make an x509
    error go away would delete the one property that makes the loopback/SNI split safe.
  - **Carrying the CA does NOT weaken the no-credential-on-the-box rule.** A CA certificate
    is public, verification-only, and mints nothing; it cannot be replayed and grants no
    route to the cluster. The VPN profile, SSO refresh token, and AWS credentials all still
    stay on the workstation.
- **The loopback bind port need not match the cluster's real port** (`-R 16443:k8s1.corp:443`
  is fine), because the box kubeconfig uses `tls-server-name` rather than an `/etc/hosts`
  entry. That is the whole reason **no `privileged: true` is ever required** — the rejected
  `/etc/hosts` alternative would have forced a port match and thus root for a 443 API server.
- **A reverse tunnel bypasses km's egress enforcement by construction** — the MITM proxy, the
  eBPF allowlist, and `deniedHosts` never see this traffic. **The honest control is the
  lifetime: the tunnel dies with the shell**, and there is deliberately NO `-N`, no detached
  mode, and no daemon. An earlier draft's `spec.network.reverseTunnel` profile field was
  **dropped** — connect-time provisioning removed its only real job, and keeping it would
  have implied an authorization control that does not exist (the operator holds the private
  key and can `ssh -R` by hand regardless).
- **None of this is testable on the dev machine** (no VPN, no cluster, no kubectl), and every
  debug cycle costs a tagged release. So `--dry-run` / `--print-ssh` / `--verbose` are
  **permanent interface, not bring-up scaffolding**, and the 7-step live UAT was written as
  part of the phase. `k8s.io/client-go` is deliberately NOT a dependency — the kubeconfig
  structs are hand-rolled on `gopkg.in/yaml.v3`.
- Ships in **v0.8.6**. See `docs/k8s-reverse-tunnel.md` for the operator runbook and
  `docs/superpowers/specs/2026-08-26-k8s-reverse-tunnel-design.md` for the design.

**Phase 129 (2026-08-26) — Declarative MITM intercepts: profile-declared host→action rules replace the hardcoded rickroll (complete):**
- `spec.network.mitm.intercepts` replaces the compiled-in, unconditional `google.com` → Rickroll
  easter egg (`sidecars/http-proxy/httpproxy/proxy.go`, deleted) with a declarative list of named
  host→action rules an operator authors: `redirect` (301 + `Location`) or `respond` (canned
  status/body/contentType). **Opt-in, off by default** — this is the one place the repo departs
  from its usual "dormant ⇒ byte-identical" rule; a profile with no `mitm:` block gets zero
  interception, and the Rickroll stops firing fleet-wide on the next `km create`. Accepted and
  deliberate. `apiVersion` stays unbumped (purely additive).
- **Migration path:** the egg survives as `profiles/base/mitm-rickroll.yaml`, an abstract fragment
  any profile opts into with one `extends:` line. `profiles/mitm-demo.yaml` is the worked
  example — it extends the fragment, adds its own `respond` rule, and shows the off-switch
  (`- name: rickroll` / `enabled: false`) commented out.
- **Precedence (unconditional, cannot be overridden by a profile):** deny gate →
  Bedrock/Anthropic/OpenAI metering + GitHub repo filter → operator intercepts → general
  allowlist. An intercept naming a metering/GitHub host is silently dead (`km validate` WARNs);
  an intercept for a host absent from the allowlist still fires — the useful case for a canned
  error on a host the sandbox can't otherwise reach. No `block` action: `deniedHosts` already
  blocks with strictly stronger semantics (ahead of everything, broader subdomain match,
  runtime-appendable via `km-netpolicy`) — a second, weaker way to block would be a footgun.
- **Inheritance resolves by name, last-wins, whole-entry** (not a field merge) — Phase 117 unions
  lists, so without whole-entry override a leaf could never turn an inherited rule off.
- **Matcher narrowing is a bug fix.** The deleted regex (`^(www\.)?google\.com`) was unanchored at
  the end, so `google.com.evil.example` was also Rickrolled. The new matcher reuses
  `IsHostAllowed` semantics (case-insensitive, port-stripped, leading-dot subdomain match, no
  regex, no `*`) and rejects the lookalike; pinned by a unit test.
- **Transport is base64 JSON** in a conditional `/etc/systemd/system/km-http-proxy.service.d/mitm.conf`
  drop-in (`KM_MITM_INTERCEPTS`) — load-bearing, since an unquoted systemd `Environment=` splits on
  whitespace and a `respond.body` has spaces. The sidecar fails toward **zero intercepts** on a
  decode/parse error, never a partial rule set. Under `enforcement: ebpf`/`both`, intercept hosts
  are threaded into `--proxy-hosts` but DNS is deliberately NOT widened — a host outside
  `allowedDNSSuffixes` never resolves and the rule silently never fires (`km validate` warns).
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.** All three, because
  three things land together: the sidecar binary, the profile schema field the create-handler's
  bundled `toolchain/km` validates, and the userdata drop-in the create-handler zip renders.
  **`km init --sidecars` alone is actively harmful mid-rollout** — the new sidecar binary has no
  rule set to read yet, so a sidecar-only refresh drops the Rickroll on a live box while the
  profile still cannot declare it back until the full sequence completes. No Terraform module,
  DynamoDB table, IAM policy, Lambda bridge, or SES resource changes; userdata goldens are
  untouched in the dormant case. Existing sandboxes keep today's behaviour until
  `km destroy && km create`.
- See `docs/mitm-intercepts.md` for the full operator runbook and
  `docs/superpowers/specs/2026-08-25-mitm-intercepts-design.md` for the design.

**Phase 128 (2026-08-26) — Wiz Runtime Sensor on EC2 sandboxes + `iam.allowedSecretPaths` made real (complete; live UAT pending):**
- Opt-in `profiles/base/security/wiz.yaml` fragment installs the **Wiz Runtime Sensor** as a root
  systemd daemon, so an autonomous agent's process/file/network activity is visible in the
  operator's Wiz tenant. **Dormant by default** — a profile that does not `extends:` it is
  byte-identical to Phase 127. Composes via Phase 117 inheritance; lists union.
- **The enabling fix: `iam.allowedSecretPaths` never worked on EC2.** It rendered an
  `aws ssm get-parameter` loop into userdata (`userdata.go:495`) but was NEVER plumbed into the
  ec2spot role — which granted `ssm:GetParameter` on two hardcoded resources only. **It does not
  degrade: the bootstrap runs under `set -euo pipefail` (`userdata.go:36`) and the fetch is a
  standalone assignment, so a denied call ABORTS THE BOOT** before the `if [ $? -eq 0 ]` guard,
  which is dead code on the failure path. `profiles/base/gpu/serve.yaml:214` had already recorded
  this empirically. Two shipped profiles (`gpu-llama-{12x,48x}`) declared a path no module version
  ever granted; both are stripped.
- **`{{prefix}}` token is a SECURITY GUARD, not ergonomics.** Every `allowedSecretPaths` entry must
  start with `{{prefix}}/`; anything else is a hard `km validate` error. Requiring prefix-relative
  paths makes it structurally impossible for a profile to grant the sandbox role SSM access outside
  its own install's namespace. Validation is **shape-only** (works with no configured install);
  expansion happens at compile time and **fails loud** — an unset `KM_RESOURCE_PREFIX` errors rather
  than defaulting to `km`, which on a non-default install would render IAM against a SIBLING
  install's namespace. **`cmd/create-handler` never calls `profile.Validate`**, so on the remote
  path the compiler-side `HasPrefix` guard in `InterpolateSecretPaths` is the ONLY barrier — it is
  deliberately exactly as strict as the validator.
- **`infra/modules/ec2spot/v1.4.0`** (new immutable dir): additive `secret_paths` var (`list(string)`,
  default `[]`) + a count-gated `aws_iam_role_policy` granting `ssm:GetParameter` on exactly those
  paths. Empty list creates NO resource. Pin bumped in `locals.substrate_module_versions`. The
  compiler emits `secret_paths` into `module_inputs` **only when non-empty**.
- **DNS is the exception to the root-bypass rule — the trap that nearly shipped.** Root bypasses the
  iptables DNAT (`userdata.go:4532`, uid-0 RETURN) and the eBPF cgroup, so the fragment needs no
  `allowedHosts` — **but DNS is neither uid- nor cgroup-scoped.** `userdata.go:4737` rewrites
  `/etc/resolv.conf` to `127.0.0.1` and the resolver NXDOMAINs non-allowlisted names for everyone,
  root included. The fragment therefore declares `allowedDNSSuffixes: [".wiz.io"]`. Symptom when
  missing is **misleading**: `km-init.sh` is `set -e`, so a blocked lookup aborts the remaining
  initCommands and userdata reports `No init script found in S3 (skipped)`.
- **Credential naming is load-bearing, not cosmetic.** userdata derives an env var as
  `basename | upper | '-'→'_'`, so `{{prefix}}/wiz/wiz-api-client-id` yields exactly
  `WIZ_API_CLIENT_ID` — the name Wiz's installer reads. No glue between fetch and install, and the
  credential never lands in `/etc/sandbox-secrets.env` (`0440 root:sandbox`), which the monitored
  agent could read. Use a **`SENSOR`**-type Wiz service account, not a general API client.
- **NOT tamper-proof, deliberately.** On `privileged: true` the agent has sudo and can stop, mask, or
  `chattr -i` anything. L0 hardening is a systemd drop-in (`Restart=always`) plus `chattr +i` — the
  installer owns `wiz-sensor.service`, so km adds an override, which also survives package upgrades.
  **The real control is `privileged: false`**; detection belongs off-box in the Wiz tenant.
- **Three Wiz integrations now exist and point in three directions** — km pulls (`checks.triggers`,
  Phase 116), Wiz pushes (`webhooks:`, Phase 127), and a sandbox reports to Wiz (this phase). They
  share no code or config. See `docs/wiz-sensor.md` § 7.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — no
  sidecar binary changed, and the new IAM statement needs a full terragrunt apply). Existing
  sandboxes keep the old role until `km destroy && km create`. **Prerequisite ordering is
  load-bearing:** create the two SSM parameters BEFORE any profile extends the fragment, or every
  sandbox using it fails to boot.
- **Residual risk (accepted):** live UAT is not covered — above all Wiz-sensor/km-eBPF coexistence
  (km loads cgroup BPF + SSL uprobes; the sensor loads its own kprobes), which no code review
  settles. Two tenant unknowns remain open: `WIZ_DOMAIN`/region, and the connector's inventory sync
  cadence, which bounds whether a short-lived sandbox is ever tag-correlated.
- See `docs/wiz-sensor.md` for the operator runbook and
  `docs/superpowers/specs/2026-08-25-wiz-sensor-design.md` for the design.

**Phase 127 (2026-08-25) — Generic webhook ingress bridge, Wiz first source (complete):**
- A third-party SaaS POSTs a payload to a new km-owned Lambda (`km-webhook-bridge`,
  Function URL), which authenticates it, drops replays, matches operator-declared rules, and
  dispatches a prompt to a named sandbox alias — warm via a per-sandbox SQS FIFO queue, cold via
  EventBridge `SandboxCreate`. First source: **Wiz** — an Automation Rule fires on an Issue or
  Threat and the `ir-bot` sandbox runs `/triage` against the payload. This is the **push**
  counterpart to the existing Phase 116 **pull** ingress (`checks.triggers`); the two share the
  identical `alias`/`profile`/`on_absent`/`cooldown_seconds`/`prompt` vocabulary by design, so an
  operator learns one dialect for both directions.
- **Dormant by default.** Absent `webhooks:` in `km-config.yaml` ⇒ `KM_WEBHOOK_SOURCES` unset ⇒
  the bridge drops everything and `km doctor` skips the whole group with zero AWS calls.
- **No CLI verb, by explicit decision.** Configuration is `km-config.yaml` only — the opposite of
  `km slack`/`km github`/`km h1`, which exist because of App manifests, OAuth, and token rotation.
  A webhook has a URL and a shared secret; neither needs a verb.
- **Wiz payloads are ours to define, not Wiz's.** Wiz Automation Rules author their own request
  body as an operator-editable Mustache template, so km publishes a canonical
  `docs/webhook-templates/wiz.{issue,threat}.v1.json` contract for the operator to paste in,
  rather than reverse-engineering a fixed Wiz schema. A wrong leaf variable is fixed by editing
  the pasted template against Wiz's own variable picker, never by a Go patch.
- **Wiz offers no HMAC/signature and no delivery-id header** — confirmed against several
  third-party integration guides. The template-injected
  `"delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}"` is the deliberate
  replacement for a missing delivery id; the bearer token minted into SSM is therefore the
  **entire** auth boundary. Wiz also offers a client-certificate option that is unusable here — a
  Lambda Function URL cannot terminate mTLS — and the operator has confirmed mTLS is never
  required, so no ALB/API Gateway fronting is planned.
- **Four storm-control layers, cheapest first:** replay nonce (keyed on `delivery_key`) → cooldown
  (`group_by`, the coalescing lever — first alert of a burst wins, the rest suppressed for the
  window; this is dedup-by-grouping, NOT true batching, which stays out of scope) → an
  install-wide rate ceiling shared across every configured source (counted only AFTER replay and
  cooldown, so suppressed duplicates cost nothing) → the Phase 121 per-sandbox action-quota gate
  (`onBreach: freeze`). The first three fail OPEN on a store error (never strand a real alert on a
  transient DynamoDB blip); the quota gate is the one layer that actually stops runaway spend when
  it trips, and it is **warm-path only** — a cold-create dispatch has no sandbox yet to have a
  usage history against, so it is quota-ungated by construction, not an oversight.
- **The `{{...}}` non-collision:** Wiz renders its own Mustache variables before sending; km then
  expands its own `{{severity}}`/`{{entity.name}}`/`{{raw}}` syntax against the already-rendered
  envelope when building the agent prompt. The two never see each other's raw tokens despite
  looking identical — worth stating explicitly, or it costs someone an hour of confused staring.
- **No threads table, no reply-back leg, no `km-webhook` sandbox-side helper** — unlike GitHub/H1,
  a webhook source is one-way by design (confirmed with the operator, 2026-08-25): nothing to
  reply to, no conversation to persist. The agent reports out via `km-slack` instead if needed.
- **`km init` mints the bearer token once and never overwrites it.** The absence check is narrow
  (only a genuine "parameter not found" counts as safe-to-mint; any other SSM read error is
  surfaced, never treated as absence) so a transient read failure can't silently overwrite a live
  token out from under a configured Wiz integration. Deliberate rotation is an explicit
  `aws ssm put-parameter --overwrite` plus a Wiz-side paste — see `docs/webhook-ingress.md` § Token
  rotation.
- **Deploy surface, order load-bearing:** `make build` FIRST (the binary carries the new
  `regionalModules()` entries — `lambda-webhook-bridge` plus a `webhook-inbound` variant of the
  shared inbound DLQ; a stale binary silently skips them, `km doctor` stays green while nothing was
  provisioned) → `make build-lambdas` (new bridge zip, and refreshes the create-handler for the new
  `notification.webhook.inbound` profile field + poller userdata) → `km init --dry-run=false`
  (**not** `--sidecars` — Lambda/Function URL/IAM/DLQ/env block all need a full terragrunt apply; a
  scoped `km init --webhooks` sugar alias covers a fast env+IAM-only refresh once the module
  already exists). Existing `ir-bot` needs **one** `km destroy && km create` to gain the
  per-sandbox queue and the `km-webhook-inbound-poller`.
- **Reused, not rebuilt:** the shared nonces table + `CheckAndStore`, the `alias-index` GSI, the
  Phase 99.1 shared inbound DLQ pattern, the EventBridge cold-create path, `WireActionQuota`, and
  the Phase 109 self-heal (a terminal `ErrNoResumableInstance` deletes the stale DynamoDB row and
  falls through to cold-create, so an `ir-bot` terminated out from under km doesn't shadow creation
  forever).
- See `docs/webhook-ingress.md` for the full operator runbook (Wiz setup, Automation Rule wiring,
  the full `webhooks:` config surface, storm control, deploy surface, token rotation,
  troubleshooting, and known limits).

**Sandbox liveness: VNC + SSH presence signals (2026-08-23):**
- `km-presence` gained **signal 6 (KasmVNC viewer attached)** and **signal 7 (SSH session
  established)**, closing a gap where an interactively-used sandbox was reaped by
  `spec.lifecycle.idleTimeout` while someone was working in it. Both were live-verified on a
  desktop sandbox; the daemon now ORs seven signals instead of five.
- **Root cause is utmp, not the timer.** Signal 1 reads `who`, and `sshd` writes a utmp record
  ONLY for PTY sessions. KasmVNC runs as a systemd service (never a login session), and VS Code
  Remote-SSH runs `vscode-server` over a **non-PTY** exec channel (`sshd: user@notty`). Measured
  live: non-PTY session ⇒ `who=0`, PTY session ⇒ `who=1`. So a connected user looked idle on all
  five original signals, with an **empty CloudWatch audit stream** as the only trace (the daemon
  emits a heartbeat only when a signal is active — an empty stream is proof of zero signals, not
  of a broken pipe).
- **The VS Code case was INTERMITTENT**, which is what made it hard to see: opening VS Code's
  integrated terminal allocates a PTY and makes the same session visible to signal 1. Whether a
  box survived depended on an unrelated UI panel being open.
- **Both signals detect the live socket, never the process.** `pgrep vscode-server` is the
  obvious check and is WRONG — VS Code deliberately leaves that server running after a client
  disconnects so reconnects are fast, so it would latch the sandbox awake forever. Likewise VNC
  is matched by owning process (`Xvnc`), NOT by port: the profile sets
  `network.websocket_port: auto`, so 8444 is observed behaviour, not a contract.
- **Both fail idle** (a missing/erroring `ss` reports no session). Deliberate: a reaped desktop
  costs one `km resume`, whereas a signal that can never go negative silently disables idle
  teardown fleet-wide and leaks instances. Every negative path is unit-tested for this reason.
- **Signal 7 matches BOTH `(("sshd",` and `(("sshd-session",`** — OpenSSH 9.8+ split the session
  process out and renamed it. Matching only the first would silently stop firing on an AMI bump.
  The box runs 9.6 today, so the second form is forward-compat and covered by a test.
- Signals 6 and 7 are kept **independent** (each runs its own `ss`; ~4ms/60s tick) so a future
  `km doctor` can report WHICH session type is holding a sandbox awake. A cross-independence test
  pins that they never trigger each other.
- **Consequence:** an open browser tab or VS Code window now pins a sandbox alive indefinitely —
  same tradeoff as leaving a `km shell` open. `spec.lifecycle.ttl` is unaffected and is now the
  real backstop against forgotten sessions.
- **Pure `cmd/km-presence` sidecar change.** No SandboxProfile schema change, no TF/DDB/Lambda/IAM
  change, no userdata change (the km-presence.service unit already runs `User=root`, which signal
  6/7 need for `ss -p` to resolve process names).
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-presence` sidecar;
  NOT `--dry-run=false`). Existing sandboxes keep the five-signal daemon until
  `km destroy && km create` — the binary is fetched at boot. Interim mitigation on a live box:
  raise `idleTimeout`, `km extend <id> --idle <dur>`, or keep a `km shell` open.
- See `docs/desktop.md` § Idle timeout and desktop sessions and `docs/vscode.md` § Idle timeout
  and Remote-SSH sessions.

**Phase 126 (2026-08-22) — Cross-account capacity borrowing: launch sandboxes into a linked capacity account (live-verified 2026-08-24, except vLLM serving):**
- A SandboxProfile can launch its EC2 box into a *different*, pre-linked AWS account to
  borrow that account's vCPU quota, while the one km control plane — state, DynamoDB, budget,
  `km list`, every bridge — stays home. Dormant by default: absent `spec.runtime.launchAccount`
  ⇒ byte-identical to Phase 125. No `apiVersion` bump. Motivating fact: a GPU vCPU quota
  (`L-DB2E81BA`) can be far larger in a second account than in the account km actually runs
  in. Nothing about the mechanism is GPU-specific — the launcher role's `--instance-types`
  allowlist is the only thing that scopes a given link to GPU families.
- **Enrollment is two commands, two credential sets.** `km account add <name> --aws-profile
  <target-admin> --trust <home-account-id> --instance-types ... [--provision-network
  --az-count N] [--dry-run=false]` runs real Terraform against the TARGET account (its own
  admin creds), provisioning a bounded launcher role (trusted by three home-side principals —
  operator/local-create, `*-create-handler`, `*-ttl-handler` — each `sts:ExternalId`-gated), a
  permissions-boundaried box role, a results bucket, and optionally one subnet **per AZ**
  (never a single subnet — that collapses the Phase 124 AZ sweep to one attempt) + EFS. It
  writes an owner-only handoff fragment to `~/.km/account-links/<name>.link.yaml`. `km account
  register <name> --from-fragment <path> --aws-profile <home>` then runs with HOME creds:
  writes the `km-config.yaml` link entry, the external id as an SSM SecureString, and exactly
  one read-only (`s3:GetObject`/`s3:ListBucket`, never `PutObject`) grant on the home
  artifacts bucket for the box role. `km account list` / `km account rm [--target-aws-profile]
  [--purge-backend]` round it out.
- **The enrollment unit's Terraform state lives IN THE TARGET ACCOUNT** — `km account add`
  creates an S3 bucket (`tf-{prefix}-linkstate-{target-acct}-{regionLabel}`) + DynamoDB lock
  table via the AWS API before the first terragrunt init, shared per (prefix, account, region)
  and keyed per link. There is no local Terraform state anywhere in this project.
- **The load-bearing terragrunt fix:** the sandbox template's `include "root"` needed
  `merge_strategy = "deep"` — without it, a child `generate "provider"` block hard-errors
  every sandbox render (`Detected generate blocks with the same name: [provider]`), not just
  cross-account ones. The child block reproduces root's full generated output (incl. both
  `required_providers` pins, which deep-merge would otherwise drop) plus a conditionally
  interpolated `assume_role` stanza, proven byte-identical to root's own output in the
  dormant case against the real pinned terragrunt v0.99.1 binary.
- **A genuine apply-time bug found and fixed along the way:** the link record carries subnet
  ids but no VPC id anywhere. Without resolving it, the EC2 module would self-provision a
  disconnected VPC and then try to place the ENI in a subnet from the link's real, different
  VPC — a hard `terraform apply` failure. Fixed via one `DescribeSubnets` call against the
  assumed-role config, resolved once per launch before any artifact is compiled.
  **Known residual, non-blocking gap:** the link's pre-provisioned `SecurityGroupID` goes
  unused — the EC2 module always self-creates its own per-sandbox SG regardless of account,
  matching home-launch behavior (there is no "reuse an existing SG" input anywhere today).
- **The capacity/quota gate reads the LINKED account**, via a second, fail-closed assumed-role
  AWS config — a failed assume aborts the create/report rather than silently falling back to
  the home account's headroom. The `{prefix}-capacity` DynamoDB table stays in the home
  account for every launch; only the row key is namespaced by account id for a linked launch
  (home stays permanently un-namespaced — success rows carry no TTL, so "namespacing home for
  symmetry" would silently orphan months of sticky-AZ history).
- **Both teardown paths are account-aware.** `km destroy` (including its cold-clone fallback)
  reads the sandbox's DynamoDB row for its `launch_account` BEFORE the home-account tag scan,
  which can never see a resource in a different account. The expiry Lambda renders a
  conditional `assume_role` provider block sourced from the link's own region (never its own
  cold-start env vars, which are Lambda-instance-wide). Both fail closed — hard error — on an
  unknown link or a failed external-id read, never a silent fall-through to a home-account
  provider that would report success while the linked instance kept billing.
- **`km doctor` gains four read-only checks**, all silently skipped with zero AWS calls when
  no `launch_accounts` are configured: link structurally well-formed; launcher role actually
  assumable (forces real credential resolution, not just config construction); artifacts
  grant present and not widened beyond the exact read-only pair; no orphaned linked-account
  EC2 instances (a leaked box `km list` cannot see, billing silently).
- **Security model:** the target account is exempt from Service Control Policies, so the
  launcher role's own IAM policy is the ENTIRE containment boundary — no second layer. The
  external id is the confused-deputy protection. The box role is capped by a permissions
  boundary. Every cross-account data path is read-only in both directions (box→home artifacts:
  read-only; home→box results: read-only; box→its own results bucket: read-write, same
  account only). **Operator-safety note:** enrolling an org **management** account as the
  target is possible but discouraged — SCPs never apply there, so this feature's usual "SCP is
  a second layer" assumption doesn't hold; prefer a dedicated member account.
- **Deploy surface (three independent paths — do not conflate):** `km account add/register/
  list/rm` + the profile field + local create-flow wiring are pure operator-binary changes
  (`make build`). `spec.runtime.launchAccount` reaching a **remote** `km create` additionally
  needs `make build-lambdas` + `km init --dry-run=false` (the create-handler's bundled
  `toolchain/km` must be refreshed). The expiry Lambda's cross-account teardown path
  (`KM_LAUNCH_ACCOUNTS` env + the `sts:AssumeRole`/`ssm:GetParameter`+`kms:Decrypt` IAM
  grants) needs the same `make build-lambdas` + `km init --dry-run=false`. The target
  account's own roles/network/bucket are provisioned by `km account add` running terraform
  directly against that account — **never** by `km init`, which never touches the target
  account at all (same standalone shape as `km cluster add`). `--sidecars` is not sufficient
  for any of the above — nothing in this phase touches a sandbox-side helper binary.
- **Live UAT 2026-08-24 (`126-UAT.md`):** cross-account create AND teardown proven; `km capacity`
  reads the target (768 vCPU vs 64 home); `km doctor`'s four checks green incl. the artifacts
  grant surviving `km init`; containment matrix 8/9 (Finding L1: `ec2:RunInstances` is not
  tag-gated, so an untagged box is un-reapable AND invisible to the orphan check — **mitigated
  2026-08-25 by detection**, because tag-gating it is IMPOSSIBLE: for the `instance/*` resource
  of a `RunInstances` call AWS populates only `ec2:AvailabilityZone` and `ec2:InstanceType`, so
  an `aws:RequestTag` condition there is unsatisfiable and breaks every launch — see the
  `LaunchOnlyGpuBoxes` comment and commit `0d362066`. **`simulate-principal-policy` will
  evaluate a key AWS never populates at call time, so a simulator verdict proves what the
  policy SAYS, not what AWS can ENFORCE.**). **vLLM serving
  a token is still unproven** — every allowlisted GPU shape probed `InsufficientInstanceCapacity`
  in every us-east-1 AZ (Finding L4), incl. the FP8 single-GPU fallback; ca-central-1 has no L40S
  at all and its L4s are dry; us-west-2/us-east-2 are quota-walled at 0 (requests pending).
- **Two Phase 124 defects found here, not Phase 126 bugs — they bite home launches too:**
  **L3 (root cause) — FIXED 2026-08-25** `rankScore` (`pkg/capacity/rankaz.go`) checked
  `LastSuccessAt` before the fresh-ICE branch and returned unconditionally; success rows carry no
  TTL, so an AZ that ever succeeded out-ranked every other AZ forever however often it later ICEd
  — and `km capacity` disagreed with the ranker, showing the AZ as `recently-dry` while the launch
  path still picked it. **Freshness is now evaluated before stickiness** (an EXPIRED ICE still
  lets stickiness apply), `ComputeCapacityVerdict` carries a cross-reference so the two cannot
  drift, and two regression tests pin it. NOTE: the pre-fix `TestRankAZs_ICEStickySuccess` gave
  only success-ONLY and ICE-ONLY AZs, never one holding both — which is how a green suite hid
  this. Any new ranking rule needs the both-attributes case. **L2 — FIXED 2026-08-25** terraform retried ICE internally, so km never saw the error, the
  sweep could not rotate, and the Lambda died at 900s leaving the stale lock the next run
  reports. `aws_instance.ec2_ondemand` now has `timeouts { create = var.ondemand_create_timeout }`
  (3m) — the on-demand twin of Phase 124's spot waiter; ICE and WaiterTimeout are both
  iterate-class so either error shape rotates. **Uncovered two worse problems:** the guard test
  asserted the defect (`_OnDemandNoTimeout` required NO timeouts block, on a rationale CloudTrail
  disproves), and `pkg/compiler/ec2spot_timeout_test.go` had been reading `ec2spot/v1.2.0` while
  the live pin was `v1.3.0` since Phase 125 — every assertion in it was inert. Retargeted + a
  drift guard (`TestEC2SpotModuleDir_TracksLivePin`) parses the pin from the sandbox template.
  **L5 — FIXED 2026-08-25** a create killed mid-apply leaks its 300GB volume: `km destroy`
  reports success (the volume never reached state) and `km doctor` stayed green (its EBS check
  is scoped to *untagged* volumes, and these are correctly tagged). New
  `checkLaunchAccountOrphanVolumes` reports unattached volumes in a linked account that home
  does not know about, read-only by construction (`LaunchAccountVolumeAPI` has no
  `DeleteVolume`).
- See `docs/cross-account-capacity-borrowing.md` for the full operator runbook.

**Egress deny lists + runtime narrowing (2026-08-21):**
- **`spec.network.egress.deniedDNSSuffixes` / `deniedHosts`** block destinations outright, ahead of
  the allowlist. A deny beats every allow — including the `*` wildcard, the GitHub repo-filter
  carve-out, the OpenAI budget path, and the Bedrock/SES/Anthropic MITM interceptors. The HTTP deny
  gate is registered FIRST because goproxy dispatches first-match and every later handler is a
  carve-out; a deny evaluated after them would be silently bypassable.
- **Deny matching is deliberately BROADER than allow matching** — a bare entry covers subdomains
  (`evil.example.com` also blocks `api.evil.example.com`). Strictness on an allowlist permits less;
  the same strictness on a denylist fails OPEN.
- **`spec.network.egress.runtimeDeny: true`** lets a RUNNING sandbox append denies to itself from
  user-land via **`km-netpolicy deny|list`** (`/opt/km/bin`), effective within ~1s, no restart.
  Narrow-only is enforced twice: the only operation is *append* (there is no removal verb, and a
  test fails if one is added), and the file carries `chattr +a` so the kernel refuses truncate,
  unlink, rename, and attribute-clear. Lives under `/var/lib`, NOT `/run` — a reboot that dropped
  accumulated denies would WIDEN the policy.
- **`pkg/netpolicy`** is now the single canonical deny matcher, replacing three near-copies in
  `sidecars/dns-proxy`, `sidecars/http-proxy`, and `pkg/ebpf/resolver`. All three consult it per
  decision rather than snapshotting, which is what makes runtime denies take effect live.
- **The eBPF resolver reads the runtime file too.** In `ebpf`/`both` mode the bootstrap deliberately
  leaves `km-dns-proxy` disabled and the resolver serves DNS — so without this, `km-netpolicy deny x`
  would report success while `x` stayed resolvable.
- **Guard:** `/etc/km/netpolicy.env` mirrors the profile-baked lists, because those otherwise live
  only in the proxy units' `Environment` blocks and `km-netpolicy list` would report `(none)`.
- **Limit:** on `privileged: true` the sandbox has sudo and can clear `+a` — but it can equally stop
  the proxies, so the guarantee is meaningful on unprivileged boxes.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` —
  that does not refresh the create-handler zip that renders userdata). Dormant by default; existing
  sandboxes keep their config until `km destroy && km create`. Live UAT passed.
- See `docs/egress-deny-lists.md`.

**Phase 125 (2026-08-19) — Per-profile private-subnet sandboxes with per-AZ NAT gateways (code-complete; live UAT pending):**
- Two decoupled, dormant-by-default toggles: install-level `network.nat_gateway`
  (`km-config.yaml`) controls whether NAT/EIP infrastructure **exists**; per-profile
  `spec.network.privateSubnet` (SandboxProfile) controls whether **this** sandbox's ENI lands
  private. Both absent ⇒ byte-identical to Phase 124. No `apiVersion` bump.
- **One NAT gateway + one EIP per AZ** (4 total), each sitting in the public subnet of the AZ
  it serves — not one shared NAT — because Phase 124's AZ-failover sweep rotates launches
  across all 4 AZs, and a shared NAT would add cross-AZ data-transfer charges to every byte and
  make one AZ a SPOF for the whole install's internet access.
- **`infra/modules/network/v1.1.0`** (new immutable dir): `aws_route_table.private` becomes
  `count`-based (one per private subnet — a single route table cannot hold four different
  `0.0.0.0/0` NAT targets), with `moved` blocks so the first apply against an existing install
  shows moves, not destroy+create. Plural outputs (`private_route_table_ids`,
  `nat_gateway_ids`, `nat_eip_public_ips`) replace the old singular ones.
  **The route-table split is UNCONDITIONAL** — it fires the moment the live unit sources
  v1.1.0, independent of the NAT toggle, so the first `km init` after this phase shows a
  non-empty (additive, non-destructive) plan even for an operator who touches neither toggle.
- **`infra/modules/ec2spot/v1.3.0`**: `public_subnets` renamed to `sandbox_subnets` (truthful
  for either placement) and a new `associate_public_ip` bool (default `true`, `false` for
  private placement — a public IPv4 in a private subnet is both unroutable and billable).
- **REQ-125-SUBPIN:** `infra/templates/sandbox/terragrunt.hcl`'s single shared module-version
  literal is now a per-substrate `locals.substrate_module_versions` map + lookup — this also
  repairs a pre-existing bug where the shared literal pointed the (never-tested) ECS substrate
  at a nonexistent module directory. Scope boundary: this fixed the PIN only, not ECS substrate
  functionality — ECS is not in use and remains unproven end to end.
- Go plumbing: `NetworkOutputs` gains `PrivateSubnets`/`NATGatewayIDs` (both the local
  `outputs.json` path and the S3 tfstate fallback used by `--remote`); placement is resolved
  **once**, into `compiler.NetworkConfig.SandboxSubnets`, before the Phase 124 AZ sweep — the
  sweep itself was retargeted (not forked) to read/write `SandboxSubnets` exclusively;
  `capacity.RankAZs` drops any AZ with no NAT gateway for a private sandbox.
- **Guards:** `km create` fails fast (before any artifact/upload) when a private profile hits a
  NAT-less install, naming `network.nat_gateway` and the fix command; `km init` **refuses** to
  disable NAT while any sandbox row is `network_placement=private` and `status=running` (no
  override flag, by design); two `km doctor` WARNs — NAT enabled but idle (cost + "safe to
  disable"), and a running private sandbox with NAT off. `network_placement` is a
  schema-on-write DynamoDB attribute on `{prefix}-sandboxes`, threaded through the shared
  marshal/unmarshal chokepoint so it survives pause/resume/extend/ttl-handler.
- **Cost:** ~$132/month baseline for 4 AZs plus $0.045/GB processed — e.g. a GPU profile
  pulling 300GB of weights costs ~$13.50 in NAT processing alone. This is the entire reason the
  toggle is reversible: disabling NAT leaves the private subnets as free, routeless islands.
- **Residual risk (accepted pending live UAT):** `cmd/ttl-handler/main.go` renders its
  destroy-placeholder `main.tf` against a frozen `ec2spot/v1.0.0` pin regardless of which
  version a sandbox was created against; an automatic TTL expiry of a v1.3.0-created private
  sandbox is the only path that actually crosses this version boundary (`km destroy` reuses the
  create-time `terragrunt.hcl` and never crosses versions). Resolved analytically at HIGH
  confidence in `RESEARCH.md` Finding 1 (`terraform destroy` operates on state, not
  destroy-time config, and the pin has already survived two prior bumps); live UAT step 6
  proves it. See `docs/private-subnet-nat.md` for the current UAT status.
- **Deploy = `make build` (BEFORE `km init` — binary carries the new `NetworkOutputs`
  extraction and the `KM_NAT_GATEWAY_ENABLED` export; a stale binary silently skips both) +
  `make build-lambdas` + `km init --dry-run=false`.** NOT `--sidecars` (no sidecar binary
  changed; the NAT/EIP/route resources need a full terragrunt apply). Existing sandboxes are
  unaffected and keep public placement until `km destroy && km create`.
- See `docs/private-subnet-nat.md` for the full operator runbook (toggles, cost, the one-time
  route-table split, reversal, guards, deploy surface, scope fences) and
  `.planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md`
  for the live UAT record.

**`km-github commit` — verified, bot-attributed signed commits from a sandbox (2026-07-02):**
- New `km-github commit` sidecar verb creates a **GitHub-SIGNED, `klanker-maker[bot]`-attributed** commit (committer `GitHub`, `verified:true reason:valid`) via the GraphQL **`createCommitOnBranch`** mutation. This is the ONLY commit path that auto-signs: a sandbox's local `git commit` is unsigned, and the low-level REST `POST /git/commits` is bot-attributed but `verified:false reason:unsigned`. The mutation signs with GitHub's key and attributes the commit to the **token's own identity** (no hardcoded bot email → portable across installs) and supports multi-file commits in one commit.
- **Usage:** `km-github commit --repo O/R --branch BR [--parent SHA] --message-file MSG -- <repo-relative files...>` → prints the new commit OID to **stdout**, the `verified=… reason=… author=… committer=…` line to **stderr**. Then sync the local worktree: `git fetch origin && git reset --hard origin/BR`. The message file's first line is the headline, the rest (leading blank lines stripped) the body.
- **Precondition — `contents:write`:** the mutation needs a token with `contents:write`, minted only when the sandbox profile's GitHub permissions include **`push`** (`CompilePermissions`, `pkg/github/token.go`). Without it the mutation 403s. See `docs/github-app-permissions.md` § `contents`.
- **`--parent` gotcha:** `--parent` force-resets the branch to SHA first — if a PR is already open and the head is reset to exactly the base SHA, GitHub auto-closes it ("no commits between base and head"). `gh pr reopen`, or create signed commits BEFORE opening the PR.
- **Durable Go form (not the interim bash helper the originating spec proposed):** `cmd/km-github` already has SSM token-fetch + subcommand dispatch + test seams, so the Go verb reuses all of it and drops the bash/jq/gh/base64 + exported-`gh`-function fragility — and avoids churning the byte-identity userdata goldens. New `commit.go` + `commit_test.go`; dispatch case + usage line in `main.go`.
- **Pure `cmd/km-github` sidecar change.** No SandboxProfile schema change, no userdata change (userdata already downloads `km-github` from `s3://…/sidecars/km-github`), no Lambda/IAM/DDB change, no golden change.
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-github` sidecar; NOT `--dry-run=false`, NOT `--lambdas`). Existing sandboxes gain the verb on `km destroy && km create` (the sidecar is fetched at boot).

**Phase 124 (2026-06-28) — Platform-wide AZ failover + capacity feasibility for EC2 launches (code-complete; live GPU UAT pending G-quota):**
- **AZ sweep with classify-and-retry:** `km create` pre-orders the region's AZs via `capacity.RankAZs` (drops non-offering AZs, checks GPU quota, sorts by historical success) then loops classify-and-retry: ICE / SpotPrice / SpotLimit / WaiterTimeout → rotate to next AZ; Quota / Auth / Invalid → **fail-fast** (no AZ rotation helps a quota wall). `sweepDecision(class, attempt, maxAttempts)` is the single classification callsite.
- **GPU quota fail-fast gate in `RankAZs`:** for GPU families (`g4dn`, `g5`, `g6`, `g6e`, `p3`, `p4`, `p5`), `RankAZs` calls Service Quotas for `L-DB2E81BA` ("Running On-Demand G and VT instances") **before** the sweep. Headroom=0 → immediate `*QuotaError` with the quota code and increase URL; this is NOT an ICE error and never triggers AZ rotation. The quota defaults to 0 per-account; verify in the TARGET application account/region, not the org management account.
- **`km capacity [profile | --type t]`:** per-AZ feasibility table. Verdicts: `likely` (offered + quota OK + success signal or no fresh ICE), `recently-dry` (ICE within 45-min window), `not-offered` (absent from `DescribeInstanceTypeOfferings`), `quota-blocked` (GPU + headroom=0), `unknown` (no store signal). "available" is never printed — capacity is probabilistic.
- **`km create --wait-for-capacity[=30m]`:** outer backoff loop — re-sweeps all AZs every 5 minutes until capacity or deadline. Fail-fast errors exit immediately; ICE-class exhaustion waits and re-sweeps. Dormant by default (absent flag = single-sweep, exit on exhaustion).
- **`{prefix}-capacity` DynamoDB table:** records `last_ice_at` (TTL 45 min via `expires_at`) and `last_success_at` per (instance_type, az) pair. `RankAZs` reads it for sticky AZ preference (last-success → prefer; recent ICE → deprioritize). `km doctor` checks table presence and GPU vCPU quota.
- **Deploy-surface order is load-bearing:** `make build` (operator binary, includes new `dynamodb-capacity` entry in `regionalModules()`) MUST run BEFORE `km init --dry-run=false`. A stale binary silently skips the capacity table and IAM — `km capacity` and RankAZs store writes fail at runtime with no obvious error. Correct order: `make build` → `make build-lambdas` (create-handler with nocap classifier) → `km init --dry-run=false`. NOT `--sidecars`. No SandboxProfile schema change, no bridge Lambda change, no sandbox recreate needed. See `docs/operational-gotchas.md` § AZ failover + capacity feasibility.
- **Phase 124.07 (gap-closure) — capacity store feedback loop closed:** `bestEffortRecordCapacity` (in `create.go`) is now wired into the AZ sweep loop — `RecordSuccess` fires on a successful apply; `RecordICE` fires on `ClassICE` failures. Both are best-effort (5s timeout, log.Warn on error, never blocks the create). The create-handler Lambda role is granted `dynamodb:GetItem/PutItem/UpdateItem/DescribeTable` on `{prefix}-capacity` via a new `dynamodb_capacity` policy in `km-operator-policy`. Deploy this gap-closure with `make build-lambdas` + `km init --dry-run=false` (the IAM grant is in the create-handler terragrunt module, not `--sidecars`).

**Phase 122 (2026-06-27) — GPU vLLM model-serving profiles + local-model chat (code-complete; live UAT pending G-quota):**
- 7 GPU SandboxProfiles serve 70B-class local models via **vLLM** on a Deep Learning AMI (Ubuntu 24.04, `ami-0a9d213b92dabc044`) base, weights cached on a 300GB `additionalVolume` (`/data`, `HF_HOME`), served as `--served-model-name local`. Composed from a NEW abstract fragment `profiles/base/gpu/serve.yaml` (Phase 117 inheritance): `gpu-{qwen,llama}-{12x,48x}` + `gpu-glmair-12x` + `gpu-kimidev-12x` + `gpu-glm46-48x` (g6e.12xlarge/48xlarge = 4/8× L40S). Kimi K2 (~1T) out of scope (needs gated P-family).
- **On-box Bifrost gateway (`:8001`) is a CORE multi-provider ROUTER**, not a shim. **O7 discovery:** Codex requires the OpenAI **Responses API** (Feb 2026) and vLLM serves only Chat Completions, so codex cannot point straight at vLLM — Bifrost (**Docker image `maximhq/bifrost:v1.6.0`**, run via a docker systemd unit; LiteLLM = documented fallback) translates Responses→vLLM, serves Anthropic Messages for Claude Code, and routes by the **`provider/model` string the caller sends** (no named-route config): `vllm-local/local` / `bedrock/us.anthropic.claude-sonnet-4-6` (instance-role SigV4, keyless) / `anthropic/claude-sonnet-4-6` (`ANTHROPIC_API_KEY` via SOPS) / `bedrock/openai.gpt-oss-120b` (keyless — GA Aug 2025; **drop the `-1:0` suffix or Bifrost's catalog 404s**) / `openai/gpt-5` (dormant). Cloud routes flow through the MITM proxy → metered automatically. OTLP export is env-driven → `:4318` (`km otel`). The on-box gateway config + codex `localBaseURL: http://localhost:8001/openai/v1` were **validated live against Bedrock in a GPU-free Docker rehearsal** (`.planning/phases/122-*/122-BIFROST-VALIDATION.md`) — the originally-authored config was an invented schema that would have failed every route.
- **Codex repoint is box-global** (`spec.agent.default: codex`, `agent.codex.localBaseURL` → new `synthesizeCodexConfig` `[model_providers.local]` emission): the local model is reachable via VS Code (Continue), Slack chat-with-resume (`codex exec resume`), on-box terminal/headless codex, AND laptop **`km model start <id> [--anthropic]`** (new `internal/app/cmd/model.go`, reuses `runReconnectingPortForward`; `--anthropic` forwards `:8001` and sets `ANTHROPIC_BASE_URL=…/anthropic` for local Claude Code). **claude stays cloud-pointed on-box** → `/claude` (cloud) vs default `/codex` (local 70B) A/B in one channel.
- **Deploy = `make build` (operator binary) + `make build-lambdas` + `km init --dry-run=false`** (remote create — the new `agent.codex.localBaseURL` schema field needs the create-handler `toolchain/km` refreshed, linux/arm64; NOT `--sidecars`). `km create … --local` works with the operator binary directly. No new TF module / DDB table / bridge change.
- **Prerequisite gotcha:** the "Running On-Demand G and VT instances" vCPU quota (`L-DB2E81BA`) defaults to **0** per-account — verify against the TARGET application account/region, not the org management account. Waves 1–2 are committed and green (`go test ./...` 41 ok / 0 FAIL, 20/20 profiles validate); the live UAT (G3–G9) resumes once the G-quota lands. See `docs/gpu-model-serving.md` and the design spec `docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`.

**Phase 92 (2026-05-31) — SandboxProfile spec restructure (complete):**
- `spec.identity:` → `spec.iam:` (with `allowedSecretPaths` declared).
- `spec.cli.notify*` → `spec.notification:` (typed `events` / `email` / `slack` sub-blocks).
- `spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled`.
- `spec.cli.{agent,claudeArgs,codexArgs}` → `spec.agent:` block (`default` / `claude.args` / `codex.args`). `spec.cli` is now `noBedrock`-only.
- Inlined `configFiles["/home/sandbox/.claude/settings.json"]` REMOVED everywhere; settings.json is synthesized from `spec.agent.claude.tools.*` + `trustedDirectories` (canonical `permissions.allow` / `permissions.deny`). Codex `config.toml` is synthesized too — Codex has no native tool gating, so it ships inert hooks + an asymmetry note. See `docs/agent-tool-gating.md`.
- Mixed mode (typed `agent.claude.*` + inlined settings.json) is now SUPPORTED via deep-merge: the compiler merges synthesized typed keys (permissions/trustedDirectories win) ON TOP of the inlined file, preserving operator keys like `enabledPlugins`/`env`/`model` (`compiler.mergeSynthesizedClaudeSettings`). It was previously a hard `km validate` error (the Phase 92 Wave 4 "no-merge" decision), which silently dropped those keys whenever a typed field was set. `km-notify` hooks now include `SubagentStop` (drains a finished Task subagent's transcript into the Slack thread) alongside `Notification`/`Stop`/`PostToolUse`. See `docs/agent-tool-gating.md`.
- Sandbox-side env var names (`KM_NOTIFY_*`, `KM_SLACK_*`, `KM_AGENT`) are UNCHANGED; `apiVersion` stays `klankermaker.ai/v1alpha2`.
- Post-merge: `make build && km init --sidecars` to refresh the management Lambdas.

**Phase 118 (2026-06-24) — Slack trigger allowlist + private per-sandbox channels (complete):**
- Two independent, composable Slack additions; both **dormant by default** (neither set ⇒ byte-identical to Phase 117).
- **Feature A — private channel.** `notification.slack.private` (bool, default `false`) creates the per-sandbox channel as `is_private:true` (was hardcoded public at `pkg/slack/client.go:606`). Threaded a `private bool` through `CreateChannel` + both `SlackAPI`/`SlackInitAPI` interfaces + all call sites (init/rotate pass `false`). Invites unchanged; **no new OAuth scopes** (`groups:*` already requested). Effective only with `perSandbox:true`; create-only (a Phase-104 reused channel is NOT converted public→private). `private:true` + `perSandbox:false` ⇒ `km validate` WARNS.
- **Feature B — Uxxxx trigger allowlist (`allow`).** Gate *who can trigger the bot* by Slack user ID. Install-level `slack.allow` (km-config.yaml → `KM_SLACK_ALLOW` bridge env) and per-sandbox `notification.slack.inbound.allow` (profile → `km-sandboxes` row attr `slack_allow`, comma-joined S → bridge `FetchByChannel` → `SandboxRoutingInfo.Allow`). **Resolution:** non-empty per-sandbox **replaces** install-level; else install-level; else empty ⇒ **everyone allowed** (backward-compatible). Enforced in `events_handler.go` on `event.User`; reject = **silent ignore** (no 👀, no reply, no dispatch; 200). Always enforced, **before** the mention-only filter AND the Phase 91.3 thread-bypass (a non-listed user can't hijack an active thread). Handler order: channel-ownership → allowlist → mention/thread → dedup → dispatch.
- **INVERTED semantics vs GitHub/H1 bridges:** empty `allow` = everyone (allow-by-default), NOT deny-by-default. `isInSlackAllowlist` is only consulted after a `len(allow)>0` guard.
- **Four known-gotcha plumbing items addressed:** config v2→v merge-list entry for `slack.allow` (else silently dropped — with a regression-guard test); `slack_allow` round-trips through `SandboxMetadata` (survives pause/resume/extend); `FetchByChannel` attr-name parity verified against real write/read sites; `km validate` WARNS (not errors). JSON schema gained `private` (slack block) + `allow` (inbound block) — both are `additionalProperties:false`.
- **Deploy:** install-level `allow` → `KM_SLACK_ALLOW` is an env-block change → `km init --dry-run=false` (NOT `--sidecars`; `km init --slack` also covers it), takes effect on the next webhook. Per-sandbox `allow`+`private` → profile schema + create-handler + bridge → `make build-lambdas` + `km init --dry-run=false`; existing sandboxes need `km destroy && km create`. **No apiVersion bump.** See `docs/slack-notifications.md` § Phase 118.

**Phase 119 (2026-06-25) — Slack inbound per-thread parallelism (complete):**
- The per-sandbox inbound poller can now dispatch **multiple distinct Slack threads concurrently** while keeping turns **within a single thread strictly serial** (parallel-across-threads, serial-within-thread). **Dormant by default** (cap unset/`1` ⇒ byte-identical to Phase 118).
- **Two layers.** (1) **Bridge** enqueues to the per-sandbox FIFO using the **thread timestamp** as `MessageGroupId` (was the sandbox id) at both Send sites — FIFO orders within a group, parallelises across groups; behavior-neutral when cap=1. (2) **Poller** rewritten as a **bounded-concurrent ack-after dispatcher**: reads `KM_SLACK_MAX_CONCURRENCY` (default 1), long-polls up to `--max-number-of-messages MAX` (clamped 10), backgrounds each turn into its own RUN_DIR (`…Z-$$-$RANDOM`), and enforces a counting semaphore (`while [ "$inflight" -ge "$MAX" ]; do wait -n; done`).
- **Field:** `notification.slack.inbound.maxConcurrentThreads` (`*int`, default `1` = serial = dormant). **Emit-only-when->1:** the compiler writes `KM_SLACK_MAX_CONCURRENCY` into `/etc/km/notify.env` only when cap>1 (a cap=1 box has NO such line — verified live). **Poller-only, no DDB attr** (bridge doesn't read it; groups by threadTS unconditionally). **`km validate` WARNS** when cap>1 without both `perSandbox:true` AND `inbound.enabled:true`.
- **Ack-after-completion + visibility heartbeat + idempotency guard.** Message deleted only after the turn finishes. Base queue `VisibilityTimeout` raised **30s → 1800s for NEW queues** (existing 30s queues covered by a 120s `ChangeMessageVisibility` heartbeat ticker, torn down post-turn). Crash-redelivery dup window closed by writing `last_processed_event_ts` to the thread DDB row + a poller guard that skips re-dispatch of `event_ts <= last_processed` (second layer behind the bridge nonce-dedup).
- **/workspace shared-mutation caveat:** parallel turns share `/workspace` — suited for conversational/read-mostly fan-out; concurrent repo-mutating turns are operator responsibility (per-thread worktree isolation deferred). Demo: `profiles/learn.v2.parallel.yaml` (cap=3).
- **Deploy:** bridge (threadTS grouping) → `make build-lambdas` + `km init --slack`. Profile field + poller → `make build-lambdas` + `km init --dry-run=false` (renders the new poller userdata — NOT `--sidecars`); existing sandboxes need `km destroy && km create`. Queue base raise applies to **new** queues only. **No apiVersion bump.** Live synthetic-HMAC E2E proved all six assertions (`.planning/phases/119-*/119-UAT.md`). See `docs/slack-notifications.md` § Phase 119.

**Phase 115 (2026-06-16) — Generic GitHub webhook event→prompt router (complete):**
- The `km-github-bridge` Lambda gains an **autonomous, event-driven ingress path** alongside the human-gated `issue_comment` flow. Any non-`issue_comment` webhook (`repository`/`created`, `push`, `release`, …) is matched against **first-match** rules in a new `github.events:` config block and dispatched to a sandbox with the expanded prompt template as the agent's task. First use case: a new repo created in your org → onboarding/audit prompt.
- **Dormant by default.** Absent `github.events:` in `km-config.yaml` → `KM_GITHUB_EVENTS` unset → byte-identical to Phase 114. Each rule: `on` (event type) + optional `actions:` + `match:` (repo glob, required) + optional `exclude:` globs + `profile:` (cold-create) / `alias:` (warm: FIFO enqueue to a running/stopped box; cold-create when absent) + optional `agent:` + `cooldownSeconds:` storm guard. Template vars: `{{repo}} {{event}} {{action}} {{sender}} {{default_branch}} {{html_url}}`.
- **Flow:** HMAC verify → delivery-GUID dedup (BEFORE match) → `MatchEventRule` first-match → optional per-(event,repo,action) cooldown via nonces table → `ExpandEventTemplate` → `GitHubEnvelope{Kind:<event>, Number:0, Action:<action>, Body:<prompt>}` → warm SQS enqueue (alias set) or cold `PutSandboxCreate` (alias empty). No reaction posted (autonomous events have no originating comment). The poller renders a `[GitHub Event Trigger]` preamble (`<event> / <action>`), NOT the `PR: #0` comment preamble.
- **Manifest:** `km github manifest` emits `default_events` as the **union** of `issue_comment` + every configured `on:` type, and adds `metadata:read` when `repository` is present. **`repository`/`created` is only delivered to a GitHub App installed on the org with "All repositories" access** — a brand-new repo is never in a "selected repositories" set (the OPPOSITE of the `issue_comment` recommendation).
- **Pure bridge-Lambda + config + manifest + poller change.** `lambda-github-bridge/v1.1.0` edited in place (additive `github_events_json` var, no version bump). No SandboxProfile schema change ⇒ no sandbox recreate (cold-created boxes get the new poller free; long-lived alias boxes need `km destroy && km create`).
- **Deploy = `make build-lambdas` + `km init --github`** (or full `km init --dry-run=false`; NOT `--sidecars`, which doesn't update the env block) + `km github manifest` → re-subscribe + re-install the App when adding a new event type.
- Live E2E verified on `slacktest03` (warm/alias path): positive match → warm enqueue → poller → agent; `exclude:` glob drop; caught + fixed a blank-`action` envelope bug (`Action` field was absent from `GitHubEnvelope`). See `docs/github-bridge.md` § Phase 115 and `.planning/phases/115-*/115-UAT.md`.

**Phase 114 (2026-06-15) — Slack bridge auto-resume on @-mention of a stopped/paused sandbox (complete):**
- A Slack @-mention targeting a sandbox whose EC2 instance is stopped/paused now auto-resumes it (resume-or-cold-create), mirroring the GitHub Phase 109 self-heal. **Fixed a latent bug:** the Slack bridge's `FetchByChannel` read the `state` attribute but `km` writes `status` on the `{prefix}-sandboxes` row — so `info.Paused` was always false since Phase 67 (pause-hint dead, blocked resume). Verify DDB attr names against a real row, not the test mock. See [[project_slack_bridge_inbound_e2e_and_status_attr]].

**Phase 113 (2026-06-14) — Sandbox self-awareness: on-box profile + capability/network/privilege self-census in klanker:sandbox (complete):**
- Every EC2 sandbox now writes its rendered profile to **`/opt/km/.km-profile.yaml`** at boot (mode 0644, owned sandbox:sandbox). The file is produced by `yaml.Marshal` of the `userDataParams` struct immediately before `tmpl.Execute` in `generateUserData()` (section 2.10 heredoc with single-quoted `'KM_PROFILE_EOF'` sentinel to prevent shell expansion). Written **VERBATIM — no redaction** (secrets are SSM paths, not values; the sandbox role already grants those reads). The on-box copy is semantically equivalent to `artifacts/{id}/.km-profile.yaml` in S3 but NOT byte-identical (re-serialized YAML; field ordering/comments may differ; also captures post-resolution mutations like `--no-bedrock` not present in the raw S3 bytes).
- **`klanker:sandbox` SKILL.md** rewritten from 106-line "detect env + email tooling" into a 361-line **six-section self-census** (A–F): (A) Identity & agent, (B) capability census (sidecar binaries + systemctl probes for pollers + runtime features from profile), (C) network position (enforcement mode inferred from `km-ebpf-enforcer` unit + iptables DNAT count + exactly two safe-active curls: one allowed host → must connect, one `evil.example.com` → must block), (D) privilege & restrictions (`sudo -n true` cross-checked against `spec.execution.privileged` with WHY explanations so the agent stops fighting locked-down behavior), (E) Slack publish-back readiness (env checks, cross-links `klanker:slack`), (F) posture summary paragraph. The two Section C curls are the **only traffic-generating step** and will appear in `km otel`.
- **Graceful degradation:** on a pre-Phase-113 box (`/opt/km/.km-profile.yaml` absent), every profile-derived check is gated on `PROFILE_AVAILABLE=0` — the skill falls back to env-var census + live probes and never errors.
- **Plugin version bump: `0.4.10`** in both `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` (Phase 111 held 0.4.9). Required for `klanker:sandbox` skill content change to reach clients that cache the old version.
- **Pure `pkg/compiler/userdata.go` + `skills/sandbox/SKILL.md` change.** No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda change, no IAM change.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (create-handler carries the new userdata section 2.10 — NOT `--sidecars`, which does not rebuild the create-handler zip). Plugin version bump in `.claude-plugin/plugin.json` + `marketplace.json`. Existing sandboxes gain the file only on `km destroy && km create`; the census degrades gracefully until then.
- See `klanker:sandbox` skill and `docs/operational-gotchas.md` § Sandbox self-awareness.

**Phase 112 (2026-06-16) — Flip the default Slack render mode to `blocks-rich` (complete):**
- After the Phase 111 soak, `blocks-rich` (Tier-3: native `markdown` + `table` blocks) is now the **default** render mode for sandbox-originated Slack output. Both sandbox-side post sites flipped their fallback in `pkg/compiler/userdata.go`: `_km_stream_drain` (per-turn streaming) and the Slack-inbound poller reply post both go from `--render "${KM_SLACK_RENDER:-blocks}"` → `${KM_SLACK_RENDER:-blocks-rich}`. Default-path goldens (frozen in Phase 111) were updated in lockstep.
- **Fails soft (unchanged):** an overflowing rich render (12K markdown cap / 50-block cap) demotes Tier-3 → Tier-2 (`blocks`) → Tier-1 (`mrkdwn`) automatically.
- **Operator override unchanged:** `KM_SLACK_RENDER=blocks|mrkdwn|plain` in `/etc/km/notify.env` pins a sandbox back to the old default or lower. It is a sandbox-side knob only (NOT compiler-emitted) → does not affect userdata byte-identity.
- **Pure `pkg/compiler/userdata.go` change.** No SandboxProfile schema change, no new TF resource, no DDB column, no bridge Lambda change. The `km-slack` renderer (`pkg/slack`) is untouched — Phase 111 already shipped the Tier-3 code; this only flips which tier is the default.
- **Byte-identity note:** the frozen `userdata_learn_v2_pre92_baseline.golden.sh` baseline was patched **surgically** (render token + comment lines only) — NOT re-captured via `CAPTURE_PRE92_BASELINE=1`, which would have folded the post-baseline SubagentStop script into the frozen baseline that the byte-identity test deliberately strips. The `additional_volume_only` + `h1_byte_identity` goldens (full-output snapshots with sanctioned capture flags) were regenerated normally.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (the create-handler zip renders userdata; NOT `--sidecars`, which does not rebuild that zip). Existing sandboxes keep their baked-in `blocks` default until `km destroy && km create`.
- See `docs/slack-notifications.md` § Phase 112.

**Phase 112.1 (2026-06-24) — Fix: `blocks-rich` silently dropped replies on `invalid_blocks` (empty table cell) (complete):**
- A GFM table with a **blank cell** (the "row-label" leading-column idiom, e.g. `| | Customer | Admin |`) made `km-slack` emit a `rich_text`/`raw_text` cell with an **empty `text` string**. Slack rejects an empty text element with `invalid_blocks`, dropping the entire `chat.postMessage` — the agent's reply was silently lost (reply survived only in `/workspace/.km-agent/runs/*/output.json`). Surfaced once Phase 112 made `blocks-rich` the default. Incident: install `sec`, sandbox `learn-8a08070e`.
- **Two layers:** (1) `pkg/slack/table.go` — `makeBoldCell`/`classifyCell` substitute `" "` for a blank cell (renders visually-empty but schema-valid); covers both the header path AND the ragged-row body-padding path (same empty-text vector — `TestRichTable_RaggedPad` had codified the buggy empty-text padding and was corrected). (2) `cmd/km-slack/main.go` `runWith` — defense in depth: if a block payload is rejected with `invalid_blocks`, re-post **once** without blocks using the already-computed mrkdwn/plain fallback. Builds a **fresh envelope** (new nonce) since the bridge already reserved the first one (`replayed_nonce` guard); single attempt, WARN-logged.
- **Pure `pkg/slack` + `cmd/km-slack` sidecar change.** No Terraform / DDB / SandboxProfile schema change; no bridge Lambda change. Mitigation without deploy: `KM_SLACK_RENDER=mrkdwn` in `/etc/km/notify.env` pins a box off the rich path.
- **Deploy = `make build && km init --sidecars`** (rebuilds + uploads the `km-slack` sidecar). Existing sandboxes pick it up on a sidecar refresh / `km destroy && km create`.

**Phase 111 (2026-06-14) — Rich Slack rendering: markdown + table blocks (opt-in) (complete):**
- New Tier-3 `renderRich` (`pkg/slack/rich.go` + `table.go`) selected by `KM_SLACK_RENDER=blocks-rich`. Default stays `blocks` (Tier-2) — **opt-in**, so default-path golden fixtures stay frozen. Flipping the default is the separate **Phase 112** (gated on this soak).
- Prose → Slack **`markdown` block** (GA Feb 2025): native GFM headings/bold/italic/code/lists + **clickable `[label](url)` anchors** (rendered natively, not `<url|label>`). 12,000-char cumulative cap with chunking; on overflow `ok=false` → Tier-2 `RenderBlocks` → Tier-1 `Mrkdwnify` (existing tiers DEMOTED, not deleted; fail-soft `recover()`). Leading H1 still → `header` block; 🔧 tool lines → `context` block; plain `text` fallback unchanged.
- GFM pipe tables → dedicated **`table` block** (GA Aug 2025): alignment from the delimiter row; >20 cols / >100 rows → existing `fencePipeTables` monospace fallback. **Live-UAT schema fix (`f8271c17`):** header cells are `rich_text` bold wrapped in the MANDATORY `rich_text_section` (a flat element list is rejected with `invalid_blocks`); body cells are all `raw_text` (`raw_number`'s value-field is undocumented and was rejected — deferred; numeric columns still right-align via `column_settings`). See [[project_slack_blockkit_schema_validation]] for the bot-token validation technique.
- `KM_SLACK_AI_FOOTER=true` (default off) appends a "Generated by AI" `context` footer. Both `KM_SLACK_RENDER` and `KM_SLACK_AI_FOOTER` are **sandbox-side env knobs only** (NOT compiler-emitted) → userdata/byte-identity goldens untouched.
- **Pure `pkg/slack` + `cmd/km-slack` sidecar change.** No Terraform / DDB / SandboxProfile schema change; no bridge Lambda change (the bridge forwards the pre-serialized block array verbatim via `json.RawMessage`). Live E2E on `slacktest01` passed: headings, link anchors, tables (bold header + L/C/R alignment), wide-table + 12K-cap fallbacks, code-fence guard, AI footer.
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-slack` sidecar binary; NOT a full apply). Existing sandboxes need `km destroy && km create` (or sidecar refresh) to gain the new renderer.
- See `docs/slack-notifications.md` § Phase 111 and `.planning/research/slack-markdown-block.md`.

**Phase 109 (2026-06-12) — GitHub bridge self-heals orphaned `stopped` alias rows (resume-or-cold-create) (complete):**
- An orphaned `{prefix}-sandboxes` row (`status=stopped` but the EC2 instance is **gone** — terminated out from under km) used to permanently shadow cold-create: the resume branch logged a non-fatal error and enqueued to a per-sandbox FIFO with **no live poller**, so the comment stranded and the bot silently no-op'd on cold start. `ResolveByAliasWithStatus` returned `(id,"stopped",nil)` so `err==nil` skipped cold-create.
- **Fix:** `EC2Resumer.StartSandbox` now wraps an exported sentinel `ErrNoResumableInstance` on the terminal `len(found)==0` path (transient `DescribeInstances`/`StartInstances` errors are **not** wrapped — they keep log-non-fatal + enqueue). `WebhookHandler.Handle` branches the resume failure with `errors.Is`: terminal ⇒ `StatusWriter.DeleteSandboxRow(id)` (clears the stale row so the alias becomes absent — dodges the **ambiguous-alias trap**) then `Publisher.PutSandboxCreate` (cold-create), **no enqueue, no thread upsert**; transient ⇒ unchanged enqueue; success ⇒ unchanged `SetStatusRunning` + enqueue. Genuinely stopped/paused (hibernated) instances still resume exactly as before.
- **Pure bridge-Lambda code + one IAM statement.** `SandboxStatusWriter` extended with `DeleteSandboxRow`; `DynamoSandboxStatusWriter.DeleteSandboxRow` is a single `DeleteItem` keyed by `sandbox_id` (`DynamoUpdateItemClient` widened with `DeleteItem`); `infra/modules/lambda-github-bridge/v1.1.0` adds `dynamodb:DeleteItem` to the existing `DDBSandboxesUpdateItem` statement (still no `PutItem`). No SandboxProfile schema change, no new TF resource, no new DDB column.
- **Ported to the H1 bridge in lockstep (parity):** identical fix in `pkg/h1/bridge/{aws_adapters,interfaces,webhook_handler}.go` (resume branch is `dispatchTarget`); `infra/modules/lambda-h1-bridge/v1.0.0` gets the same `dynamodb:DeleteItem` grant. Slack bridge has no resume-or-cold-create path (no change).
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — the IAM/env block updates only on a full terragrunt apply; `km init --github` / `--h1` also cover the env+IAM change for the respective bridge). **No sandbox recreate.**
- Out of scope (note): the orphaned FIFO queue + per-sandbox Lambdas are not GC'd (only the DDB row); they're harmless garbage flagged by `km doctor`, cleaned by `km destroy <id>`.
- See `docs/github-bridge.md` § Phase 109 and `docs/h1-bridge.md` § Phase 109.

**Phase 108 (2026-06-12) — GitHub bridge per-turn idempotency guard: no more duplicate PR comments (complete):**
- A single @-mention could make the sandbox agent post **two byte-identical** PR comments/reviews seconds apart — the agent invoked `km-github comment`/`review` twice in one turn (the poller's hard "post your reply (REQUIRED)" mandate colliding with an invoked skill's own post instruction, or a self-retry of a call that secretly succeeded). GitHub issue comments/reviews are NOT idempotent.
- **Fix at the helper chokepoint** (`cmd/km-github/`): every post now embeds an invisible per-turn marker `<!-- km-turn:$KM_GITHUB_TURN_ID -->` and, before posting, scans the issue's existing comments / the PR's existing reviews for that same marker — if present, it no-ops (exit 0, logs `duplicate suppressed`). The marker is keyed on the poller's `RUN_ID`, so two *separate* legitimate mentions on the same PR each still post.
- **Fail-open:** if the pre-post duplicate-check GET errors, the helper posts anyway (never strand a legitimate reply because a read failed) — but logs it. The check paginates (`per_page=100`, follows `Link: rel="next"`) so a just-posted comment that sorts last is still found.
- **`KM_GITHUB_TURN_ID`** is exported inline into all four agent-dispatch `sudo` blocks of the github inbound poller (codex resume/first, claude main/retry), mirroring `KM_GITHUB_REPLY_AGENT`. Empty for manual `km-github` invocations ⇒ marker + duplicate-check disabled ⇒ byte-identical to pre-Phase-108.
- **Pure helper + `pkg/compiler/userdata.go` change.** New `pkg/github/marker.go` (`TurnMarker` / `CommentMarkerExists` / `ReviewMarkerExists`). No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda or IAM change. Slack/H1 pollers untouched.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars`): the create-handler zip carries the new userdata, AND the full `km init` rebuilds+uploads the `km-github` sidecar binary via `buildAndUploadSidecars` (the helper is delivered to the box from `s3://.../sidecars/km-github`, NOT by `make sidecars`). Existing sandboxes need `km destroy && km create` to gain the new userdata env-var export + the new sidecar.
- See `docs/github-bridge.md` § Phase 108.

**Phase 106 (2026-06-11) — Session-resume hint on GitHub + HackerOne bridge replies (post-on-mint) (complete):**
- After a bridge agent turn, the sandbox-side poller posts ONE collapsed `<details>` resume-hint comment carrying the sandbox id, run-from `/workspace`, and the agent-correct resume command: `claude --resume <id>` (Claude) or `codex exec resume <id>` (Codex), branched on `EFFECTIVE_AGENT`, plus the freshly minted session id.
- **Post-on-mint:** fires only when the session id is new or changed (first turn or Gap-E cross-box re-mint) ⇒ exactly once per stable thread. Best-effort `|| true` — never blocks the SQS ack.
- **H1 hint is INTERNAL by default** (`km-h1 comment`, no `--reply-to-researcher`) — it lands on the internal/team comment track, NEVER visible to the external HackerOne researcher.
- **GitHub + HackerOne pollers only.** Slack poller deliberately EXCLUDED — byte-identical to pre-Phase-106.
- **Pure `pkg/compiler/userdata.go` change.** No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda or IAM change.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — `--sidecars` only re-uploads sidecar binaries, not the create-handler zip; NOT `km init --github`/`--h1` — those refresh bridge env+IAM only). Existing sandboxes need `km destroy && km create`.
- See `docs/github-bridge.md` § Phase 106 and `docs/h1-bridge.md` § Phase 106.

**Phase 105 (2026-06-11) — Scoped `km init` for bridge config (complete):**
- `km init --only <module>` applies a **single** terragrunt module instead of the full ~27-module fleet. Refreshes the Lambda env block + IAM for that module only. Does NOT rebuild a stale code zip (still `make build-lambdas` + `km init --lambdas`) and does NOT provision new resources/tables/queues (still full `km init --dry-run=false`).
- **Sugar aliases (tier-1, no confirmation):** `--github` → `lambda-github-bridge`; `--slack` → `lambda-slack-bridge`; `--h1` → `lambda-h1-bridge`; `--email` → `email-handler`. All four are pure env+IAM targets — fast, no destroy-class risk.
- **Tier-2 gated (no cheap alias):** `--only ses` — routes through the destroy-class safety gate (same curated trip-block as `km init --plan`) as a pre-apply gate; refuses a protected destroy/replace without `--i-accept-destroys`. The `ses` module owns SES domain identities, Route53 records, and the consolidated S3 bucket policy.
- **SSM side-effect:** scoped `--github`/`--h1`/--slack` also republishes SSM command maps / bot-user-id (same as a full apply; no separate step required).
- **Mutual exclusion:** `--only`/`--github`/`--slack`/`--h1`/`--email` are mutually exclusive with `--sidecars`, `--lambdas`, and `--plan`.
- **`--dry-run` honored:** defaults true (shows what would apply); `--dry-run=false` applies.
- **No-drift invariant:** scoped apply derives from the identical `km-config.yaml → KM_* → terragrunt` pipeline as a full apply; a subsequent `km init --plan` is a no-op.
- **Deploy = `make build`** (operator-side binary only; no Lambda zip rebuild, no new TF resource).
- See `klanker:init` skill § Fast-path variants and `OPERATOR-GUIDE.md` § Scoped init.

**Phase 104 (2026-06-10) — O(1) Slack channel resolution on alias reuse (complete):**
- `km create` on a reused `--alias` (profile has `notification.slack.perSandbox: true` + `archiveOnDestroy: false`) now resolves the existing channel in **bounded, O(1)** time — never the unbounded `conversations.list` workspace scan that previously wedged the create-handler Lambda for the full 900 s.
- **Root cause fixed:** any transient `conversations.info` error (ratelimited / 5xx / context blip) previously fell through to an unbounded paginated scan. Phase 104 classifies the error: only a definitive `channel_not_found` invalidates the stored mapping; every other error triggers a bounded retry (2× / 500 ms) then optimistic use of the stored ID — never a scan.
- **Three layers:** P0 wall-clock sub-context bounded by `KM_SLACK_RESOLVE_BUDGET` (default 45 s); P1 lookup-first with transient-error classification; P2 durable `km-slack-channels` DDB table (hash key `alias`, no TTL) as the authoritative store.
- **New `km-slack-channels` table** (`{prefix}-slack-channels`, PAY_PER_REQUEST, SSE on). Existing SSM by-name cache kept as back-compat read/write fallback; both stores written through on every successful resolution. `km doctor` checks table existence only — NOT orphan-row scan (alias rows are not per-sandbox and must never be auto-deleted).
- **`km slack adopt <alias> <channelID>`** — seeds the DDB store for channels created outside km or before Phase 104. Validates `^C[A-Z0-9]+$` format + bot membership; writes through to DDB + SSM. Find the channel ID in Slack → channel name (top bar) → About → Channel ID.
- **Observability:** `slack_resolve path=cache_hit|cache_optimistic|created|scan_capped|failfast ms=… id=…` — one INFO log per Mode-2 resolution at create time.
- **No SandboxProfile schema change ⇒ no `--sidecars`, existing sandboxes unaffected** (create-time fix; running sandboxes do not need to be recreated).
- **No `lambda-slack-bridge` change.** Bridge is NOT a consumer of the `km-slack-channels` table.
- **Deploy = `make build` (BEFORE `km init` — binary carries the new `dynamodb-slack-channels` regionalModules entry; a stale binary silently skips it → `AccessDenied` at runtime) + `make build-lambdas` + `km init --dry-run=false`** (new table + create-handler IAM require a full terragrunt apply, NOT `--sidecars`).
- See `docs/slack-notifications.md` § Phase 104 for incident root-cause analysis, env knobs, adopt runbook, and troubleshooting.

**Phase 102 (2026-06-08) — GitHub bridge agent verbs: /claude and /codex select the per-thread agent in a PR comment (complete):**
- An @-mention in a PR/issue comment may include `/claude` or `/codex` anywhere in the body (code-stripped, ≤1 agent verb per comment; two distinct verbs → error reply, no dispatch). The verb selects the agent for the turn and is persisted as `agent_type` in `km-github-threads`; subsequent turns in the same thread inherit it. GitHub analog of the Slack Phase 70 per-thread agent-verb (`/claude:` / `codex:`).
- **Precedence:** explicit verb > thread `agent_type` row > profile `spec.agent.default` (default: `claude`).
- **Codex precondition:** `/codex` routes to the sandbox; if the sandbox profile has no Codex installed, the poller posts a helpful error comment ("This sandbox's profile has no Codex; /codex is unavailable here.") and acks the queue message without dispatching.
- **Reserved tokens + km doctor:** `help`, `claude`, `codex` are reserved in `github.commands`. Defining a command entry with one of these names → `km doctor WARN`. Extended from "help-only" in Phase 99 to include `claude` and `codex` in Phase 102.
- **`/help` extension:** the built-in `/help` reply now prepends an "Available agents" block listing `/claude` and `/codex`, and appends "Current thread agent: `<type>`" when the thread has a stored `agent_type`.
- **Back-compat:** a comment with no agent verb is byte-identical to Phase 101 behavior. No new Terraform resources. No SandboxProfile schema change. `agent_type` is schema-on-write (DDB column added in Phase 102 Plan 02; no TF migration).
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** — bridge + create-handler Lambdas updated together. Existing sandboxes need `km destroy && km create` to gain the new Phase 102 poller (D6 guard + `THREAD_AGENT_TYPE` env var). Bridge agent-verb parsing fires on the next webhook delivery without sandbox recreate.
- See `docs/github-bridge.md` § Phase 102 for the full operator runbook, Codex precondition, reserved tokens, and two-install/one-App UAT.

**Phase 101 (2026-06-08) — GitHub bridge orphan-repo helpful reply (complete):**
- When the shared bot is @-mentioned in a PR or issue comment on a repo **no install owns**, the front-door install posts ONE guidance comment naming `github.repos:` wiring and `km init`. Previously (Phase 100) the event was silently dropped (`github_relay_no_owner`); Phase 101 closes that gap with claim-aware scatter-gather — each relayed peer returns `{"claimed": bool}`; zero claims ⇒ true orphan ⇒ one comment. GitHub analog of the Slack Phase 96 default router (`docs/slack-notifications.md` § Phase 96).
- **Dormant by default.** Set `github.default_router: true` in `km-config.yaml` on the **front-door install only**. Absent or false → byte-identical to Phase 100 (no comment, no claim-gather overhead). `github.default_router` → `KM_GITHUB_DEFAULT_ROUTER`; dormancy = `"false"`.
- **Rollout-safe mixed fleet.** A peer still on Phase-100 code returns plain 200 (no body) — tallied as `claimed:true`. **No false "nobody owns this"** until peers upgrade. Upgrade peers in any order.
- **Per-(repo, number) cooldown** (3600s) via the nonces table (key `gh-router-cooldown:{owner}/{repo}#{number}`) — no new infrastructure.
- **No SandboxProfile schema change ⇒ no `km init --sidecars`, no sandbox recreate.**
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** on the **front-door install**. `KM_GITHUB_DEFAULT_ROUTER` is an env-block change; `--sidecars` does not update the env block.
- See `docs/github-bridge.md` § Phase 101 for the full operator runbook + the two-install/one-App/unowned-repo UAT.

**Phase 100 (2026-06-08) — GitHub bridge federated relay: one GitHub App serving many installs (complete):**
- A single GitHub App can serve multiple `resource_prefix` installs. GitHub delivers every `issue_comment` webhook to one **front-door** install; its bridge runs `Resolve(owner/repo)` against its own `github.repos:` and on a **miss** relays the raw webhook verbatim (body + `X-Hub-Signature-256` + `X-GitHub-Event` + `X-GitHub-Delivery`, adding `X-KM-Relayed: 1`) to every peer in `github.peer_bridges`. The install whose `github.repos:` owns the repo processes it and posts the **single** 👀 (the front door reacts none). GitHub analog of the Slack Phase 95 relay, simplified to fire-and-forget (orphan-repo reply deferred to Phase 101).
- **Dormant by default.** Add `github.peer_bridges:` (list of *other* installs' GitHub bridge Function URLs, `km github status` → `bridge-url`) to `km-config.yaml`. Absent/empty → `KM_GITHUB_PEER_BRIDGES` empty, relayer nil → **byte-identical to Phase 97/98**. `km doctor` SKIPs the peer check silently.
- **`X-KM-Relayed: 1` is the entire single-hop loop guard.** A relayed request is terminal: process if owned, else drop (`github_relay_no_owner` log) — never re-relayed. Each install dedupes the forwarded `X-GitHub-Delivery` in its **own** nonces store; each peer re-verifies HMAC with its own copy of the same App webhook secret (GitHub sigs are timestamp-free → no skew window).
- **`Resolve()` reorder** (moved ahead of the thread-lookup + @-mention filter) is an unconditional scale fix — byte-identical dispatch, and it skips a wasted `LookupSandbox` DDB read per PR comment on unowned repos (the 700-repo fix). Federation off or on, dispatch outcomes are identical.
- **`km doctor` adds:** `GitHub peer bridges` (malformed URL / self-loop → WARN; empty → SKIP), mirroring `checkSlackPeerBridges`; own bridge-url from SSM `{prefix}config/github/bridge-url`.
- **Correctness invariant (documented, not enforced):** each repo owned by exactly one install across the fleet; two owners ⇒ double-processing.
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** on each affected install: `KM_GITHUB_PEER_BRIDGES` is an env-block change that needs a full terragrunt apply (`--sidecars` rebuilds the zip + cold-starts but does NOT update the env block). The `lambda-github-bridge` module is edited **in place at `v1.1.0`** (additive `default=""` var, no version bump). **No SandboxProfile schema change ⇒ no sandbox recreate.**
- See `docs/github-bridge.md` § Phase 100 for the full operator runbook + the two-install/one-App E2E UAT.

**Phase 97 (2026-06-06) — GitHub comment-trigger bridge: km-github-bridge Lambda (complete):**
- When an allowlisted GitHub login @-mentions the bot in a PR comment, the km-github-bridge Lambda HMAC-verifies the webhook, dedupes by `X-GitHub-Delivery` GUID, resolves `owner/repo` → `{alias, profile, allow}` from `km-config.yaml github.repos:`, emits 👀 ACK, and dispatches to a per-repo sandbox (warm: FIFO enqueue; cold: EventBridge SandboxCreate).
- **Dormant by default.** Add `github.repos:` to `km-config.yaml` to activate. Absent → byte-identical to pre-Phase-97 behavior. `km doctor` skips the GitHub group silently when unconfigured.
- New CLI: `km github init` (cache bot-login from GitHub App), `km github manifest` (generate App JSON), `km github status` (print SSM-backed config). New sandbox-side helper `km-github comment|review|pr-files`.
- New profile: `profiles/github-review.yaml` — lean 2h/20m-idle spot t3.medium with `notification.github.inbound.enabled: true` (provisions the per-sandbox github-inbound FIFO queue).
- Source-aware userdata poller drains github-inbound envelopes and dispatches Claude turns; agent posts PR reviews via `km-github review`.
- **Deploy:** `make build-lambdas` (clean) + `km init --dry-run=false` (new Lambda + EventBridge + env block, NOT `--sidecars`) + `km init --sidecars` (schema field) + `km github manifest` to update App scopes + `km github init` to cache bot-login. Existing sandboxes need `km destroy && km create` to gain the queue + poller.
- See `docs/github-bridge.md` for the full operator runbook, deploy sequence, and troubleshooting.

**Phase 96 (2026-06-05) — Slack default router: orphan-channel @-mention reply (complete):**
- When the shared bot is @-mentioned in a channel no install owns, the front-door install posts ONE threaded reply naming the `#sb-{alias}-{profile}` convention and listing running sandbox channels across all installs as `<#CID>` Slack mentions. Empty aggregate list → guidance-only variant.
- **Dormant by default.** Set `slack.default_router: true` in `km-config.yaml` on the **front-door install only**. Absent or false → byte-identical to Phase 95 (no reply, no extra calls).
- Mechanism: claim-aware scatter-gather (Phase 95 relay upgraded); zero claims = true orphan; any `claimed:true` from any peer → owner handled it, no reply. Legacy Phase-95 peer responses treated as `claimed:true` (rollout-safe mixed fleet).
- Cooldown: 3600 s per channel via the nonces table (`router-cooldown:{channel}` TTL key) — no new infrastructure.
- No new Slack OAuth scopes. No SandboxProfile schema change. No sandbox recreate needed.
- **Deploy:** `make build-lambdas` (clean) + `km init --dry-run=false` on **ALL installs** (NOT `--sidecars` — env-block + IAM require a full terragrunt apply). See `docs/slack-notifications.md` § Phase 96.
- Deferred: agentic self-serve create, non-member channels (`app_mention` + `chat:write.public`), DM fallback (`im:write`).
- See `docs/slack-notifications.md` § Phase 96 for the full operator guide, deploy instructions, and troubleshooting.

**Phase 93 (2026-06-02) — `km desktop` (KasmVNC remote browser/XFCE) + Ubuntu OS-aware bootstrap (complete):**
- `km desktop start|status <id>` — KasmVNC graphical session in the operator's local browser over an SSM port-forward (loopback `127.0.0.1:8444`, SSM-only, no public/SG change). Mirrors `km vscode`. New `spec.runtime.desktop` block (`enabled` default **false**, `mode: kiosk|full`, `browsers ⊆ {firefox,chromium,chrome,brave}`, `geometry`). Engine: KasmVNC. **Ubuntu 24.04/22.04 only** (`km validate` errors on desktop+non-Ubuntu AMI). See `docs/desktop.md` / `klanker:desktop` skill.
- **The EC2 userdata bootstrap is now OS-aware** (`pkg/compiler/userdata.go` + the >12KB stub in `compiler.go`): was Amazon-Linux-only; Ubuntu needs apt-over-HTTPS (SG allows only 443, not port 80), `ForceIPv4`, AWS-CLI install via python3 (no `unzip`), `ssh.service`, and `systemd-resolved` stopped for the eBPF resolver on `:53`. AL2023 path unchanged. Both `proxy` and `ebpf`/`both` enforcement verified on Ubuntu. Desktop install runs BEFORE network enforcement, so the `spec.network` allowlist does not gate it. Details: `.planning/phases/93-*/93-UAT.md` and [[project_ubuntu_userdata_constraints]].
- `km desktop start`/`km vscode start` now **auto-reconnect** the SSM port-forward on drop (Ctrl-C to quit), with a liveness probe that recycles a silently-hung plugin — KasmVNC/sshd survive server-side. `runReconnectingPortForward` in `internal/app/cmd/shell.go`.
- The unused `cmd/configui` web dashboard was **removed**.
- **Deploy:** desktop schema + OS-aware userdata are compiled by the create-handler Lambda, so for remote `km create` redeploy with `make build-lambdas` (clean) + `km init --dry-run=false`. The reconnect wrapper is operator-side (local binary only). Existing sandboxes need `km destroy && km create`.

## Where to look

| You want to… | Look at |
|---|---|
| Push webhook ingress — `webhooks:` block, Wiz Automation Rule setup, canonical km payload template, storm control (replay/cooldown/group_by/rate ceiling), deploy surface | `docs/webhook-ingress.md` |
| Why an interactively-used sandbox got reaped as idle — the utmp/PTY root cause, the eight `km-presence` signals, why VNC/SSH are matched by socket not process, and the fail-idle rule | `docs/desktop.md` § Idle timeout + `docs/vscode.md` § Idle timeout |
| Egress deny lists — `spec.network.egress.deniedDNSSuffixes` / `deniedHosts`, deny-beats-allow (incl. `*` and the GitHub/OpenAI/MITM carve-outs), the deliberately-broader deny matching, the `*`-allowlist-under-eBPF limitation, deploy surface | `docs/egress-deny-lists.md` |
| Live egress census, allowlist pinning, on-demand packet capture — `km-netpolicy observed/flows/profile/pin/capture`, the deny-vs-pin union/intersection relationship, the `--exact` tradeoff, the two sharp edges, capture bounds, deploy surface | `docs/egress-census.md` (Phase 131) |
| What a sandbox ran and who reached a host — `km-netpolicy execs/who/execs save`, why `who`'s pid attribution comes from the HTTP proxy (not the eBPF ring buffer) and needs `enforcement: ebpf`/`both`, the root-only unredacted store, argv bounds, rotation, `ExecStopPost` vs. `ExecStop`, deploy surface | `docs/exec-capture.md` (Phase 132) |
| Declarative MITM intercepts — `spec.network.mitm.intercepts`, `redirect`/`respond` actions, precedence (deny → metering/GitHub → intercepts → allowlist), by-name last-wins override, the `profiles/base/mitm-rickroll.yaml` migration fragment, deploy surface | `docs/mitm-intercepts.md` (Phase 129) |
| kubectl in a sandbox against a cluster only your laptop can reach — `km tunnel` operator runbook: prerequisites, flags, troubleshooting, the `socks` mode, deploy surface | `docs/k8s-reverse-tunnel.md` (Phase 130) |
| **How the k8s tunnel actually works** — the three nested tunnels and why SSM forced SSH-inside-SSM, the ExecCredential proxy and why the broker is deliberately dumb, the `tls-server-name`-vs-CA split, the exec apiVersion exact-match trap, the precise trust boundary, and why the deploy surface is `make build` alone | `docs/k8s-reverse-tunnel-internals.md` (Phase 130) |
| Persistent agent panes that survive detach — `km herdr start`, the base/tools/herdr fragment, km-presence signal 8, the pause-vs-stop lifecycle trap, the ssh-config conflict, deploy surface | `docs/herdr-remote-attach.md` |
| Why a detached herdr session did or did not keep a sandbox awake — signal 8's busy-pane rule, why it detects work rather than the server, and why a quiet session is still reaped | `docs/herdr-remote-attach.md` § Signal 8 |
| Cross-account capacity borrowing — `km account add/register/list/rm`, `spec.runtime.launchAccount`, the two-credential enrollment sequence, the launcher-role security model, capacity/teardown/doctor cross-account wiring, deploy surface | `docs/cross-account-capacity-borrowing.md` (Phase 126) |
| Private-subnet sandboxes + per-AZ NAT gateways — `network.nat_gateway` / `spec.network.privateSubnet` toggles, cost, the one-time route-table split, reversal, guards, deploy surface | `docs/private-subnet-nat.md` (Phase 125) |
| Sizing the private topology to cut the NAT bill — `network.private_subnet_count` (1–4, absent = all 4), the AZ-rotation tradeoff it buys, and the subnet destroys on lowering it | `docs/private-subnet-nat.md` § Paying for fewer AZs |
| AZ failover + capacity feasibility — `km create` classify-and-retry sweep, GPU quota wall (`L-DB2E81BA`), `km capacity` verdicts, `--wait-for-capacity`, `{prefix}-capacity` DDB table, deploy-surface order | `docs/operational-gotchas.md` § AZ failover + capacity feasibility (Phase 124) |
| Action quotas + freeze quarantine — `spec.limits`/install `limits:`, windows (lifetime/perHour/perDay), `onBreach` warn/block/freeze, zero=hard-deny, `km freeze`/`km unlock`, `km status` Quotas section, the `km-quota-alerter`, deploy surface | `docs/action-quotas.md` (Phase 121) |
| Operator CLI tour | `klanker:user` skill |
| One-time platform setup, `km init`, multi-instance, Slack bootstrap | `klanker:init` skill |
| Send / receive email from inside a sandbox | `klanker:email` skill |
| Inject SOPS-encrypted secrets into a sandbox | `docs/sandbox-secrets.md` (Phase 89) |
| Brokered secret unsealing — `grants`, `km-env`, the shims and the nvm PATH race, the boot check, reading `secret_unseal`, the security posture verbatim | `docs/brokered-secrets.md` (Phase 133 Wave 1) |
| The IMDS fence — `spec.secrets.fenceIMDS`, `km-creds` and `credential_process`, the two Denies verbatim, why self-assume needs an `aws:PrincipalArn` condition rather than its own ARN, assertion 6's negative control, what the fence is NOT, deploy surface | `docs/brokered-secrets.md` § The IMDS fence (Phase 133 Wave 2) |
| Serverless `km check` runner (deploy/run/ls/sync/rm, KM_CHECK_TRIGGER, CheckDispatch) | `docs/check-runner.md` (Phase 116) |
| Inject secrets into a `km check` Lambda — `--secret <ssm-path>` + `--sops <file>` (deploy-time unpack to per-check SSM SecureString params; no Lambda KMS) | `docs/check-runner.md` § Secrets |
| Post to Slack from inside a sandbox (incl. transcript streaming, inbound, attachments) | `klanker:slack` skill |
| Polite-bot mode, `KM_SLACK_MENTION_ONLY`, per-channel @-mention-only inbound | `docs/slack-notifications.md` § Phase 91 |
| Federated bridge relay — one Slack App across multiple km installs | `docs/slack-notifications.md` § Phase 95 |
| Default router: orphan-channel @-mention reply, `slack.default_router`, cooldown | `docs/slack-notifications.md` § Phase 96 |
| Private per-sandbox Slack channel (`notification.slack.private`, `is_private:true`) | `docs/slack-notifications.md` § Phase 118 |
| Slack trigger allowlist (`slack.allow` / `notification.slack.inbound.allow`, `KM_SLACK_ALLOW`, `slack_allow` attr, resolution order, silent-drop) | `docs/slack-notifications.md` § Phase 118 |
| Slack inbound per-thread parallelism (`notification.slack.inbound.maxConcurrentThreads`, `KM_SLACK_MAX_CONCURRENCY`, threadTS FIFO grouping, bounded-concurrent poller, ack-after + 1800s visibility heartbeat, `last_processed_event_ts` dedup, /workspace caveat) | `docs/slack-notifications.md` § Phase 119 |
| Alias reuse vs archived channels — unarchive-on-reuse (`path=cache_unarchived`), why a renamed `channelName` never takes effect on a mapped alias, `km slack forget-channel` / `km slack adopt` as the escape hatches | `docs/slack-notifications.md` § Alias reuse and archived channels |
| Verified, `klanker-maker[bot]`-attributed **signed** commits from a sandbox — `km-github commit` (GraphQL `createCommitOnBranch`), `contents:write`/`push` precondition, `--parent` PR-auto-close gotcha | CLAUDE.md § `km-github commit` + `docs/github-app-permissions.md` |
| Sandbox-side helper binaries convention — everything a klanker does for GitHub/Slack/Email/HackerOne starts at `/opt/km/bin` | CLAUDE.md § Sandbox-side helper binaries |
| GitHub comment-trigger bridge — `@km-bot review this PR` → sandbox agent → PR review | `docs/github-bridge.md` (Phase 97) |
| GitHub bridge federated relay — one GitHub App across multiple km installs (`github.peer_bridges`) | `docs/github-bridge.md` § Phase 100 |
| GitHub App permissions reference — every scope/event + why, `contents:write` push opt-in | `docs/github-app-permissions.md` |
| Slack App permissions reference — every bot scope/event + why | `docs/slack-app-permissions.md` |
| GitHub bridge agent verbs — `/claude` / `/codex` per-thread agent select in PR comments | `docs/github-bridge.md` § Phase 102 |
| GitHub generic event→prompt router — `github.events:` rules fire a sandbox agent on `repository`/`push`/`release` webhooks (warm `alias:` or cold-create); "All repositories" install gotcha | `docs/github-bridge.md` § Phase 115 |
| HackerOne comment-trigger bridge — program webhook → sandbox agent → report comment (auto-triage + `@`-handle, multi-target fanout, internal-by-default replies) | `docs/h1-bridge.md` (Phase 103) |
| O(1) Slack channel resolution on alias reuse, `km-slack-channels` table, `km slack adopt`, `KM_SLACK_RESOLVE_BUDGET` | `docs/slack-notifications.md` § Phase 104 |
| Scoped `km init` — `--only <module>` / `--github` / `--slack` / `--h1` / `--email` (tier-1 env+IAM fast-path) + `--only ses` (tier-2 destroy-class gated) | `klanker:init` skill § Fast-path variants |
| Ask the operator to do something via email | `klanker:operator` skill |
| Detect sandbox environment + verify tooling | `klanker:sandbox` skill |
| Sandbox self-census — identity, capabilities, network boundary, privilege/restrictions, Slack readiness, posture summary | `klanker:sandbox` skill (Phase 113) |
| On-box profile file (`/opt/km/.km-profile.yaml`), deploy surface, graceful degradation on pre-Phase-113 boxes | `docs/operational-gotchas.md` § Sandbox self-awareness |
| VS Code Remote-SSH operator workflow | `klanker:vscode` skill |
| Run a remote browser/desktop in a sandbox via km desktop | `klanker:desktop` skill |
| Serve a local 70B model on a GPU sandbox (vLLM + Bifrost multi-provider router), reach it via VS Code / Slack codex / `km model start [--anthropic]`; G-quota + DLAMI prereqs, secret/route wiring, deploy surface | `docs/gpu-model-serving.md` (Phase 122) |
| Cross-account k8s (IRSA) cluster onboarding | `klanker:cluster` skill |
| Non-obvious operational footguns (deploy surface, terragrunt, teardown, Ubuntu userdata) | `docs/operational-gotchas.md` |
| `km shell` → "km-session-entry: not found" on Ubuntu; apt mirror host rewrite (`ec2.archive.ubuntu.com` → `archive.ubuntu.com`), diagnosing a still-running cloud-init without a shell | `docs/operational-gotchas.md` § km shell → "km-session-entry: not found" |
| Full operator runbook | `OPERATOR-GUIDE.md` |
| Email protocol deep-dive (SES, IAM, signing) | `docs/multi-agent-email.md` |
| Slack runbook (full setup, troubleshooting) | `docs/slack-notifications.md` |
| VS Code runbook | `docs/vscode.md` |
| Remote browser/desktop runbook (KasmVNC, kiosk, full XFCE, AMI-bake) | `docs/desktop.md` (Phase 93) |
| Snapshot-backed EBS volumes in profiles | `OPERATOR-GUIDE.md` § additionalSnapshots |
| Codex parity, `spec.agent.default`, Slack prefix routing & agent switching | `docs/codex-parity.md` (Phase 70) |
| Structured Claude/Codex tool gating via `spec.agent:`, synthesizers, asymmetry note | `docs/agent-tool-gating.md` (Phase 92) |
| Cut a release (goreleaser + GH Actions, tag-driven) | `docs/release.md` |
| Run km from a container — the operator image's mounts, the profile overlay (`KM_PROFILE_SEARCH_PATHS`, whitespace-separated), what makes `km init --plan` work in-container, and why applying does not | `containers/operator/README.md` |
| SOPS / SSM allowlist via `iam.allowedSecretPaths` | `docs/sandbox-secrets.md` (Phase 89, renamed `identity:`→`iam:` in Phase 92) |
| Composable multi-parent profile inheritance — `extends:` list, deep-merge, `profiles/base/` fragments, `initCommandsAppend`, v1 narrowing limitation | `OPERATOR-GUIDE.md` § Composable inheritance |
| Wiz Runtime Sensor on a sandbox — opt-in `base/security/wiz` fragment, the prefix-relative SSM-path rule, why it is not tamper-proof, deploy surface | `docs/wiz-sensor.md` |
| Which Wiz integration is which — km **pulls** from Wiz (`checks.triggers`, Phase 116), Wiz **pushes** to km (`webhooks:`, Phase 127), and a sandbox **reports to** Wiz (the runtime sensor). Three directions, independent, composable | `docs/wiz-sensor.md` § Relationship to the other Wiz integrations |

## Sandbox-side helper binaries (`/opt/km/bin`)

**Everything a sandbox agent ("klanker") does to act on the outside world starts at `/opt/km/bin`.** These are the `km-*` helper binaries km bakes onto every EC2 sandbox — not the operator `km` CLI (that runs on the operator's workstation). They are cross-compiled `linux/amd64` sidecars uploaded to `s3://{artifacts}/sidecars/km-*` by `km init --sidecars` (`buildAndUploadSidecars` → `sidecarBuilds()` in `internal/app/cmd/init.go`), then fetched at boot by userdata (`aws s3 cp …/sidecars/… /opt/km/bin/…`) and symlinked into `/usr/local/bin`, so a bare `km-slack`/`km-github`/`km-send` resolves on `PATH`. Each reads its per-sandbox GitHub/Slack/SES/HackerOne credentials from SSM itself (`/{prefix}/sandbox/{id}/…`) — the agent never handles a raw token.

**When a klanker needs to act, reach for the matching helper before anything hand-rolled:**

| Channel | Helper(s) in `/opt/km/bin` | Verbs / use |
|---|---|---|
| **GitHub** | `km-github` | `comment` / `review` / `check` / `pr create` / **`commit`** (verified signed commit via GraphQL `createCommitOnBranch`) — all keyless (token from SSM). Prefer this over raw `gh`/`git push`. |
| **Slack** | `km-slack` | post status/progress/threaded replies + Block-Kit rich rendering (see `klanker:slack` skill). |
| **Email** | `km-send`, `km-recv` (+ `km-mail-poller`) | signed inter-sandbox / operator email (see `klanker:email` skill). |
| **HackerOne** | `km-h1` | comment on reports (internal-by-default). |
| **Git auth** | `km-git-askpass`, `km-git-credential-helper` | inject the installation token into plain `git` operations (baked-in sandbox id, works in any subprocess). |
| **Egress / network** | `km-netpolicy` | `deny <pattern>...` / `list` — runtime narrowing (see `docs/egress-deny-lists.md`). `observed` / `flows [--since 10m] [--denied] [--json]` / `profile` / `pin [--dry-run] [--exact] [--yes] [--allow-empty] [--accept-truncated]` / `capture start\|stop\|status\|list` — live egress census, allowlist pinning (irreversible, no un-pin verb), on-demand bounded packet capture (see `docs/egress-census.md`, Phase 131). `execs [--since 10m] [--uid N] [--failed] [--json]` / `who <host>` / `execs save` — what the sandbox ran, joined against a flow to answer which process reached it (see `docs/exec-capture.md`, Phase 132). |
| **Terminal multiplexer** | `herdr` (in `/usr/local/bin`, not `/opt/km/bin`) | Third-party; mirrored from S3 by `km init --sidecars`, installed by `base/tools/herdr` or `km herdr start`. Attach from the operator's laptop with `herdr --remote km-<id>`. |

Infra sidecars also live here (`km-http-proxy`, `km-dns-proxy`, `km-audit-log`, `km-presence`, `km-queue-runner`, `km-notify-hook`, `km-upload-artifacts`, `otelcol-contrib`, `sops`) plus the source-aware inbound pollers (`km-{github,slack,h1}-inbound-poller`, `km-mail-poller`) — an agent rarely calls these directly, but their presence is how the box self-censuses its capabilities (see the `klanker:sandbox` skill + `/opt/km/.km-profile.yaml`, Phase 113). New sandbox-side capability ⇒ new `km-*` binary in this dir + a `sidecarBuilds()` entry + a userdata `s3 cp`, delivered by `km init --sidecars`.

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml> [alias]` — provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`, `--prompt <text-or-@file>` repeatable, `--wait`). The alias may be a second positional instead of `--alias`; whichever argument ends in `.yaml`/`.yml` is the profile, so order does not matter.
- `km clone <source> [new-alias]` — duplicate one or more running sandboxes from the source's stored profile (`--count`, `--alias`, `--no-copy` to skip the `$HOME` copy, `--ai`/`--compute`/`--ttl`/`--idle`/`--on-demand`/`--no-bedrock` overrides)
- `km destroy <sandbox-id>` — teardown a sandbox (`--remote` by default, `--yes` to skip confirmation; `km kill` is an alias)
- `km pause <sandbox-id>` — hibernate/pause an EC2 or Docker instance (preserves infra)
- `km resume <sandbox-id>` — resume a paused or stopped sandbox
- `km lock <sandbox-id>` — safety lock preventing destroy/stop/pause (atomic DynamoDB)
- `km unlock <sandbox-id>` — remove safety lock (requires confirmation or `--yes`)
- `km stop <sandbox-id>` — stop the EC2 instance, preserving infrastructure for restart (`--remote`); distinct from `km pause` (hibernate)
- `km extend <sandbox-id> <duration>` — extend a sandbox's TTL or change its idle timeout (`--idle`, `--remote`)
- `km list` — list sandboxes (narrow default, `--wide` for all columns, `--auth` for per-sandbox agent-login state, `--json`, `--tags`, `--reset` to reset local numbering)
- `km status <sandbox-id>` — detailed state, resources, and budget for a single sandbox
- `km logs <sandbox-id>` — tail CloudWatch audit logs for a sandbox (`--follow`, `--stream <name>`)
- `km agent <sandbox-id> --claude` — interactive Claude session via SSM (`--no-bedrock` for direct API)
- `km agent run <sandbox-id> --prompt "..."` — fire-and-forget non-interactive Claude in tmux (`--wait`, `--interactive`, `--no-bedrock`, `--auto-start`)
- `km agent attach <sandbox-id>` — attach to a running agent's tmux session (Ctrl-B d to detach)
- `km agent results <sandbox-id>` — fetch latest run output (`--run <id>` for specific run)
- `km agent list <sandbox-id>` — list all agent runs with status and output size (`--queue` to list on-box prompt queue entries instead)
- `km agent auth <sandbox-id> --claude|--codex` — authenticate the claude/codex CLI inside a sandbox via SSM (interactive login flow over the SSM session)
- `km at '<time>' <cmd>` — schedule deferred/recurring operations; supports `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run` (`km schedule` is an alias)
- `km at list` / `km at cancel <name>` — manage scheduled operations
- `km email send` — send signed email between sandboxes or to/from operator (`--from`, `--to`, `--cc`, `--use-bcc`, `--reply-to`)
- `km email read <sandbox>` — read sandbox mailbox with signature verification and auto-decryption (`--json`, `--mark-read`)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (`--prompts`, `--events`, `--tools`, `--timeline`)
- `km budget add <sandbox-id>` — top up a sandbox's compute or AI budget; auto-resumes the sandbox if it was budget-suspended
- `km slack init` — bootstrap Slack integration (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`); also caches `bot_user_id` at `{prefix}/slack/bot-user-id` (Phase 91)
- `km slack test` — end-to-end smoke test through the bridge
- `km slack status` — print SSM-backed Slack config
- `km slack invite <email>` — invite an email to a Slack channel; auto-detects native vs Connect (`--channel`, `--external`, `--dry-run`)
- `km slack manifest` — render a deployment-specific Slack App manifest to stdout (`--app-name`)
- `km slack rotate-token --bot-token <new>` — rotate Slack bot token + cold-start the bridge
- `km slack rotate-signing-secret --signing-secret <new>` — rotate Slack App signing secret
- `km slack adopt <alias> <channelID>` — seed the alias→channel DDB mapping for an externally-created channel so the next `km create` resolves it O(1) (Phase 104)
- `km github init` / `km github manifest` / `km github status` — manage the GitHub App bridge: cache bot-login + record bridge-url in SSM, render the App manifest JSON, print SSM-backed config
- `km h1 init` / `km h1 status` — manage the HackerOne webhook bridge: mint webhook secret + capture Basic-Auth creds, print SSM-backed config (no `manifest` subcommand — H1 has no App-manifest concept)
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port`)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox keypair without `km destroy && km create` (`--force`, `--yes`)
- `km desktop start <sandbox-id>` — open SSM port-forward to KasmVNC graphical session; prints `https://localhost:8444/` + credential (`--local-port`)
- `km desktop status <sandbox-id>` — check KasmVNC unit state on the sandbox
- `km desktop rekey <sandbox-id>` — rotate the per-sandbox KasmVNC password on a running sandbox (no restart / no session interruption; `--force`, `--yes`)
- `km desktop restart <sandbox-id>` — force a server-side restart of the KasmVNC session (Xvnc + WM + browser, like logging out of XFCE and back in) for a frozen/wedged desktop or stuck input; drops the live session (`--yes`)
- `km tunnel` — parent verb for carrying a network path from the operator's workstation into a sandbox. A **family**, not one command: every mode shares the SSM+SSH transport and differs only in what it forwards and what it provisions on the box
- `km tunnel k8s <sandbox-id> --context <kube-context>` — interactive sandbox shell carrying a reverse tunnel to a Kubernetes cluster only the operator's workstation can reach; credentials are minted locally by the operator's own exec plugin and proxied over a unix socket, so no VPN/SSO/AWS credential reaches the box (`--dry-run`, `--print-ssh`, `--verbose`, `--local-port`, `--bind-port`, `--kubeconfig`). Dies with the shell — no daemon mode by design
- `km tunnel socks <sandbox-id>` — interactive sandbox shell with a SOCKS5 proxy on the box's loopback (`--bind-port`, default 1080) egressing via the operator's workstation, so the box reaches whatever the VPN reaches. No broker, nothing written on the box. `--set-proxy-env` pre-sets ALL_PROXY/HTTPS_PROXY/HTTP_PROXY (off by default — it stops km metering AI spend). Wider than `k8s` by design; dies with the shell
- `km herdr start <sandbox-id>` — hold open the SSM+SSH transport for a Herdr remote attach; run `herdr --remote km-<id>` in another terminal (`--local-port`, `--no-install`)
- `km herdr status <sandbox-id>` — report sshd, authorized_keys, the herdr binary, and whether the box's km-presence carries signal 8
- `km cluster add --name <name> --oidc-provider-arn <arn>` — provision cross-account IRSA role (`--namespace`, `--service-account`, `--aws-profile`, `--region`, `--dry-run`, `--register-oidc-provider`)
- `km cluster list` — show configured cross-account cluster roles
- `km cluster rm <name>` — destroy a cluster IRSA role
- `km account add <name> --aws-profile <target-admin> --trust <home-account-id> --instance-types ...` — enroll a target AWS account as a capacity-borrowing link (target-account admin creds; `--provision-network --az-count N`, `--provision-efs`, `--enable-bedrock`, `--dry-run` defaults true)
- `km account register <name> --from-fragment <path> --aws-profile <home>` — register a link into the home account (home creds; writes `km-config.yaml` entry + SSM external-id + one read-only artifacts-bucket grant)
- `km account list` — list configured cross-account capacity links (never prints the external id)
- `km account rm <name> [--target-aws-profile <profile>] [--purge-backend]` — remove a link (home-only by default; `--target-aws-profile` also destroys the target-account footprint; `--purge-backend` deletes the shared state bucket/lock table)
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy, `--plan` to preview with destroy-class safety gate, `--dry-run=false` to actually apply, `--only <module>` for a scoped single-module apply; sugar: `--github` / `--slack` / `--h1` / `--email` (tier-1, env+IAM, no confirmation); `--only ses` (tier-2, destroy-class gated))
- `km bootstrap --shared-ses` — provision the shared SES rule set (idempotent; `--plan` previews with destroy-class safety gate)
- `km bootstrap --shared-secrets-key` — provision the shared KMS key for SOPS secret injection (one-time per install; `--plan` previews with destroy-class gate; see `docs/sandbox-secrets.md`)
- `km bootstrap --all` — chain foundation (SCP/KMS/artifacts) + shared SES rule set in one command; mutex with `--shared-ses`; `--plan` honors the destroy-class gate
- `km env [--aws-profile]` — print exportable `KM_*` block for `eval $(km env)` to drive terragrunt directly (excludes `AWS_PROFILE` by default; `KM_ACCOUNTS_TERRAFORM` intentionally omitted)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn`, `--ami`)
- `km ami list` / `km ami bake <sandbox-id>` / `km ami copy <ami-id> --region <dest>` / `km ami delete <ami-id>` — operator-baked AMI lifecycle
- `km rsync save|load|list|view|delete <sandbox-id>` — snapshot and restore the sandbox user's `$HOME` to/from S3
- `km roll creds` — rotate all platform + sandbox credentials
- `km configure` — interactive wizard to generate `km-config.yaml` (`km configure github` configures the GitHub App)
- `km uninit` — tear down all shared regional infrastructure for a region (`--yes`, `--force` if active sandboxes exist, `--include-scp`; NO `--dry-run` — confirmation is `--yes`)
- `km unbootstrap` — tear down platform-foundational resources: SSM params, buckets, KMS key (`--include-zone` to also delete the Route53 zone, `--kms-deletion-window`, `--yes`)
- `km info` — platform config, accounts, SES quota, AWS spend, DynamoDB tables
- `km doctor` — validate platform health (config, credentials, SES, Lambda, VPC, stale resources, AMIs, EBS, Slack inbound, presence daemon, etc.; `--all-regions`, `--backfill-tags`, `--ignore-prefix=<csv>` to treat sibling installs' cross-install resources as known)

## Architecture

- `cmd/km/` — CLI entry point
- `internal/app/cmd/` — Cobra commands
- `pkg/profile/` — Schema, validation, inheritance
- `pkg/compiler/` — Profile → Terragrunt artifacts
- `pkg/ebpf/` — eBPF enforcer (cgroup BPF programs, DNS resolver, audit consumer)
- `pkg/terragrunt/` — Terragrunt runner
- `pkg/aws/` — AWS SDK helpers (DynamoDB metadata, S3 artifacts, SES, EC2)
- `sidecars/` — HTTP proxy (MITM), DNS proxy, audit-log, tracing, km-presence
- `infra/modules/` — Terraform modules (`km-operator-policy`, `cluster-irsa`, `create-handler`, `ses`, etc.)
- `infra/live/` — Terragrunt hierarchy
- `profiles/` — Built-in SandboxProfile YAML files
- `skills/` — User-invocable skills (klanker plugin)
- `spec.runtime.additionalSnapshots` — list of snapshot-backed EBS volumes. Each entry materialises a fresh `aws_ebs_volume` from an existing EBS snapshot, attaches on `/dev/sd[f-p]`, mounts with userdata-detected filesystem. Coexists with `additionalVolume` (both can be set). EC2-only. Volume lifecycle = sandbox lifecycle. See `OPERATOR-GUIDE.md` § additionalSnapshots.

## Profile spec — Phase 92 structural cleanup

- **apiVersion bumped `v1alpha1` → `v1alpha2` (STRICT).** Profiles must declare `apiVersion: klankermaker.ai/v1alpha2`; `v1alpha1` is now rejected by the schema. No backwards compatibility (zero running sandboxes at cutover).
- **`spec.identity:` → `spec.iam:`.** The IAM/session block moved out of the `identity:` namespace. `iam.{roleSessionDuration, allowedRegions, allowedSecretPaths}` are the surviving fields.
- **`identity.sessionPolicy` removed** without replacement (it was never read by any code path).
- **`iam.allowedSecretPaths`** (Phase 89 SOPS SSM allowlist) is now declared in the JSON schema (closes the Phase 89 schema drift).
- **Dead top-level `spec.agent:` block removed** (`maxConcurrentTasks`, `taskTimeout`, `allowedTools` — never read). A new `agent:` block with structured tool-gating semantics is re-introduced later in Phase 92 (Waves 4/5).
- **`spec.cli.notify*` → `spec.notification:` (Wave 3, 2026-05-31).** The 15 notify/Slack fields moved out of `spec.cli` into a structured `spec.notification:` block: `notification.events.{onPermission,onIdle,cooldownSeconds}`, `notification.email.{enabled,address}`, `notification.slack.{enabled,perSandbox,channelOverride,archiveOnDestroy}` plus `slack.inbound.{enabled,mentionOnly,reactAlways}`, `slack.transcript.enabled`, and `slack.invites.{emails,useConnect}`. Surviving `spec.cli` fields: `noBedrock`, `agent`, `claudeArgs`, `codexArgs`. **Sandbox-side env var names (`KM_NOTIFY_*`, `KM_SLACK_*`) are UNCHANGED** — only the YAML surface changed; userdata output is byte-identical.
- **`spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled` (Wave 3).** `IsVSCodeEnabled` now takes a `*RuntimeVSCodeSpec`.
- `scripts/validate-all-profiles.sh` is the single-source-of-truth gate that runs `km validate` over the 20-file profile inventory (local-only; exits non-zero on any failure).

**Phase 117 (2026-06-24) — Composable multi-parent profile inheritance (complete):**
- `extends:` now accepts a **string** (back-compat) OR an **ordered `[]string` list** of parent names. Bases resolve left→right; child applies last.
- **deepMerge semantics (uniform):** maps recurse at every depth (key-union), scalars child/right-parent wins, lists concat+dedup (order-preserving, first occurrence kept). Union applies to every list field: `initCommands`, `allowedDNSSuffixes`/`allowedHosts`, `allowedRepos`/`allowedRefs`, `email.allowedSenders`, `agent.claude.tools.autoApprove`, `rsyncPaths`, `artifacts.paths`. Object lists (`additionalSnapshots`) de-dup by deep equality.
- The old hand-written typed merger zoo (`mergeNotificationSpec` / `mergeAgentSpec` / etc.) was replaced by a generic `deepMerge(map[string]any)` over a YAML round-trip. Thin wrappers kept for backward-compat test paths.
- **Abstract fragments** (`metadata.abstract: true`) live in `profiles/base/`. `km validate` skips them (exit 0 + SKIP message); `km create` rejects them. `validate-all-profiles.sh` excludes `profiles/base/` automatically.
- **`execution.initCommandsAppend`** appends leaf-specific install steps after the merged `initCommands`; `initCommands` itself always replaces (union of all bases + child).
- **Diamond inheritance** is memoized (each node resolved once). Max depth: 10; cycles rejected.
- **v1 narrowing limitation:** a child CANNOT shrink a base's list (lists union). To use a narrower allowlist, compose from a narrower base or keep the field in-leaf. A `!replace` directive is a deferred v2 follow-up.
- **Bool zero-value trap:** fragments that write full blocks with non-pointer bools push their zero-values (`false`) onto children — keep mixed-bool blocks like `spec.runtime` in the leaf.
- Shipped fragment library: `profiles/base/{safenetwork,sidecars-all,observability-learn,budget-standard,artifacts-workspace,iam-us-east-1,agent-claude-all-tools,email-strict}.yaml`. `learn.v2.*` and `dc34.yaml` compose 6 of these (email-strict kept in-leaf per narrowing limitation).
- `km validate` and `km create` both resolve the full DAG and validate the **merged bytes** (not raw child bytes — child alone fails required-field checks for partial profiles).
- **Deploy = `make build` only** (operator binary). No Lambda rebuild, no schema-on-write DDB change, no `km init`. Existing sandboxes unaffected (inheritance is resolved at `km create` time, not baked into infra).
- See `OPERATOR-GUIDE.md` § Composable inheritance for the full authoring guide, worked dc34 example, and v1 limitation details.

## Releases

Tag-driven via goreleaser + GH Actions. The `VERSION` file is the dev-build counter (auto-bumped by every `make build`); git tags (`vX.Y.Z`) are the release identity.

**Artifacts produced per release:** four tarballs (`km_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.xz`), each bundling `km` + `terraform` v1.9.8 + `terragrunt` v0.99.1 + `LICENSE` + `README.md` + `OPERATOR-GUIDE.md` + `THIRD-PARTY-LICENSES.txt` + the `containers/operator/` build files + `profiles/**` + **`infra/**`** (the terragrunt tree, needed by the operator image). Plus **`km_vX.Y.Z_lambdas.tar`** — the ten linux/arm64 Lambda deployment zips (~217MB), built by `scripts/build-release-lambdas.sh` and consumed only by `containers/operator/Dockerfile`. Plus a SHA256 checksums file covering all of them. Operators still provide `aws` CLI + `session-manager-plugin` themselves.

**The lambdas asset is deliberately uncompressed and deliberately separate.** Uncompressed because the payload is ten already-deflated zips — measured, `xz` turned 217MB of input into a 225MB file while costing minutes of CI time. Separate rather than folded into the four platform archives because it is linux/arm64 regardless of the operator's platform, so bundling would quadruple it for no one's benefit. `scripts/build-release-lambdas.sh`'s Lambda list is pinned to `lambdaBuilds()` by `TestReleaseLambdaScript_TracksLambdaBuilds` — a Lambda added to the Go list but not the script would otherwise ship an asset silently missing a zip, and the operator image would fail `km init --plan` on that one module with an error naming terraform rather than the release pipeline.

**Cut-a-release workflow:**

1. **Pre-flight:** verify `main` is green, GSD milestone is at a clean checkpoint, `CHANGELOG`-worthy commits use conventional-commit prefixes (`feat:`, `fix:`, `docs:` — goreleaser groups by these). **Update `docs/RELEASE-HIGHLIGHTS.md`** with this release's curated "✨ Major additions highlighted" items — auto-draft a starting point with `scripts/draft-release-highlights.sh` (pulls new phase blocks from CLAUDE.md since the last tag + lists non-phase feat/fix candidates), then trim/add emoji. It's injected verbatim into the release notes above the auto-changelog (workflow loads it → `$KM_RELEASE_HIGHLIGHTS` → `.goreleaser.yaml` `release.header`). The mechanism is always-on; a stale file = stale highlights. See `docs/release.md` § Release highlights.
2. **Local sanity check (no tag):**
   ```bash
   goreleaser check
   goreleaser release --snapshot --clean
   ls dist/ && tar -tJf dist/km_v*_darwin_arm64.tar.xz
   ```
3. **Tag and push:**
   ```bash
   git tag vX.Y.Z              # or vX.Y.Z-rc1 for prerelease (auto-flagged)
   git push origin vX.Y.Z
   ```
4. **GH Actions runs `.github/workflows/release.yml`** → cuts a **Draft** release. Review assets, then publish manually from the GH UI.
5. **Post-release:** bump the klanker plugin version (`plugin.json` + `marketplace.json`) in lockstep if any skill content changed — clients cache the old version otherwise (see [[project_plugin_version_gates_cache]]).

**Pinned bundled-tool versions:** `terraform` 1.9.8, `terragrunt` 0.99.1. Bumping these is a one-line edit to `.goreleaser.yaml` `before.hooks` args + the cache-key in the workflow.

**Files:** `.goreleaser.yaml` (release config), `scripts/fetch-bundled-tools.sh` (per-platform tool fetcher, cached at `~/.cache/km-bundle/`), `scripts/draft-release-highlights.sh` (auto-draft highlights from CLAUDE.md phase blocks since last tag), `docs/RELEASE-HIGHLIGHTS.md` (injected into release notes), `.github/workflows/release.yml` (tag-triggered).

Full runbook + troubleshooting: `docs/release.md`.

## SES per-install rule namespacing

km supports multiple installs in a single AWS account via SES rule namespacing. Each install owns a unique `resource_prefix` and per-prefix SES receipt rules under a single account-shared rule set.

**Operator address format:** `operator-{resource_prefix}@{email_subdomain}.{domain}`
Example: `operator-km@sandboxes.example.com` for the default install; `operator-km2@sandboxes.example.com` for a second install with `resource_prefix: km2`.

**Shared rule set:** `sandbox-email-shared` — account-shared, owned by `infra/modules/ses-shared-rule-set/v1.0.0/`, has `lifecycle.prevent_destroy = true`. Provisioned once per account/region by `km bootstrap --shared-ses`; idempotent on re-apply.

**Per-install rules:** Each install adds exactly two rules to the shared rule set:
- `{prefix}-operator-inbound` — routes `operator-{prefix}@` to the operator Lambda
- `{prefix}-sandbox-catchall` — routes all other `{sandbox-id}@` addresses to sandbox mailboxes

`km uninit` removes only this install's two rules and leaves the shared rule set and sibling installs' rules intact.

**Doctor check:** `km doctor` reports `✓ SES rules healthy` when all rules in the shared rule set map to a known `resource_prefix`, or `⚠ orphan SES rules: <list>` when rules exist for prefixes not in the local `km-config.yaml`. The orphan check is WARN-level — expected when a sibling install is present.

## Plan-before-apply destroy-class gate

`km init --plan` and `km bootstrap --shared-ses --plan` run real `terragrunt plan` per module with a curated destroy-class safety gate that trips on destroy/replace of protected resource types (SES identities, Route53 records, S3 buckets, DynamoDB tables, KMS keys, etc.). `--i-accept-destroys` is the per-invocation override (never persisted; does not auto-apply). `km doctor` nudges toward `--plan` before any apply.

See `OPERATOR-GUIDE.md` § Plan-before-apply for the trip-block format, override flow, and protected-type list.

## Wrapper-level bootstrap UX

The path from `git clone` to first apply is shaped by:

**Configure-time (`km configure`):**
- HeadBucket-checked `state_bucket` with `[Y/edit/abort]` retry UX on globally-taken names.
- Auto-derived `artifacts_bucket = ${prefix}-artifacts-${account_id}`; angle-bracket and literal `km-artifacts-12345` placeholders rejected at load.
- `Next steps:` finale prints the canonical bootstrap sequence to stdout AND embeds it as `#` header comments at the top of the generated yaml.
- Shell-env conflict WARN per conflicting `KM_*` env var.

**Bootstrap-time (`km bootstrap`):**
- Dry-run text correctly says `would run: terragrunt apply`; degrades gracefully when AWS auto-detect is unreachable.
- Status banner WARNs on empty required account IDs and shows `(not set)`.
- `--all` chains the foundation + shared SES rule set subflows.

**Init-time (`km init`):**
- Per-var drift WARN on env-vs-yaml mismatch.
- `km init --plan` skips fresh-install dependents missing upstream `outputs.json` — exit 0, re-runs cleanly once `network` is applied.
- Hard-fail on missing `artifacts_bucket` with recovery commands in error.

**`accounts.*` yaml-authoritative behavior:**
- `accounts.organization`, `accounts.dns_parent`, `accounts.application`: yaml wins, env values do NOT override `cfg`. `KM_ACCOUNTS_*` still exported to terragrunt subprocesses.
- `accounts.terraform`: env wins (asymmetry preserved — operators retain shell-local override for the cross-account terraform role).

## Network Enforcement

Three enforcement modes via `spec.network.enforcement`:
- `proxy` (default) — iptables DNAT → userspace proxy sidecars
- `ebpf` — cgroup BPF programs (connect4, sendmsg4, sockops, egress) with LPM trie allowlist
- `both` — eBPF primary + proxy for L7 inspection (Bedrock metering, Anthropic metering, OpenAI metering, GitHub filtering)

eBPF SSL uprobes provide passive TLS plaintext capture for audit/observability alongside enforcement.

## Budget Metering Coverage (Phase 88)

http-proxy MITM intercepts and meters three AI provider endpoints into the same
`BUDGET#ai#{modelID}` DynamoDB row shape:

- **Bedrock InvokeModel** (`bedrock-runtime.*.amazonaws.com`) — Claude on Bedrock (Phase 6)
- **Anthropic direct API** (`api.anthropic.com`) — Claude Code (Phase 20)
- **OpenAI direct API** (`api.openai.com`) — Codex CLI + raw OpenAI SDK (Phase 88)

L7 proxy hosts are gated per profile:
- `spec.execution.useBedrock: true` → adds `.amazonaws.com,api.anthropic.com`
- `spec.agent.default: codex` → adds `api.openai.com` (Phase 88; field re-homed from `spec.cli.agent` in Phase 92)

Unknown model IDs in any provider write rows with `spentUSD=0` and log
`event_type=*_unknown_model` so operators see the gap in `km status`.

## Learn Mode

Generate a minimal SandboxProfile from observed traffic:

```bash
km create profiles/learn.yaml          # wide-open sandbox with learnMode + privileged
km shell --learn <sandbox-id>          # observe traffic + commands, generate profile on exit
cat learned.*.yaml                     # annotated profile with DNS suffixes, initCommands
km validate learned.*.yaml             # validate before use
```

- `profiles/learn.yaml` — permissive profile with broad TLD suffixes, `enforcement: both`, `privileged: true`, `learnMode: true`
- `spec.execution.privileged` — grants sandbox user wheel/sudo access (any profile)
- `spec.observability.learnMode` — enables eBPF traffic recording (`--observe` on enforcer)
- `--learn` triggers SIGUSR1 flush on the enforcer to snapshot observations to S3

### Bake AMI on exit

```bash
km shell --learn --ami <sandbox-id>    # observe traffic + snapshot to AMI on exit
cat learned.*.yaml                     # generated profile now includes spec.runtime.ami: ami-xxxxxxxx
km validate learned.*.yaml
km create learned.*.yaml               # spin up a new sandbox from the baked AMI
```

`--ami` requires `--learn`. The bake fires before the SIGUSR1 flush so the AMI ID can be embedded in the generated profile. AMIs are private to the application AWS account, tagged with sandbox metadata, and tracked by `km ami list` / `km doctor` (stale check). `spec.runtime.ami` accepts both slugs (`amazon-linux-2023`, `ubuntu-24.04`, `ubuntu-22.04`) and raw AMI IDs. When launching from a baked AMI that already declares `/dev/sdf` in its block device mappings, the compiler auto-rotates `additionalVolume` onto the next free device (`/dev/sdg`..`/dev/sdp`) so launches don't collide.

## Agent Execution

Run AI agents non-interactively inside sandboxes. Agents run in persistent tmux sessions that survive SSM disconnects.

```bash
km agent run <sandbox> --prompt "fix the failing tests"                          # fire-and-forget
km agent run <sandbox> --prompt "What model are you?" --wait                     # blocking, prints JSON
km agent run <sandbox> --prompt "refactor the auth module" --interactive         # attach live
km agent attach <sandbox>                                                         # attach to existing
km agent results <sandbox>                                                        # latest run
km agent results <sandbox> --run 20260410T143000Z | jq '.result'                  # specific run
km agent list <sandbox>                                                           # all runs
km agent run <sandbox> --prompt "..." --no-bedrock --wait                         # direct API (needs claude login)
km at '5pm tomorrow' agent run <sandbox> --prompt "..." --auto-start              # scheduled
```

Output lands at `/workspace/.km-agent/runs/<timestamp>/output.json`. Detach from interactive with `Ctrl-B d` — the agent keeps running.

### Profile configuration

```yaml
spec:
  # Phase 92: Claude/Codex tool gating + trustedDirectories are TYPED here, not
  # inlined as configFiles JSON. The compiler synthesizes ~/.claude/settings.json
  # (canonical permissions.allow/deny) + ~/.codex/config.toml. See docs/agent-tool-gating.md.
  agent:
    claude:
      trustedDirectories: [/home/sandbox, /workspace]
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep]
  execution:
    configFiles:
      # OTHER config files are still inlined here; only the Claude settings.json
      # key is forbidden (it is synthesized from spec.agent.claude).
      "/home/sandbox/.claude/plugins/known_marketplaces.json": |
        {"...": "..."}
  cli:
    noBedrock: true    # default to direct API for km shell / km agent run
```

- `spec.agent.claude.tools.*` / `trustedDirectories` — typed Claude tool gating; synthesized into `~/.claude/settings.json` (Phase 92). Inlining `configFiles["/home/sandbox/.claude/settings.json"]` alongside these is a hard validation error.
- `spec.execution.configFiles` — pre-seed OTHER tool config files (written after `initCommands`, owned by sandbox user)
- `spec.cli.noBedrock` — operator-side default; doesn't affect sandbox provisioning, only CLI behavior when connecting

### Agent: claude | codex (Phase 70; restructured Phase 92)

`spec.agent.default` selects the default agent for `km shell` / `km agent run` /
Slack inbound dispatch:

```yaml
spec:
  agent:
    default: codex  # or "claude"; default claude; absence ≡ claude
```

The compiler writes `KM_AGENT` to `/etc/profile.d/km-notify-env.sh` and
`/etc/km/notify.env`. It also synthesizes `~/.codex/config.toml` on every sandbox
regardless of value (via `synthesizeCodexConfig`, Phase 92) — Claude-default
sandboxes get an inert config (forward-compat for when Codex ships a
Claude-Code-style hook API).

Per-turn override via Slack: a message starting with `claude:` or `codex:`
selects the agent for that turn (case-insensitive, anchored at start, zero or
one space after colon). Inside an existing thread, naming the *other* agent
triggers an 8-step clean handoff to a new top-level message. See
`docs/codex-parity.md` for the full switch sequence.

**`km init --sidecars` is required** after this phase ships so management
Lambdas pick up the schema addition. Existing sandboxes don't pick up
`agent.default: codex` retroactively — `km destroy && km create`.

### DDB column hangover (Phase 70)

The `km-slack-threads.claude_session_id` column (Phase 67) now stores
agent-agnostic session IDs — either a Claude session ID or a Codex session ID,
based on the row's `agent_type`. The column name is a Phase 67 hangover;
renaming would require a migration job we chose not to run (cosmetic only).

Future agents (Goose etc.) slot in as new `agent_type` enum values without
further DDB schema work.

### Phase 72: Corporate workspace support — auto-detect invite + manifest generator

Adds three capabilities for installing klankermaker into corporate Slack workspaces (where
invitees are native workspace members rather than external collaborators):

- `km slack manifest` — generates a deployment-specific Slack App manifest including the new
  `users:read.email` scope. Pipe to a file and paste into Slack admin "From manifest" UI.
- `km slack invite <email> [--channel <name|id>] [--external] [--dry-run]` — ad-hoc command to
  add people to channels post-install. Auto-detects whether to use `conversations.invite`
  (native) or `conversations.inviteShared` (Slack Connect, requires Pro tier). `--dry-run` is a
  read-only probe: classifies the address without sending any invite or joining any channel.
- `spec.notification.slack.invites.emails: []string` — profile field that auto-invites ADDITIONAL
  people (beyond the always-invited primary operator) to the per-sandbox `#sb-{id}` channel after
  `km create` succeeds. Auto-detects native vs Connect.
- `spec.notification.slack.invites.useConnect: *bool` (default true) — gates the Connect fallback for the
  `notification.slack.invites.emails` loop only. True: external addresses auto-Connected. False:
  external addresses skipped with a fail-soft warning + follow-up command. Does NOT affect the
  primary operator invite (always invited) or `km slack invite`/`km slack init`.

**New required Slack bot scope:** `users:read.email`. Existing PoC installs need a one-time
manifest update + reinstall + token rotation:

```bash
km slack manifest > /tmp/km-app.json
# Paste into Slack admin → Apps → existing app → App Manifest → Save → Reinstall
km slack rotate-token --bot-token <new-token>
km doctor
```

**`km doctor` adds:** `slack_users_read_email_scope` (WARN if missing).

See `docs/slack-notifications.md` § Phase 72 for the full operator guide.

### Phase 91: Polite-bot — @-mention-only inbound mode

Introduces polite-bot mode so the bridge does not react to every message in shared team
channels. The effective behaviour is determined by the channel mode + optional profile override:

| Mode | Channel | Default |
|------|---------|---------|
| 1 | Shared (e.g. `#km-notifications`) | mention-only |
| 2 | Per-sandbox `#sb-{id}` | every-message (back-compat) |
| 3 | Operator override (`notification.slack.channelOverride`) | mention-only |

- `spec.notification.slack.inbound.mentionOnly: *bool` — per-profile tri-state override: nil = mode
  default, `true` = force polite, `false` = force chatty. Omit for smart per-mode behaviour.
- `KM_SLACK_MENTION_ONLY` — install-level Lambda env var (`"true"`/`"false"`; default `"false"`).
  **Phase 91.1:** populated from `km-config.yaml` key `slack.mention_only` automatically by
  `km init`. No `export` required; env-wins drift WARN preserved.
- `KM_SLACK_BOT_USER_ID` — bot user ID for the mention scan. **Phase 91.1:** auto-read from
  SSM `{prefix}slack/bot-user-id` by `km init` (populated by `km slack init`); no `export`
  required. First-install skips silently with `[info]` line.
- **`km doctor` adds:** `slack_bot_user_id_cached` (WARN if missing when mention-only is active).
- **Phase 91.3 (km v0.3.772+):** thread-bypass for the mention scan. Once the bot dispatches
  a turn in a thread (sandbox_id is written to km-slack-threads at upsert time), every
  subsequent reply in that thread bypasses the mention requirement. Threads are 1:1
  conversations with the bot — re-@-mentioning was unnatural. Top-level messages and
  replies in unknown threads still require mention.
- **Phase 91.4 (km v0.3.773+):** first-only reactor toggle. `slack.react_always: false`
  in km-config.yaml flips the install-level default so the bridge posts 👀 only on
  top-level engagement messages — thread replies dispatch silently. Profile field
  `notification.slack.inbound.reactAlways *bool` shipped for forward-compat with future
  per-sandbox routing; runtime behaviour today is install-level.
- **Phase 91.5 (km v0.3.776+):** per-sandbox `notification.slack.inbound.reactAlways` override.
  When the profile sets the field explicitly, `km create` writes `slack_react_always`
  to the sandbox's `km-sandboxes` row; the bridge's `FetchByChannel` reads it and the
  per-sandbox value wins over the install-level default at step 10. Profile field is
  now first-class — set it on individual profiles to deviate from `slack.react_always`.
  Top-level engagement messages still always 👀-react regardless (the signal can't be
  silenced).

**Why `km init` and not `km init --sidecars`:** the bridge Lambda's `environment.variables`
block is owned by the `lambda-slack-bridge` Terraform module, which only updates on full
terragrunt apply (`km init`). `--sidecars` rebuilds binaries and forces a Lambda cold-start
but does NOT update the env block — so flipping `slack.mention_only` requires
`km init --dry-run=false`.

Rollout after upgrading to a Phase 91.1 build:

```bash
make build
km slack init --force          # re-runs auth.test, caches bot_user_id at SSM
# Edit km-config.yaml — add:
#   slack:
#       mention_only: true
km init --dry-run=false        # km auto-reads slack.mention_only + SSM bot_user_id; terragrunt apply
km doctor
km destroy <sandbox-id> --remote --yes && km create <profile>   # existing sandboxes pick up new field
```

See `docs/slack-notifications.md` § Phase 91 for the full operator guide and troubleshooting.

### Phase 95: Federated bridge relay — one Slack App, many km installs

Adds opt-in static relay so a single Slack App registration serves multiple km installs.
Slack delivers all events to one "front door" install; that install broadcasts unknown-channel
events to peer bridges until the owning install processes them.

- `slack.peer_bridges: []string` — list of sibling install `/events` URLs in `km-config.yaml`.
  Absent or empty = federation off = byte-identical behaviour to pre-Phase-95.
- `KM_SLACK_PEER_BRIDGES` — comma-joined Lambda env var written by `km init`. Only updated on
  a full `km init --dry-run=false` (NOT `km init --sidecars` — env-block change requires full
  terragrunt apply).
- **Deploy sequence:** `make build-lambdas` (clean) then `km init --dry-run=false` on each
  install where `slack.peer_bridges` changed.
- **Loop guard:** `X-KM-Relayed: 1` header makes relayed requests terminal — never re-relayed.
- **`km doctor`:** `slack peer bridges` warns on malformed peer URL or self-loop (peer URL ==
  own bridge URL); skips when the list is absent/empty.
- **Correctness invariant:** channel name/alias uniqueness across all installs is required.
  Per-sandbox `#sb-{id}` channels are safe by construction; shared named channels must be
  registered on exactly one install.

See `docs/slack-notifications.md` § Phase 95 for the full operator guide, setup flow, and troubleshooting.
