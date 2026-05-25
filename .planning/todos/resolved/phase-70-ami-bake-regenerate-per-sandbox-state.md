---
title: Phase 70 follow-up — AMI bake captures stale per-sandbox state; userdata should regenerate on every boot
area: km-ami / userdata
created: 2026-05-24
origin: Phase 70 SC-4 UAT 2026-05-24 (poller queue mismatch root cause)
---

### Problem
Operator-baked AMIs (`km ami bake <sandbox-id>`) snapshot the entire root volume, INCLUDING per-sandbox files that were written by userdata with the source sandbox's ID. When a new sandbox is created from the baked AMI, cloud-init runs userdata once and renames the hostname, but does NOT regenerate the per-sandbox files. Result: the new sandbox runs with the SOURCE sandbox's identifiers in critical config files.

Discovered during SC-4 UAT — the new `learncodex` sandbox was polling the OLD bakesrc sandbox's SQS queue because 7 systemd unit files still had `Environment=SANDBOX_ID=learn-3cad85fe` (bakesrc's id) instead of the new `learn-009e0e7b`. Slack messages posted to `#sb-learncodex` went into `learn-009e0e7b.fifo` queue but the poller was polling `learn-3cad85fe.fifo` — silence forever.

Files affected (from one UAT diagnostic):
```
/etc/systemd/system/km-audit-log.service
/etc/systemd/system/km-tracing.service
/etc/systemd/system/km-mail-poller.service
/etc/systemd/system/km-slack-inbound-poller.service
/etc/systemd/system/km-presence.service
/etc/systemd/system/km-queue.service
/etc/systemd/system/km-ebpf-enforcer.service
/etc/profile.d/km-audit.sh
/etc/profile.d/km-cgroup.sh
```

Workaround during UAT was `sed -i 's/<old>/<new>/g'` + `systemctl daemon-reload` + `systemctl restart`.

### Fix options (one or more)
1. **Userdata writes all per-sandbox files unconditionally on every boot.** The compiler already emits these as bash heredocs; ensure they OVERWRITE not skip-if-exists. Need to audit each writer.
2. **Cloud-init / userdata reads sandbox-id from IMDS at runtime** (not from the AMI snapshot) and uses that to regenerate per-sandbox configs. More dynamic; loosens AMI coupling.
3. **`km ami bake` runs a cleanup step before snapshotting.** Remove per-sandbox files from `/etc/systemd/system/*.service`, `/etc/profile.d/*`, etc. So the baked AMI is "blank" and userdata re-emits everything fresh on first boot.

Option 1 is probably the most pragmatic since the compiler already knows the file contents.

### Audit
Walk `pkg/compiler/userdata.go` for every heredoc that writes a file referencing `{{.SandboxID}}` or similar. Confirm each one:
- Uses `cat > /path << EOF` (not `cat >> /path` — append). Truncation semantics.
- Is NOT inside a conditional like `if [ ! -f /path ]` (skip-if-exists).

### Verification
Bake an AMI from one sandbox, create a new sandbox from that AMI, then verify on the new sandbox:
- `grep SANDBOX_ID /etc/systemd/system/*.service` shows the NEW sandbox-id (not source's)
- The poller polls the correct SQS queue (`km-slack-inbound-<new-sandbox-id>.fifo`)

### Files
- `pkg/compiler/userdata.go` (audit all heredoc writes)
- `internal/app/cmd/ami_bake.go` (optional: add pre-snapshot cleanup step)
- `pkg/compiler/userdata_ami_test.go` (new: assert per-sandbox files regenerate)

### Resolution (2026-05-24)
Chose **Option 1** (per upstream prompt): audit every per-sandbox heredoc in
`pkg/compiler/userdata.go` and lock the truncating-unconditional property with a
regression test. **No userdata.go code change was needed** — the audit confirmed
all per-sandbox writers already use the safe form.

**Audit results** (all systemd units + per-sandbox shell files):

| File | Writer line | Form | Skip-if-exists? |
|---|---|---|---|
| `/etc/profile.d/km-identity.sh` | 184 | `cat >` (truncate) | no |
| `/etc/profile.d/km-notify-env.sh` | 937 | `cat >` (truncate) | no |
| `/etc/profile.d/km-audit.sh` | 1123 | `cat >` (truncate) | no |
| `/etc/profile.d/km-cgroup.sh` | 3218 | `cat >` (truncate) | no |
| `/etc/km/notify.env` | 953 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-dns-proxy.service` | 1015 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-http-proxy.service` | 1032 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-audit-log.service` | 1079 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-tracing.service` | 1103 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-mail-poller.service` | 2138 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-slack-inbound-poller.service` | 2155 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-presence.service` | 2183 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-queue.service` | 2433 | `cat >` (truncate) | no |
| `/etc/systemd/system/km-ebpf-enforcer.service` | 3109 | `cat >` (truncate) | no |

The few `cat >>` (append) lines that exist all target
`/etc/profile.d/km-audit.sh` (lines 3067, 3081, 3338) or
`/etc/profile.d/km-profile-env.sh` (lines 263, 281, 294) — both files where
shell-source semantics make the last assignment win, so append after a truncate
(or even append-only) does not produce stale-id reads at runtime.

One semi-finding: `/etc/profile.d/km-profile-env.sh` is only truncated when
`{{- if .ProfileEnv }}` is set; otherwise it is append-only. Practically benign
(last-write-wins on shell source, OTEL_RESOURCE_ATTRIBUTES with the new
sandbox_id appended at the end shadows any baked-AMI line) — left as a cosmetic
file-growth concern, not a functional bug. Excluded from the strict regression
list with a comment in the test.

**Regression test:** `pkg/compiler/userdata_ami_test.go` — two cases:
1. `TestUserdata_PerSandboxFilesAreUnconditionalTruncatingWrites` enables every
   per-sandbox feature (slack inbound + enforcement=both), iterates the 14
   files above, asserts each has a `cat > <path> <<` heredoc AND the preceding
   lines do not contain a skip-if-exists guard (`[ ! -f <path> ]`,
   `[ ! -e ... ]`, `test ! -f ...`, `test ! -e ...`).
2. `TestUserdata_PerSandboxUnitsHaveNoAppendOnlyWrites` asserts no
   `cat >> /etc/systemd/system/km-*.service` exists in the rendered userdata —
   systemd units must only ever be truncated, never appended.

Both tests pass. The 6 pre-existing failures in `pkg/compiler/` (notify env,
audit hook, GIT_ASKPASS, etc.) are unrelated to this work and predate this
follow-up — they reproduce on `git stash` of these edits.

**What this doesn't fix:** the original UAT symptom (new sandbox polling
source's SQS queue with the source's SANDBOX_ID in the unit file). Option 1 is
preventive — it guarantees that *if* userdata runs, the per-sandbox files are
overwritten. The audit shows that property already holds. Therefore the UAT
root cause must lie elsewhere — either cloud-init didn't re-run on the
AMI-baked instance, the new sandbox's compile step received the source's
SandboxID, or the file was somehow protected from rewrite. Option 2 (IMDS
lookup at runtime) and Option 3 (AMI bake pre-snapshot cleanup) remain open if
the UAT symptom recurs after the deploy.

**Deploy:** `km init --sidecars` to push the create-handler Lambda's bundled
km binary so newly-created sandboxes use the audited userdata. (Same deploy
step as the other four resolved Phase 70 follow-ups — one `km init --sidecars`
covers all five.)
