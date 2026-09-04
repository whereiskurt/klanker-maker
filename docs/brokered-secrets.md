# Brokered secret unsealing (Phase 133)

## What changed, and why

Malware landing on a sandbox and dumping environment variables used to collect
every secret in the profile's SOPS bundle. Before this phase, `spec.secrets.sopsFile`
was decrypted once at boot into `/etc/sandbox-secrets.env` (`0440 root:sandbox`),
and `/etc/profile.d/zz-sandbox-secrets.sh` auto-exported every key into every
login shell. Agent turns dispatch through `runuser -u sandbox -- bash -lc` — a
**login** shell — so the whole bundle sat in the agent's environment, in every
`km shell`, and in the environment of anything either one ever forked. A `cat
/proc/*/environ`, or simply reading the file, yielded everything.

Phase 133 replaces that with an on-demand broker:

- **`km-secretsd`** — a root systemd daemon (`km-secretsd.service`) listening on
  `/run/km/secrets.sock` (`0660 root:sandbox`). It holds the decrypt authority.
  Ciphertext (`/etc/sandbox-secrets.enc.yaml`, `0400 root:root`) sits at rest;
  **nothing is ever decrypted to disk.**
- **`km-env`** — the sandbox-side client. `km-env exec [--as NAME] [--only
  K1,K2] -- <cmd> [args...]` asks the broker over the socket, then `execve`s
  the named command with the granted keys added to *its own* environment. The
  parent shell that invoked `km-env` never sees them.
- **Root-owned shims** at `/opt/km/shims/{claude,codex,...}`, first on every
  agent-dispatch `PATH`, so `claude`/`codex` are transparently rerouted through
  `km-env exec --as <name> --`. The agent process gets its secrets; the turn
  shell around it does not.
- **A boot selftest** (`km-secretsd selftest`) that mechanically proves agents
  would actually get their secrets, and aborts the boot if not.

`/etc/sandbox-secrets.env` and `/etc/profile.d/zz-sandbox-secrets.sh` are
**deleted outright**. No login shell — interactive, `runuser -lc`, or otherwise
— carries a secret again.

**The instance role still holds the decrypt authority — that has not changed.**
The sandbox EC2 role permanently grants `kms:Decrypt` on
`alias/{prefix}-sandbox-secrets` and `s3:GetObject` on
`sandboxes/{id}/secrets.enc.yaml`. Any process that can reach IMDS could
re-derive the bundle by calling AWS directly. AWS has no notion of which uid on
an instance is making a call, so the only place a uid-granular fence can exist
is the OS layer. That fence — `spec.secrets.fenceIMDS`, `km-creds`,
`ec2spot/v1.7.0` — **ships and is opt-in**; see § The IMDS fence below. Leave it
off and the authority described here is unfenced, which is what § Security
posture spells out.

## Decrypt-per-request, and its honest limit

`km-secretsd` performs a live `kms:Decrypt` on every unseal — there is no
boot-time decrypt, no cache, no TTL. Immediately after writing the response,
it zeroes the `[]byte` buffers it held the plaintext in. Secret values are
`[]byte`, never `string`, everywhere the broker controls them, because Go
strings are immutable and may be copied by the runtime — a `map[string]string`
could never actually be zeroed, so using one would make the zeroing claim
decorative.

This has a real, disclosed boundary: zeroing covers the decrypted YAML buffer
and the per-key values the broker directly holds. It does **not** cover the
Go strings the YAML decoder allocates internally while parsing the bundle, nor
`json.Encoder`'s own internal `bytes.Buffer` (which ends up holding a base64
copy of every value on the wire while it writes the response). Both are
GC garbage of indeterminate lifetime that the broker's code cannot reach.
Every unseal also lands as a discrete, attributable `kms:Decrypt` call in
CloudTrail — off-box evidence, independent of anything on the box.

Cost accepted: ~10ms per unseal, and secrets are unavailable during a network
partition — a loud failure (`km-env: cannot reach the secrets broker`), not a
silent one.

## `grants`: shape, not containment

```yaml
spec:
  secrets:
    sopsFile: ./secrets/prod.enc.yaml
    grants:                     # optional; absent ⇒ claude+codex get the full bundle
      claude: [ANTHROPIC_API_KEY]
      codex:  [OPENAI_API_KEY]
      deploy: [DB_PASSWORD, DB_HOST]
```

Each key in `grants` is **simultaneously** the binary name intercepted on
`PATH` and the identity presented to the broker as `--as`. One name, two jobs
— adding `deploy: [...]` both narrows what `deploy` may unseal *and* causes a
`/opt/km/shims/deploy` to be generated (if a `deploy` binary is found on the
sandbox user's `PATH` at boot). That PATH side effect is intentional but easy
to miss on first reading.

Absent `grants` entirely, the default is `claude` and `codex` each granted the
full bundle — the identical effective grant sandboxes had before this phase,
just scoped to one process instead of every shell. Narrowing is available,
never mandatory, so no profile needs to change to keep working.

A `grants` key that names a binary not present on the box at boot is a
**warning, not a failure** (`initCommandsAppend` may install it later; because
no shim is generated for it, nothing else is silently broken).

**`grants` is blast-radius hygiene and audit legibility. It is not
containment.** It shapes what the *shim-wrapped agent process* receives. It
does not shape what a determined caller reachable as uid `sandbox` can obtain:
a bare `km-env exec -- sh` still receives the full bundle by default (no
`--as`, so no narrowing applies), and any caller can claim `--as claude` for
itself — `km-env` does not authenticate the identity it presents, it only
states it. Treat `grants` as scoping what the intended consumer sees by
default, never as a security boundary against an attacker already running as
`sandbox`.

## `km-env exec` / `km-env list`, and why there is no export verb

```
km-env exec [--as NAME] [--only K1,K2] -- <cmd> [args...]
km-env list
```

- `km-env exec` unseals, then `execve`s `<cmd>` with the granted keys merged
  into its environment. `km-env` itself is replaced by the exec — it never
  lingers holding secrets, and nothing is written to disk.
- `km-env list` prints key **names only, never values** — useful for checking
  what a given `--as` identity would receive without actually running
  anything.
- `--only K1,K2` **narrows within** what `--as` already permits and can never
  widen it — the effective set is the intersection of the grant and `--only`;
  naming a key outside the identity's grant does not obtain it. Same
  disposition as `km-netpolicy deny`/`pin`: every runtime lever in this
  platform tightens, none loosens.

**There is deliberately no `km-env export` and no `eval $(km-env)` form.**
Either would put the bundle straight back into a shell — the exact thing this
phase removes. This is enforced by a lexical guard test
(`cmd/km-env/main_test.go` `TestNoShellExportVerbExists`), which scans every
non-test `.go` file in `cmd/km-env/` for a small set of banned literal verb
tokens (`"export"`, `"env"`, `"eval"`, `"dump"`, `"shell"`, `"source"`). It is
a grep guard, not static analysis: it cannot catch a verb assembled from a
constant, a concatenation, or matched via a prefix/regex instead of a literal
case — read a pass as "no obvious banned verb", not a proof that none exists.

## Migrating a non-agent secret consumer

The PATH shims only cover the **agent binaries** (`claude`, `codex`, plus
anything named in `grants`). Anything else that used to read the Phase 89
plaintext `/etc/sandbox-secrets.env` — a profile `initCommand` populating a
systemd `EnvironmentFile`, a bring-up script, a sidecar's config builder — is
**not** intercepted and must ask the broker itself. That file is gone, and the
failure is silent by default: `grep '^KEY=' /etc/sandbox-secrets.env || true`
now matches nothing, writes nothing, and returns 0, so the consumer starts
without its credential and fails much later for a reason that names the
consumer rather than km.

The replacement is `km-env exec --only <KEY>`, which unseals into exactly one
child process:

```sh
# Before (Phase 89) — silently a no-op since Phase 133:
grep '^HF_TOKEN=' /etc/sandbox-secrets.env >> /etc/km/vllm.env 2>/dev/null || true

# After (Phase 133):
/opt/km/bin/km-env exec --only HF_TOKEN -- \
  sh -c 'test -n "$HF_TOKEN" && printf "HF_TOKEN=%s\n" "$HF_TOKEN" >> /etc/km/vllm.env'
```

Four things about that shape are deliberate:

- **`--only <KEY>`**, not a bare unseal. The request narrows to the one key the
  consumer needs, so the `secret_unseal` audit line names exactly that key and
  nothing wider. `--only` can never widen a grant; see above.
- **No `--as`.** A non-agent consumer has no shim and no consumer identity.
  Omitting `--as` returns the whole available set (then narrowed by `--only`);
  claiming an agent's identity here would be a lie in the audit trail. If you
  want a consumer to appear under its own name in `grants`, add it there — it
  gets its own shim and its own grant.
- **Loud, not `|| true`.** `test -n "$HF_TOKEN"` makes an absent key a non-zero
  exit. Profile `initCommands` run inside `/tmp/km-init.sh` under `set -e`, so
  the rest of that script stops and the failure lands in
  `/var/log/cloud-init-output.log` next to the `km-env:` error line. A silently
  missing credential is precisely what this migration exists to stop being
  possible. **Note the surrounding userdata swallows the script's exit status**
  (§7.5 wraps it in `|| echo "No init script found in S3 (skipped)"`), so the
  boot itself continues — that misleading line in the log means "the init
  script failed", not "there wasn't one".
- **Ordering.** `km-secretsd` is started in userdata §5.5, well before §7.5
  runs `initCommands`, so the broker is up by the time any of this runs. The
  shims, by contrast, are generated in §7.8 — *after* `initCommands` — which is
  another reason a non-agent consumer must call `km-env` directly rather than
  relying on PATH.

**Optional keys in an abstract fragment.** If the fragment can be inherited by
a profile with no `spec.secrets.sopsFile` at all, there is no broker and no
`km-env` binary, so guard on the binary rather than swallowing the error —
`profiles/base/gpu/serve.yaml` is the worked example:

```sh
if [ -x /opt/km/bin/km-env ]; then
  /opt/km/bin/km-env exec --only ANTHROPIC_API_KEY,OPENAI_API_KEY -- sh -c '...'
fi
```

An absent bundle is then a legitimate no-op, while a broker that *is* installed
and fails is still a hard error.

**Mechanically guarded.** `pkg/secrets`'s `TestPlaintextEnvInjectionIsGone`
scans every non-test `.go` file and every shipped profile under `profiles/`
for `/etc/sandbox-secrets.env` and `zz-sandbox-secrets.sh`, ignoring comment
lines. A new consumer wired to the deleted file fails the suite rather than
failing a boot.

## Reading `secret_unseal` events

Every unseal and every refusal is written to the sandbox's CloudWatch audit
stream (`km logs <id>`):

- `secret_unseal` — `as`, `keys` (names only), `uid`, `pid`, `exe` (resolved
  from `/proc/<pid>/exe`, best-effort — advisory, not authentication; see
  § Security posture).
- `secret_unseal_refused` — same shape plus `reason` (a refusal is the more
  interesting security event, not the less).
- `secret_selftest` — emitted once per boot/resume selftest run, with a
  per-assertion `ok`/`FAIL`/`warn` status and a `failed` count.

No secret **values** are ever logged in any of these events.

**Joining against `km-netpolicy execs`:** `secret_unseal` carries the `pid`
that asked. `km-netpolicy execs` (Phase 132) records every process the
sandbox ran, keyed by pid with exact process lifetimes (so pid reuse cannot
misattribute the join). `km-netpolicy who <host>` already demonstrates the
join pattern between a flow and an exec; the same technique applies here: take
the `pid`/`uid`/timestamp from a `secret_unseal` event, and look it up in
`km-netpolicy execs` (or `km-netpolicy execs save`'s uploaded trace) to see
the full command line and parent process that made the request. This turns
"something unsealed `ANTHROPIC_API_KEY` at 14:32:07" into "process 4821,
`/opt/km/bin/km-env`, exec'd from `/opt/km/shims/claude`, pid 4801, at
14:32:07" — attribution, not authorization; see § Security posture for the
limit of what that attribution actually proves.

## The boot check

`km-secretsd selftest` runs from two places:

- **At boot**, as a plain command inside userdata, immediately after
  `km-secretsd` starts. Userdata runs under `set -euo pipefail`, so a
  non-zero exit **aborts the boot** — the same disposition as the Phase 89
  `sops decrypt` FATAL this replaces, just with more assertions.
- **On resume**, via `km-secrets-check.service` (`Type=oneshot`,
  `After=km-secretsd.service`, `Requires=km-secretsd.service`). Userdata does
  not re-run on stop/start (cloud-init is per-instance), and a resumed box can
  meet a rotated bundle or a revoked grant since it last booted. There is no
  boot left to abort at that point, so the unit simply fails red and the
  `secret_selftest` audit event records why.

Assertions (fatal aborts the boot / fails the resume check; warn does not):

1. Ciphertext present at `/etc/sandbox-secrets.enc.yaml`, mode `0400`. (fatal)
2. The broker's socket exists at `/run/km/secrets.sock` with mode `0660`.
   (fatal) **A missing socket fails this check; it is not skipped.** That is
   the whole point of the assertion: a broker that never started — bad
   `KM_SECRETS_GRANTS` JSON, a bind failure, any crash — leaves no socket, and
   `systemctl enable --now` cannot catch it either (`Type=simple` reports
   success as soon as the fork succeeds). The check waits up to 5s for the
   socket to appear before failing, which should never elapse in practice:
   `km-secretsd.service`'s `ExecStartPost` already polls for the socket and
   sets its mode, and `systemctl start` does not return until that finishes.
3. A live end-to-end unseal succeeds **through the socket** — the selftest
   speaks the same protocol `km-env` speaks, against the running daemon, so
   this proves the whole chain an agent depends on (socket reachable, daemon
   alive, grants parsed, KMS reachable, bundle decryptable) rather than only
   KMS and the ciphertext. The response buffer is zeroed; the detail logs key
   **names** only. (fatal)
4. Every generated shim's target exists and is not the shim itself (catches a
   stale path after a `claude` reinstall, or accidental self-recursion).
   (fatal)
5. `runuser -u sandbox -- bash -lc 'command -v <consumer>'` resolves to the
   shim path, not the real binary — proof the shim actually wins the PATH
   race. (fatal) **This is the highest-value assertion in the whole check.**
   Every other failure mode here is loud; this one is the exception — a lost
   PATH race means the box boots fine, the daemon runs fine, the shim exists,
   and `claude` simply runs with no secrets and dies on a confusing 401 with
   nothing in this check having complained. Assertion 5 is the only thing
   that catches it mechanically.
6. **Fence mode only** (`spec.secrets.fenceIMDS: true`). Three clauses, in
   order: uid-sandbox IMDS **fails**; `aws sts get-caller-identity` as uid
   sandbox **succeeds**; the narrowed credentials **fail** to decrypt the
   bundle. (fatal) The check is absent entirely on a profile that did not opt
   in. **The third clause is a negative control** — the fence is proven by
   proving a decrypt fails, and nothing else in the system can detect a Deny
   that silently does not match. An IAM simulator verdict is deliberately not
   used and must never be substituted: AWS reports an unsatisfiable condition
   identically to a missing statement, so a simulator tells you what a policy
   *says*, not what AWS *enforces*. Each clause fails the check on its own and
   the detail names which one — see the fence troubleshooting below.
7. A `grants` key naming a binary absent from the box is reported, but only
   as a warning — `initCommandsAppend` may install it later.

**Reading the result:**

```
systemctl status km-secrets-check     # local: active (exited) = last check passed
                                       #        failed = the box failed a resume check
km logs <id>                          # remote: secret_selftest event, per-check detail
```

At boot, a failing selftest is visible as the whole `km create` failing with
cloud-init reporting the boot script's non-zero exit — the same shape a bad
SOPS bundle produced before this phase.

## Troubleshooting

**Broker down** (`systemctl status km-secretsd` shows failed/inactive):
`km-env` fails fast with `km-env: cannot reach the secrets broker at
/run/km/secrets.sock` followed by `km-env: is km-secretsd running? try:
systemctl status km-secretsd`. This is the correct failure mode — the agent
gets a clear, actionable error instead of a confusing 401 from whatever
service the missing key would have authenticated to. Fix: `systemctl restart
km-secretsd` (or `km shell --root` and inspect `journalctl -u km-secretsd`
for why it died — a common cause is the ciphertext object missing or a
`kms:Decrypt` permission gap, both of which the selftest should already have
caught at boot).

**Shim lost the PATH race** (selftest assertion 5 fails, or `claude`
authenticates as an unexpected identity): confirm with `command -v claude`
inside a real `km shell` session — it must resolve to `/opt/km/shims/claude`.
If it resolves somewhere under `~/.nvm/versions/node/...` instead, something
is prepending ahead of `/opt/km/shims` on `PATH` (nvm's own `profile.d`
script prepends too, and `zz-km-shims.sh` is named to sort after it and win —
if a third script sorts after `zz-`, it can still win the race). Every
`dispatch_as_sandbox` call site explicitly sets `PATH=/opt/km/shims:$PATH`
ahead of invoking the turn's command specifically so this does not depend on
`/etc/profile.d` ordering; if a new dispatch site is ever added without that
prepend, `pkg/secrets/wiring_guard_test.go` is the mechanical guard against
it going unnoticed.

**Ungranted consumer** (`km-env` fails with `secrets: unknown consumer:
"<name>"`): the caller's `--as <name>` (or the shim's baked `--as`) does not
appear as a key in the profile's `spec.secrets.grants` map, and `grants` is
non-empty (so the "absent grants ⇒ everyone gets everything" default does not
apply). Add the consumer to `grants`, or leave `grants` unset entirely if
narrowing was not actually intended.

## Security posture — read this before relying on any of it

**The broker is a logged door, not a wall.** Uid `sandbox` must be able to
reach the socket for any of this to work at all — that is the whole feature.
A consequence is that any process running as `sandbox` can speak the socket
protocol directly, without going through `km-env` or a shim, and ask for
whatever it wants. Peer credentials (`SO_PEERCRED`) give the broker the
caller's uid and pid, and it resolves `/proc/<pid>/exe` for the audit record
— but `km-env` is the executable in *every* legitimate call **and** every
illegitimate one, so that check distinguishes nothing an attacker cannot
fake. It is recorded for **attribution**, not **authorization**.

What actually changes is the **character** of a theft, not whether one is
possible. Before this phase it was a passive scoop of a file or an environ
dump that left no trace anywhere. After this phase it is an active,
authenticated (in the sense of "identifies a uid and pid"), pid-attributed
request that writes a `secret_unseal` event to the sandbox's CloudWatch audit
stream and a discrete `kms:Decrypt` call to CloudTrail — joinable against
`km-netpolicy execs` to name the exact process that asked (see above). That
is real detection and forensics value. It is not prevention.

**`grants` is blast-radius hygiene and audit legibility, not containment** (see
above) — a bare `km-env exec -- sh` still gets the full bundle by default, and
any caller can claim `--as claude` for itself.

**On `privileged: true`, none of this holds.** A privileged agent has sudo,
and can stop `km-secretsd`, rewrite the shims, or read the ciphertext object
and call `kms:Decrypt` itself with the instance role's own permanent
authority — bypassing the broker entirely. The real control is
`privileged: false`. **`fenceIMDS` does not change this** — sudo can flush the
iptables rule as easily as it can stop the daemon, so the fence is worth
exactly as much on a privileged box as everything else here: nothing.

**The agent process still holds its keys in its own environ for the turn.**
This has not gone away — a process running as `sandbox` can still read a
same-uid process's `/proc/<pid>/environ`. What this phase buys is moving that
exposure from *every shell, forever* (the Phase 89 shape) to *one process,
one turn* (the shim-wrapped agent's own lifetime) — a large reduction in the
window, and not elimination of it. Closing that gap further (proxy-side
credential substitution, so the AI keys never exist on the box in any process
at any time) is deferred future work, not part of this phase.

## Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`.

**Not `--sidecars`** — the userdata changes (the `km-secretsd`/`km-secrets-check`
systemd units, the shim generator, the five `dispatch_as_sandbox` `PATH`
prepends) ride in the create-handler zip, which `--sidecars` does not rebuild.

**Do not split the deploy.** `km-secretsd` and `km-env` are uploaded to
`s3://<bucket>/sidecars/` by `buildAndUploadSidecars` (via `sidecarBuilds()`),
while the systemd units, the shim files, and the `PATH` prepends are rendered
by the create-handler. Shipping only one half means every SOPS sandbox boots a
broker-less box whose boot check fails (if the units render but the binaries
are missing, the `s3 cp` 404s and the boot aborts under `set -e`; if the
binaries upload but the units/shims don't render, nothing intercepts `claude`/
`codex` at all and secrets are simply absent again). This is the identical
lockstep Phase 131 documented for `km ebpf-attach`'s required flags.

**Byte-identity:** a profile without `spec.secrets.sopsFile` renders
byte-identical userdata to before this phase — nothing in §4 gates on
anything else. A profile *with* `sopsFile` changes substantially, as intended
(the whole Phase 89 env-injection block is replaced).

Existing sandboxes keep today's (pre-Phase-133) behaviour until
`km destroy && km create` — the broker and client binaries are fetched at
boot, and there is no in-place migration path for a running box.

## The IMDS fence (`spec.secrets.fenceIMDS`)

Wave 1 removed the *dump*. It did not remove the *authority*: the instance role
permanently holds both halves of the decrypt — `kms:Decrypt` on this install's
`alias/{prefix}-sandbox-secrets` and `s3:GetObject` on the ciphertext object — so
anything on the box that can reach IMDS can re-derive the whole bundle regardless
of `grants`. AWS has no notion of *which uid on an instance* is calling it, so a
uid-granular fence can only exist at the OS/network layer. That is this.

```yaml
spec:
  secrets:
    sopsFile: ./secrets/mine.enc.yaml
    fenceIMDS: true          # opt-in; default false
```

Only meaningful alongside `sopsFile` — with no bundle there is no broker to mint
credentials from, so a fence would strand every helper rather than protect
anything. `km validate` warns on that combination.

### What it does, in two halves that are only useful together

**Half one — block the direct path.** `km-imds-fence.service` installs, in the
**filter** table:

```
iptables -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT
```

Not the nat table: the nat-table IMDS rule you will also see is a *different*
rule with the opposite purpose (it `RETURN`s IMDS traffic so IMDSv2 keeps
working), and it only exists under `enforcement: proxy` — a fence written there
would be silently absent under `ebpf` and `both`. `REJECT`, not `DROP`: against
a `DROP` every SDK probe waits out a full connect timeout, which reads as a hang
rather than as a policy.

**A systemd unit, not a bare userdata command, and that is load-bearing.** There
is no iptables persistence anywhere in km, and userdata does not re-run on
stop/start (cloud-init is per-instance) — but `km-secretsd.service` and every
shim *are* enabled units and do come back. A userdata-only rule would leave a
resumed box with a live broker, working agents, and no fence, with nothing saying
so.

**Half two — re-home the helpers.** `km-github`, `km-slack`, `km-h1` and the git
credential helpers all read SSM **as uid `sandbox`**, so a bare fence breaks all
of them. The sandbox user's `~/.aws/config` therefore names a `credential_process`:

```ini
[default]
credential_process = /opt/km/bin/km-creds
region = us-east-1
```

Every AWS SDK picks this up transparently — **no helper binary changed at all** —
and a config-file `credential_process` outranks the IMDS provider in every SDK's
chain, with nothing outranking it in turn (userdata sets no `AWS_ACCESS_KEY_ID`
for this user).

`km-creds` is deliberately the dumbest component in the phase: it asks the broker
and prints the answer verbatim. It never calls STS and *cannot* reach IMDS — it
runs as uid `sandbox`, which is exactly what the fence blocks. All of the policy
lives in `km-secretsd`, which is root and unfenced.

### The two Denies, verbatim

The broker self-assumes the instance role with this inline session policy
(`pkg/secrets.SessionPolicy` — one definition, read by the broker and its tests):

```json
{"Version":"2012-10-17","Statement":[
 {"Sid":"InheritTheInstanceRole","Effect":"Allow","Action":"*","Resource":"*"},
 {"Sid":"DenySecretsBundleKMS","Effect":"Deny","Action":"kms:Decrypt","Resource":"*",
  "Condition":{"StringEquals":{"kms:ResourceAliases":"alias/{prefix}-sandbox-secrets"}}},
 {"Sid":"DenySecretsBundleObject","Effect":"Deny","Action":"s3:GetObject",
  "Resource":"arn:aws:s3:::{artifacts-bucket}/sandboxes/{sandbox-id}/secrets.enc.yaml"}
]}
```

Three things about it are deliberate:

- **The `Allow` is not decoration.** A session policy *intersects* with the
  role's identity policies, so a document carrying only Denies would grant
  nothing at all and every helper would break the instant the fence came up.
- **The KMS Deny is conditioned, never blanket.** An unconditional
  `kms:Decrypt` Deny would also kill the SSM SecureString reads the helpers
  depend on — a *different* key, granted under `kms:ViaService = ssm` — which is
  precisely the breakage the fence exists to avoid.
- **The S3 Deny names one object, not a prefix.**

**Self-assume rather than a parallel role is the load-bearing choice.** The
narrowed credentials are then *definitionally* the instance role minus two
Denies, so the two can never drift. A parallel role would need the instance
role's whole policy set duplicated and kept in sync forever.

The trust side uses the **account-root principal narrowed by an
`aws:PrincipalArn` condition** naming the role, plus a matching identity-based
`sts:AssumeRole` grant. That is not a style choice: IAM resolves a principal ARN
to a unique id when the policy is *saved*, so a role naming itself at CreateRole
time fails with `MalformedPolicyDocument: Invalid principal in policy`. The
root-plus-condition form is exactly as narrow — `aws:PrincipalArn` is a global
condition key AWS populates on every request. Root-principal delegation
authorizes nothing on its own, which is why both halves ship together.

Sessions are capped at one hour (role chaining); `credential_process` refreshes,
so this is invisible. The broker caches a live credential until five minutes
before expiry, so a poller shelling out to `aws` in a loop does not mint one per
call.

### What the fence is NOT

**It is not containment against a determined caller.** Uid `sandbox` can still
invoke `km-creds`, exactly as it can still speak the broker protocol directly.
What the fence removes is the **unmediated** path to the instance role — the
`curl 169.254.169.254` that needed no km component at all and left no km record.
Everything that remains is brokered, audited (`secret_credentials`, with uid,
pid and exe), and narrowed.

This is the same honest boundary `grants` has, and for the same reason: read the
security-posture section above and take it literally. **On `privileged: true`
none of it holds** — sudo can flush the iptables rule, stop the broker, or read
the ciphertext and call KMS with the instance role's own permanent authority. The
real control is `privileged: false`.

### Troubleshooting the fence

The selftest's `fence` check names its failing clause. Read it first:

- **"uid sandbox can still reach IMDS"** — the rule is not installed.
  `systemctl status km-imds-fence`; on `enforcement: ebpf`/`both` the box ships
  no iptables by default and the unit installs it, so check whether that install
  failed. Confirm with
  `iptables -S OUTPUT | grep 169.254.169.254`.
- **"uid sandbox cannot call sts:GetCallerIdentity"** — the fence took the
  helpers with it, which is an outage, not a tightening. Check
  `/home/sandbox/.aws/config` exists and names `credential_process`, that
  `/opt/km/bin/km-creds` is present and executable, and run
  `runuser -u sandbox -- /opt/km/bin/km-creds` directly: it prints a diagnosable
  error naming the broker.
- **"the narrowed credentials CAN still decrypt the secrets bundle"** — the
  negative control failed, so the fence buys nothing. The usual cause is the
  `kms:ResourceAliases` condition not matching the key's real aliases; check
  `aws kms list-aliases` against `alias/{prefix}-sandbox-secrets`.
- **`AccessDenied` on the assume, naming the role** — the sandbox predates
  `ec2spot/v1.7.0`, or `km init` has not applied it. The self-assume trust and
  its matching grant live in that module version; both halves are required.

### Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`.

**Not `--sidecars`** — the IAM change (`ec2spot/v1.7.0`) needs a full terragrunt
apply, and the userdata carrying the fence unit and `~/.aws/config` rides in the
create-handler zip, which `--sidecars` does not rebuild.

**Do not split the deploy.** `km-creds` is uploaded by `buildAndUploadSidecars`
while the `~/.aws/config` that invokes it is rendered by the create-handler: ship
one half and a fenced box either fetches a binary nothing references, or points
`credential_process` at a binary that is not there and loses AWS access for uid
`sandbox` entirely.

Existing sandboxes keep Wave 1 behaviour until `km destroy && km create`.
