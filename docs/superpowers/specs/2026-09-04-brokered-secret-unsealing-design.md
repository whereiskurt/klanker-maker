# Phase 133 — Brokered secret unsealing: secrets leave the environment

**Status:** design approved, not yet planned
**Branches from:** `main`
**Design date:** 2026-09-04

---

## 1. The problem

Malware has landed on sandboxes and dumped environment variables. Today every
secret in a profile's SOPS bundle is in that dump.

At boot, `pkg/compiler/userdata.go:1221` decrypts `spec.secrets.sopsFile` into
`/etc/sandbox-secrets.env` (`0440 root:sandbox`) and writes
`/etc/profile.d/zz-sandbox-secrets.sh`, which auto-exports every key into every
login shell. Agent turns dispatch through `runuser -u sandbox -- bash -lc`
(userdata.go:1884, 2572, 2992, 3345) — a **login** shell — so the full bundle is
resident in the agent's environment, in every interactive `km shell`, and in the
environment of every process either one ever forks. A `cat /proc/*/environ`, or
simply reading the file, yields everything.

The goal: **no credential materialised anywhere on the box unless something
actively asks for it, and then only in the one process that needs it.**

### 1.1 The finding that shapes everything

The distinction between "plaintext on disk" and "ciphertext on disk" is thinner
than it looks. The sandbox instance role permanently holds **both** halves of the
decrypt:

- `kms:Decrypt` on `alias/{prefix}-sandbox-secrets`
  (`infra/modules/ec2spot/v1.6.0/main.tf:904-921`)
- `s3:GetObject` on `sandboxes/{id}/secrets.enc.yaml` (`main.tf:927-941`)

Any process that can reach IMDS — including malware running as `sandbox` — can
re-download the bundle and decrypt it itself. Moving decryption from boot-time to
call-time removes the **dump**. It does not remove the **authority**.

Removing the authority is therefore a separate, harder problem, and it has a hard
constraint: **AWS has no notion of which uid on an instance is making a call.**
An instance role is an instance role. The only place a uid-granular fence can
exist is the OS/network layer.

---

## 2. Non-goals

- **Not a wall against code running as `sandbox`.** Once uid `sandbox` can reach
  the broker socket at all, anything running as `sandbox` can ask it. See §6.
- **Not proxy-side credential substitution.** km already MITMs
  `api.anthropic.com` / `api.openai.com` to meter them, so the proxy *could* hold
  the AI keys and inject `Authorization`, leaving nothing real on the box at any
  time. Considered and deliberately deferred — see §10.1. This phase accepts a
  one-process, one-turn environ window.
- **Not tamper-proof on `privileged: true`.** sudo defeats the fence, the shims,
  and the unit. Identical in kind to the Wiz sensor limitation
  (`docs/wiz-sensor.md`): the real control is `privileged: false`.
- **Not anomaly detection or automated response.** Unseals are audited; no rate
  rules, no freeze integration. See §10.2.
- **Not a change to `iam.allowedSecretPaths`.** That path (userdata.go:523)
  exports only into cloud-init's own root process and never reaches a sandbox
  shell. It is already better than the SOPS path and is out of scope.
- **Not a change to `spec.execution.env`.** Those values are plaintext in the
  profile YAML, so they were never secrets. A `km validate` WARN is a reasonable
  follow-up, not part of this phase.

---

## 3. Decisions

### 3.1 The boundary is a root broker plus an opt-in IMDS fence

Rejected: a `km-env` helper that only moves decryption to call time. It closes
the dump but leaves every process able to re-derive the bundle through IMDS, so
the guarantee it appears to offer is false.

Accepted: decrypt authority moves to a root-owned daemon, and — under an opt-in
gate — uid `sandbox` is fenced from IMDS so the daemon is the only door.

### 3.2 Decrypt per request; zero after; nothing resident

Rejected: decrypt once at boot and hold plaintext in the daemon's RAM. Fast and
network-independent, but plaintext is then resident for the box's entire uptime,
which is exactly the condition being removed, and a root compromise at any moment
yields everything with no trace.

Rejected: a short TTL cache. Fewer KMS calls per turn, but it widens the memory
window from milliseconds to a minute and degrades the CloudTrail record from
per-use to per-window.

Accepted: **every unseal is a live `kms:Decrypt`; the buffer is zeroed
immediately after the response is written.** Only ciphertext is at rest. Every
unseal lands in CloudTrail as a discrete, attributable API call — off-box
evidence, the same disposition as the idle-reap incident, where the record that
mattered lived in CloudWatch and not on the box.

Cost, accepted: ~10ms per unseal, and secrets are unavailable during a network
partition. That failure is loud (§7) rather than silent.

### 3.3 Consumers are wrapped; shells get nothing

Rejected: auto-unsealing at the `dispatch_as_sandbox` chokepoint. One template
edit and no shims — but the whole turn's `bash -lc` then holds the bundle, so
anything the agent shells out to inherits it, including a tool it was tricked
into running. That is a smaller version of today's problem, not a fix.

Rejected: requiring an explicit `km-env` for everything including the agent.
Strictest reading, but autonomous turns (Slack/GitHub/webhook-triggered, no human
present) then depend on the dispatch site or the agent's own skill remembering to
call it — so the agent can still be talked into unsealing, and the failure mode
when it is forgotten is a confusing 401.

Accepted: **`claude`/`codex` (and any operator-named consumer) are intercepted by
a shim that unseals into exactly that process.** "Only the agent and its
children" becomes literally true, autonomous turns work unchanged with no human
action, and interactive shells hold nothing. This is the same idiom as
`km-git-askpass` — a helper fetches at call time so the agent never handles a raw
token.

### 3.4 `grants` names both the interception and the key subset

A `grants` key is simultaneously the **binary name intercepted on PATH** and the
**identity presented to the broker**. One name, two jobs, no second list to keep
in sync.

Default (`grants` absent) is the full bundle to `claude` and `codex` — the
identical effective grant to today, merely scoped to one process instead of every
shell. Narrowing is available, never mandatory, so migration breaks nothing.

Rejected: mandatory explicit grants. Tightest by construction, but it forces a
breaking migration and turns a forgotten key into a runtime auth failure inside an
agent turn rather than an error at `km validate`.

### 3.5 The broker is default-on; the fence is opt-in

The broker and shims can only narrow — they remove an environment variable that
was previously everywhere. Gating that behind a profile field would leave every
existing profile dumping secrets into every shell until someone edited it, which
is the opposite of the intent. This is the same reasoning that un-gated
`km-netpolicy`: a facility that can only narrow cannot widen a policy by being
present.

The fence is different. It genuinely changes AWS access for uid `sandbox` and
will break any helper path the credential re-homing misses — and the symptom is
"the agent silently can't post to Slack", not a loud error. So it ships behind
`spec.secrets.fenceIMDS` (default `false`), soaks on real boxes, and flips
default in a follow-on phase. This is the Phase 111 → 112 pattern.

### 3.6 Failure is fatal at boot

The current behaviour — a bad bundle aborts the boot — is preserved and extended.
A half-working box that looks healthy and fails at first turn is worse than a box
that never came up, because the former can be handed to an autonomous trigger.

---

## 4. Architecture

```
  /etc/sandbox-secrets.enc.yaml         ciphertext, 0400 root, at rest
            │
            │  kms:Decrypt per request, buffer zeroed after
            ▼
    km-secretsd  (root daemon)
    /run/km/secrets.sock                unix, 0660 root:sandbox, SO_PEERCRED
            │
            │  Unseal(as, keys)
            ▼
      km-env  (uid sandbox)  ──execve──▶  claude / codex / one command
            ▲
            │  invoked by
    /opt/km/shims/{claude,codex,...}     root-owned 0755, first on PATH
```

Deleted outright: `/etc/sandbox-secrets.env` and
`/etc/profile.d/zz-sandbox-secrets.sh`. No login shell — interactive, `runuser
-lc`, or otherwise — carries a secret again.

### 4.1 `km-secretsd`

Root systemd daemon. Listens on `/run/km/secrets.sock`, `0660 root:sandbox`, peer
credentials read via `SO_PEERCRED`.

Two RPCs:

- `Unseal(as string, keys []string) → map[string]string` — resolves the effective
  key set (§5.2), performs a live `kms:Decrypt`, writes the response, zeroes the
  plaintext buffer.
- `Credentials() → STS credential JSON` — fence mode only, §4.4.

**Decrypt via the `getsops/sops` Go API, not by shelling to `/opt/km/bin/sops`**
(operator decision, 2026-09-04), so plaintext never transits a pipe or a second
process's memory and the buffer stays ours to zero. The sidecar cross-compile
requires `CGO_ENABLED=0` (the same constraint that forced pure-Go `AF_PACKET` on
`km-capture`); if the library will not build under it, fall back to exec with a
pipe, record it as debt, and note that the zeroing guarantee then covers only our
side of the pipe.

Also carries the `selftest` verb (§7).

### 4.2 `km-env`

The sandbox-side verb.

- `km-env exec [--as NAME] [--only K1,K2] -- <cmd> [args...]` — unseals, `execve`s
  the command with an augmented environment, writes nothing to disk.
- `km-env list` — prints key **names** only, never values.

`--only` **narrows within** what `--as` already permits and can never widen it:
the effective set is the intersection, and a key absent from the `--as` identity's
grant is not returned by naming it. Narrowing-only is the same disposition as
`km-netpolicy deny` and `pin` — every runtime lever in this platform can tighten
and none can loosen.

**There is deliberately no `km-env export` and no `eval $(km-env)` form.** That
would put the bundle straight back into a shell, which is the entire thing being
removed. Same disposition as `km-netpolicy` having no un-deny verb and `pin`
having no un-pin verb, and pinned the same way: a test fails if such a verb is
added.

### 4.3 Shims and the PATH prepend

For each effective consumer — the keys of `grants`, or `claude` and `codex` when
`grants` is absent (§5.1) — userdata generates:

```sh
#!/bin/sh
exec /opt/km/bin/km-env exec --as claude -- /home/sandbox/.nvm/versions/node/v22.x/bin/claude "$@"
```

Three mechanics, each a silent-failure source if got wrong:

1. **The shim execs an absolute path, never the bare name** — otherwise it
   re-finds itself on PATH and recurses. If the baked path is missing at exec time
   (userdata re-runs Claude's `install.cjs` idempotently and can relocate the
   binary), it falls back to searching PATH with the shim directory removed.
   Baked-first for speed, search-fallback for durability.
2. **All five `dispatch_as_sandbox` definitions gain an explicit
   `PATH=/opt/km/shims:$PATH`.** Relying on `/etc/profile.d` ordering is not
   sufficient: nvm's own profile.d script *prepends* `~/.nvm/versions/node/*/bin`
   and would win, and the tmux dispatch at userdata.go:3904 uses `bash -c`
   (non-login) and never sources profile.d at all. Either would leave the shim
   inert with no error — precisely the Phase 131/132 failure shape of a facility
   that is dead under the default configuration and invisible to tests that assert
   string presence. Explicit in all five, pinned by a guard test (§8).
3. **`/etc/profile.d/zz-km-shims.sh`** prepends the same directory for interactive
   `km shell`.

`dispatch_as_sandbox` is a single named chokepoint defined five times (four SQS
pollers plus the tmux twin) and covering eighteen call sites. **The eighteen call
sites need no edits** — the shim is what makes `km-env` innermost, so the turn
shell receives a PATH entry and nothing else.

### 4.4 The fence (`spec.secrets.fenceIMDS`, default false)

Two halves that are only meaningful together.

**Half one — block the direct path.**

```
iptables -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT
```

In the **filter** table, independent of the existing nat-table IMDS exemption at
userdata.go:5035. The uid-owner idiom is already load-bearing in this file
(userdata.go:5040-5050).

**Half two — re-home the helpers.** `km-github`, `km-slack` and `km-h1` all read
SSM as uid `sandbox`, so a bare fence breaks them. `km-creds` is a
`credential_process` helper: one line in the sandbox user's `~/.aws/config`, and
every AWS SDK picks it up transparently — **no helper binary changes at all.** It
asks the broker, which calls `sts:AssumeRole` **on the instance role itself** with
an inline session policy carrying two explicit Denies:

- `kms:Decrypt` where `kms:ResourceAliases = alias/{prefix}-sandbox-secrets`
- `s3:GetObject` on `sandboxes/{id}/secrets.enc.yaml`

**Self-assume rather than a parallel role is load-bearing.** The narrowed
credentials are then *definitionally* the instance role minus two Denies, so the
two can never drift. A second role would need the instance role's whole policy set
duplicated and kept in sync forever — the exact drift class that
`TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant` exists to prevent elsewhere.

Cost: the trust policy must name its own ARN, which is a Terraform cycle. Broken
by constructing the ARN string from `data.aws_caller_identity` plus the
deterministic role name rather than referencing the resource. Role chaining caps
sessions at one hour; `credential_process` refreshes, so this is invisible.

The two Denies are precise. The separate `kms:Decrypt` grant for SSM SecureStrings
(`main.tf:447`, conditioned on `kms:ViaService = ssm`) targets a different key and
does not match either condition, so helper SSM reads keep working.

---

## 5. Schema

```yaml
spec:
  secrets:
    sopsFile: ./secrets/codex.enc.yaml
    fenceIMDS: false            # opt-in; flips default in a follow-on phase
    grants:                     # optional; absent ⇒ claude+codex, full bundle
      claude: [ANTHROPIC_API_KEY]
      codex:  [OPENAI_API_KEY]
      deploy: [DB_PASSWORD, DB_HOST]
```

Purely additive. **No `apiVersion` bump.**

### 5.1 `grants` semantics

Each key names a binary to intercept **and** the identity presented to the broker.
Adding a key to `grants` therefore has a PATH side effect — this is intentional
(one list, not two), and is called out here because it is surprising on first
reading.

A `grants` key that resolves to no binary on the box is a **warning, not a
failure**: `initCommandsAppend` may install it later, and because no shim is
generated, nothing is silently broken.

### 5.2 What `grants` does and does not do

`grants` shapes **what the agent process receives**. It does not shape what a
determined caller can obtain. A bare `km-env exec -- sh` still receives the full
bundle by default, and anything running as `sandbox` can claim `--as claude`.

This is consistent with §6 and is worth stating in the operator docs verbatim, so
that nobody later reads `grants` as a containment boundary. Its value is blast
radius and audit legibility.

---

## 6. What this is honest about

**The broker is a logged door, not a wall.** Uid `sandbox` must be able to reach
the socket for any of this to work, and a process running as `sandbox` can speak
the socket protocol directly rather than going through a shim. Peer credentials
give the broker uid and pid, and it can inspect `/proc/PID/exe`, but `km-env` is
the exe in every legitimate case, so that check distinguishes nothing an attacker
cannot fake.

What actually changes is the **character** of the theft. Today it is a passive
scoop of a file or an environ that leaves no trace anywhere. Afterwards it is an
active, authenticated, pid-attributed request that writes a `secret_unseal` event
to the sandbox's CloudWatch audit stream and a discrete `kms:Decrypt` to
CloudTrail — joinable against Phase 132's exec capture to name the process that
asked. That is detection and forensics, and it is a real gain, but it is not
prevention.

**On `privileged: true` none of it holds.** The agent has sudo: it can stop
`km-secretsd`, rewrite the shims, flush the iptables rule, or read the ciphertext
and call KMS itself. `km validate` WARNs when `fenceIMDS` is set on a privileged
profile. The real control is `privileged: false`.

**The agent process still holds its keys in environ for the turn.** Malware running
as `sandbox` can read a same-uid process's `/proc/PID/environ`. This phase moves
the exposure from *every shell, forever* to *one process, one turn* — the bulk of
the win against the observed attack, and not elimination. §10.1 is how that gap
closes.

---

## 7. The boot check

One verb, `km-secretsd selftest`, run from two places.

- **Boot** — a root block inside userdata, immediately after `km-secretsd` starts.
  Userdata runs under `set -euo pipefail`, so a non-zero exit aborts the boot
  exactly the way today's `sops decrypt` FATAL does (userdata.go:1221). Same
  semantics, same place, more assertions.
- **Resume** — `km-secrets-check.service`, `Type=oneshot`. Userdata does **not**
  re-run on stop/start (cloud-init is per-instance), and a resumed box can meet a
  rotated bundle or a revoked grant. There is no boot to abort here, so it fails
  red and emits the audit event.

That asymmetry is inherent to systemd, not a compromise: a failed unit does not
fail cloud-init, and on resume the box is already running.

### 7.1 Assertions

| # | Assertion | Catches | Severity |
|---|---|---|---|
| 1 | Ciphertext present, `0400 root`, carries a `sops:` metadata block | Bad or missing bundle upload | fatal |
| 2 | Socket exists, `0660 root:sandbox` | Daemon failed to bind | fatal |
| 3 | **Live end-to-end unseal**, then zero. Logs key **names** only | KMS 403, wrong alias, missing grant — the class that aborts boot today | fatal |
| 4 | Every generated shim resolves to a real binary that is not itself | Stale path after a `claude` reinstall; recursion | fatal |
| 5 | `runuser -u sandbox -- bash -lc 'command -v claude'` resolves to `/opt/km/shims/claude` | **The nvm PATH race** | fatal |
| 6 | Fence only: sandbox-uid IMDS **fails**; `aws sts get-caller-identity` as sandbox **succeeds**; narrowed creds **fail** to decrypt the bundle | A fence that isn't fencing, or one that broke the helpers | fatal |
| 7 | A `grants` key naming a binary that does not exist | Consumer installed later by `initCommandsAppend` | warn |

**Assertion 5 is the highest-value line in the design.** It is the only mechanical
proof that interception is actually live, and it is the one failure that is
completely silent in every other layer — the box boots, the daemon runs, the shim
exists, and `claude` simply runs without a key and dies on a 401.

**Assertion 6's third clause is a negative control** — proving the fence by proving
the decrypt *fails*. This is the technique that verified the ttl-handler IAM fix
rather than assuming a policy edit had taken effect.

### 7.2 Where results go

Three places, deliberately: the unit's own status (`systemctl status
km-secrets-check` is the local answer); a `secret_selftest` event in the
CloudWatch audit stream (off-box evidence, because the box is the wrong place to
look for why a box misbehaved); and `/opt/km/.km-secrets-check.json` for the
`klanker:sandbox` self-census skill, mirroring the Phase 113 `.km-profile.yaml`
precedent.

---

## 8. Testing

- **`pkg/secrets/wiring_guard_test.go`** — the Phase 131 pattern. Asserts every
  `dispatch_as_sandbox` definition carries the shim PATH prepend when
  `SopsBundlePresent`. Name-agnostic, so a sixth poller added later is covered
  without editing the test. This is the single most important test in the phase:
  it mechanises the pairing that Phase 131 had to learn twice.
- **A test that fails if `km-env` grows an `export` or `eval` verb.** Mirrors the
  `km-netpolicy` no-removal-verb test.
- **Golden assertions** — sops-present userdata contains no
  `/etc/sandbox-secrets.env` and no `zz-sandbox-secrets.sh`; sops-absent userdata
  is byte-identical to today.
- **Broker tests are Linux-only** (unix sockets, `SO_PEERCRED`, uid checks). `go
  test ./...` on macOS will not cover them, exactly as `pkg/ebpf/audit` does not —
  which is part of how the endianness bug survived in Phase 132. Use the
  cross-compiled-binary-under-Docker technique.
- **Live UAT is the only real proof.** Dump `env` and `/proc/*/environ` inside a
  real agent turn and confirm absence; confirm `claude` still authenticates;
  confirm the shim wins the PATH race on a real nvm box; confirm the fence breaks
  no helper; confirm a resumed box re-runs the check.

---

## 9. Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`.

**Not `--sidecars`** — the userdata changes ride in the create-handler zip, which
`--sidecars` does not rebuild, and fence mode needs a new immutable
`infra/modules/ec2spot/v1.7.0` (never an edit to `v1.6.0`) plus the pin bump in
`locals.substrate_module_versions`.

**Do not split the deploy.** `km-secretsd`, `km-env` and `km-creds` are uploaded by
`buildAndUploadSidecars` while the systemd unit, the shims and the PATH prepends
are rendered by the create-handler. Ship one half and every SOPS sandbox boots a
broker-less box whose boot check fails — the identical lockstep Phase 131
documented for `km ebpf-attach`'s required flags.

New sandbox-side binaries need `sidecarBuilds()` entries plus userdata `s3 cp`
lines, per the `/opt/km/bin` convention.

**Byte-identity:** every profile without `sopsFile` is unchanged. Profiles with one
change substantially, as intended.

Existing sandboxes keep today's behaviour until `km destroy && km create` — the
binaries are fetched at boot.

### 9.1 Natural wave split

The phase divides cleanly along the gate in §3.5, and planning should probably
take it:

1. **Broker, `km-env`, shims, boot check, audit.** Default-on, no IAM change, no
   new module version. This is the whole win against the observed attack.
2. **Fence and `km-creds`.** Adds `ec2spot/v1.7.0`, the self-assume trust, the
   iptables rule and assertion 6. Opt-in, and the only part that can break helper
   tooling.

Wave 1 is independently shippable and independently valuable; wave 2 is what
converts a reduction into a boundary.

---

## 10. Deferred, with reasons

### 10.1 Proxy-side credential substitution

km already MITMs `api.anthropic.com` and `api.openai.com` to meter them, so the
proxy is already in the path. The shim could inject a per-turn dummy sentinel and
`km-http-proxy` could swap in the real `Authorization` at egress — after which the
AI keys exist nowhere on the box, in any process, at any time, and a dumped
environ yields a worthless sentinel.

Deferred deliberately to keep this phase reviewable. Note the trade when it is
picked up: the proxy would then authenticate *anyone* on the box — but that use is
metered, budgeted, pid-attributed and killable, whereas an exfiltrated key is
permanent and off-box. It also only covers secrets whose consumer is a host km
intercepts; a database password or a third-party API key cannot work this way.

### 10.2 Anomaly rules and freeze integration

The broker sees every request and could rate-limit, flag requests from an exe that
is not a known shim, or trip the Phase 121 freeze quarantine. Deferred: a false
positive freezes a working sandbox, the freeze path was built for spend runaway
rather than a security stop, and an untuned rule is worse than none. Audit-only
first; add rules once there is real traffic to tune against.

### 10.3 Flipping `fenceIMDS` to default-on

Its own follow-on phase, gated on a live soak proving no helper path was missed.

---

## 11. Open questions for planning

1. Does anything running as uid `sandbox` read IMDS for **metadata** rather than
   credentials — the `klanker:sandbox` self-census skill in particular? Sandbox id
   and region are already in `/etc/profile.d/km-identity.sh` (userdata.go:366), so
   this is likely covered, but it needs a live check before the fence soaks.
2. Confirm self-assume on the instance role behaves as expected in this account's
   SCP environment before committing to it over a parallel role.
