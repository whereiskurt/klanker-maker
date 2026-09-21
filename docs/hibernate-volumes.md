# Hibernate-safe additional volumes — `km-volumes`

**Applies to:** any profile with `spec.runtime.hibernation: true` and one or more
additional EBS volumes (`additionalVolume`, `additionalSnapshots`).
**Design:** `docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md`
(§13 has the reproduction).

## What went wrong before

On resume from EC2 hibernation the additional NVMe namespaces can come back
**cross-wired**: the physical volume behind `/dev/nvme1n1` is now the one that was
behind `/dev/nvme2n1` and vice-versa. The kernel re-reads each namespace's *size* but
keeps its cached controller *identity*, queues and page cache from before the sleep. So
`/sys/block/*/device/serial` still says the old volume, `blkid` still answers from the
old cache, and an `/etc/fstab` mount by UUID succeeds — against the wrong disk. Dirty
pages flush to the wrong volume; both superblocks end up on the other disk;
`EUCLEAN`; after a reboot both mounts fail and, because of `nofail`, the box reports
healthy with an empty `/repos`.

Reproduced on the first `km pause` / `km resume` cycle of a two-volume box
(`sb-3cf8e982`, 2026-09-20): a 1 MiB known file on `/repos` failed its checksum 33 s
after resume. Only a **live** NVMe Identify (`nvme id-ctrl`) showed the truth; sysfs did
not.

## What km-volumes does

Three layers, in order of preference:

1. **Avoid.** `pre:hibernate` unmounts every additional volume (bounded, never
   blocking) so no filesystem state survives into the resumed image.
   `post:hibernate` starts `km-volumes-resume.service`, which waits for the hypervisor
   to finish restoring the EBS functions, PCI-removes and rescans **every non-root NVMe
   controller** (the same from-scratch probe a reboot gives), re-binds any controller a
   too-early probe left without a driver, then mounts. It runs as a unit, not inline in
   the hook, because that settling can take longer than any sleep hook may hold.
2. **Detect.** Every mount — cold boot and resume — goes through `km-volumes mount`:
   each volume is found by its **live Identify serial** (never the BDM letter, never
   the UUID), then size and filesystem UUID are checked against the **manifest** the
   box wrote on its first cold boot. Any mismatch refuses that volume.
3. **Recover.** A refusal that survives a second re-probe applies the profile's policy
   (`refuse` or `reboot`), and an operator can `repair`.

There are no fstab lines for additional volumes any more.

### Files on the box

| Path | What |
|---|---|
| `/var/lib/km/volumes.json` | manifest: per volume `mountpoint`, `volumeId` (live serial at first boot), `sizeSectors`, `fsUUID`, `fsType`, `fromSnapshot`, `bdm` (informational only). Written once. |
| `/var/lib/km/volumes.state` | outcome of the last `mount`: per volume `mounted` / `refused` / `unvalidated` / `absent` / `ambiguous` / `lazy` / `rebooting` + reason. `km status` reads this. |
| `<mountpoint>/.km-mount-refused` | written into the *underlying* directory on refuse, removed before a successful mount — an empty `/repos` is never mistakable for a healthy empty volume. |
| `/var/log/km-volumes.log` | the sleep hook's output. |
| `/etc/systemd/system/km-volumes.service` | oneshot, every boot, before sshd and the pollers. |
| `/etc/systemd/system/km-volumes-resume.service` | oneshot started by the hook after resume: settle → re-probe → re-bind → mount, `TimeoutStartSec=240`. |
| `/usr/lib/systemd/system-sleep/km-volumes` | the hook; `pre` unmounts under `timeout`, `post` only starts the unit; always exit 0. |

### What `km status` prints

```
Volumes:
  ✓ /data      vol-04d0b727020553414
  ✗ /repos     REFUSED: size: live 30 GiB, kernel 30 GiB, expected 10 GiB
               fix: reboot the box to re-enumerate, or km shell --root <id> then km-volumes repair /repos; see docs/hibernate-volumes.md
  ? /models    unvalidated (no manifest — created before km-volumes; recreate to protect)
```

`km resume` prints the same lines as soon as the box answers (it polls for up to 90 s;
best-effort). `km doctor` has an `Additional volumes` check that WARNs per running
sandbox whose latest km-volumes event for a mountpoint is a refusal. The on-box
`klanker:sandbox` census reads the state and the marker and is told not to diagnose a
refused mount as "the wrong volume got attached".

### `spec.runtime.onVolumeMismatch`

```yaml
spec:
  runtime:
    hibernation: true
    additionalSnapshots:
      - snapshotId: snap-…
        mountPoint: /repos
    onVolumeMismatch: reboot   # default: refuse
```

- `refuse` (default) — the box stays up; the mountpoint is empty with the marker; the
  hibernated session (panes, shells) survives. Right for interactive/desktop boxes.
- `reboot` — after a resume, if a volume still fails validation after the re-probes, the
  box reboots **once** and comes back through the cold-boot mount path. A second
  refusal on the same resume does not reboot again (no loop). Right for headless boxes
  (`alwayson.*`, github, h1) whose turns are worthless without `/repos`.

`km validate` WARNs if the field is set on a profile with no additional volume.

## Recovering a volume that was already cross-written

A box hit before this shipped has two damaged filesystems; validation will refuse them
forever, correctly. Two recoveries, both operator-invoked, never automatic.

### `km-volumes repair <mountpoint>` (on the box, as root)

```
km shell --root <id>
km-volumes repair /repos
```

For a **snapshot-derived** volume it tries `e2fsck` against each backup superblock
(`mke2fs -n` lists them) until one validates, then re-runs `mount`. The observed box's
backup superblocks past the 30 GB overlap were intact, so this is the in-place path.
For a volume that is **not** snapshot-derived (`additionalVolume` — the only copy) it
refuses unless you pass `--i-accept-data-loss`. It never touches a volume that currently
validates, and it never runs `mkfs`.

### Re-materialise from snapshot (control plane)

The clean answer when `repair` finds no usable backup, and the design intent for `/repos`
— a snapshot-derived volume is disposable. Proven live on `sb-3cf8e982` (2026-09-21); every
step below is what actually worked, including three traps.

```bash
ID=<sandbox-id>; D=infra/live/<region-label>/sandboxes/$ID      # e.g. use1

# 1. A REMOTE-created sandbox has no unit on your laptop — hydrate it. The full
#    rendered unit lives in the artifacts bucket. (A --local create already has it.)
mkdir -p $D && cp infra/templates/sandbox/terragrunt.hcl $D/
for f in service.hcl user-data.sh budget-enforcer.hcl; do
  aws s3 cp s3://<artifacts-bucket>/remote-create/$ID/$f $D/$f
done

# 2. The instance must be RUNNING. A stopped instance reports
#    associate_public_ip_address=false to the API and the plan will want to REPLACE
#    THE INSTANCE. km-volumes has already left the refused volume unmounted, so the
#    detach is safe on a running box.
km resume $ID

# 3. Taint and PLAN FIRST. Expect exactly: the snapshot volume + its attachment
#    replaced, plus an in-place volume_tags change on the instance (provider quirk).
#    Anything else — stop and look.
eval "$(km env)"; cd $D
terragrunt state list | grep aws_ebs_volume.snapshot           # e.g. aws_ebs_volume.snapshot["0"]
terragrunt run -- taint 'aws_ebs_volume.snapshot["0"]'         # terragrunt ≥0.99: run --, not bare taint
terragrunt plan
terragrunt apply                                                # ~1 min: new volume from the snapshot, re-attached

# 4. On the box: the manifest still names the OLD volume id, so mount reports the
#    entry "absent". Drop that entry and re-record it as a first boot would; a
#    snapshot of a blank volume needs the mkfs the first boot would have done.
km shell --root $ID
  python3 - <<'PY'
import json; p="/var/lib/km/volumes.json"; m=json.load(open(p))
m["volumes"]=[v for v in m["volumes"] if v["mountpoint"]!="/repos"]; json.dump(m,open(p,"w"),indent=2)
PY
  NEW=$(for n in /dev/nvme*n1; do d=$(basename $n); [ "$(cat /sys/block/$d/device/serial)" = "vol<new-id-without-dash>" ] && echo $n; done)
  blkid "$NEW" >/dev/null 2>&1 || mkfs.ext4 -F "$NEW"          # only if the snapshot was blank
  km-volumes manifest --mountpoint /repos --bdm g --device "$NEW" --label "snapshot <snap-id>" --from-snapshot <snap-id>
  systemctl restart km-volumes.service && km-volumes status
```

A box created **before** km-volumes still carries `/etc/fstab` lines for its additional
volumes; they will happily remount the wrong volume by UUID at the next boot (that is
the bug). Delete them (`sed -i '/\/repos/d' /etc/fstab`) as part of the same repair.

## `/shared` (EFS) across hibernate — a separate problem

`/shared` is NFS over a local `efs-utils` stunnel with `hard` mounts. Across hibernate
the stunnel's TCP state is captured in the RAM image and dead on resume, while the NFS
client believes the connection is live; anything touching `/shared` afterwards can park
in uninterruptible `D` state. km-volumes deliberately does **not** touch it — different
failure (hang, not corruption), different remedy. Tracked separately.

## Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`. The `km-volumes`
sidecar rides `buildAndUploadSidecars`; the unit, hook, manifest call and profile field
ride the create-handler zip. **Do not split**: new userdata with no sidecar makes the
first-boot `manifest` call fail (the box mounts unvalidated and says so); a new sidecar
with old userdata is inert.

**Existing sandboxes get nothing until `km destroy && km create`** — the hook and unit
are userdata. Until every hibernating, volume-bearing box is recreated:

- set `spec.runtime.hibernation: false` on those profiles (an idle-stop then cold-boots,
  which was never affected — you lose hibernated panes, not data), **or**
- raise `idleTimeout` so the box does not hibernate unattended (empirically why a
  sibling box with `idleTimeout: 87600h` was never hit), and avoid `km pause`.

A box that was created before this shipped shows `? unvalidated` in `km status`: it
mounts the old way and is not protected.

## Honest limits

- The root volume's controller is never re-probed (it is the resume device). Root
  cross-wiring has not been observed and is not addressed.
- `pre-sleep` can only unmount what will let go. A holder that survives `SIGKILL`
  leaves a lazily-detached filesystem, recorded as `lazy`; the post-resume re-probe
  pulls the device from under it so its open fds get `EIO` rather than a wrong disk.
- Validation trusts the manifest the box wrote on its first cold boot. That boot is
  the one moment the BDM letter → device binding has never been observed wrong.
