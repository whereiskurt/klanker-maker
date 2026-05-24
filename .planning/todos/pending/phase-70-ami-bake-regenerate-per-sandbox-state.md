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
