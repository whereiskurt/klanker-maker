# Hibernate-safe additional volumes: `km-volumes` — design

**Date:** 2026-09-20
**Status:** approved design, pre-implementation
**Incident:** *Hibernate/resume re-enumerates EBS NVMe devices and cross-writes additional
volumes* (operator write-up + addendum, 2026-09-20; sandbox `always-879f5ae6`, install
`sec`). Reproduced on a second install with a snapshot-backed `/repos`.
**Scope:** `cmd/km-volumes` (new sidecar), `pkg/compiler/userdata.go` (additional-volume
block, one systemd unit, one sleep hook), `pkg/profile` (one field), `km status` /
`km resume` / `km doctor` / `klanker:sandbox` surfaces, docs.

## 1. Problem

A sandbox with `spec.runtime.hibernation: true` and one or more additional EBS volumes
(`additionalVolume`, `additionalSnapshots`) can come back from hibernation with its
additional NVMe namespaces cross-wired: the kernel re-reads each namespace's size but
keeps its cached controller identity, queues and page cache from before the sleep.
`/etc/fstab` mounts by filesystem UUID, so the mount succeeds against the wrong
backing store, and dirty pages are flushed to the wrong volume. Observed: a 160 GB
`/repos` mounted rw on a node the kernel believed was 30 GB; both volumes' primary
superblocks ended up on the other disk; `EUCLEAN`; after a clean reboot both mounts fail
and — because of `nofail` — the box reports healthy with an empty `/repos`.

Facts the design rests on (all from the write-up and addendum, one of them verified on
the code here):

- **Serials were right, sizes were wrong.** `/sys/block/*/device/serial` is stale cache
  after resume; only a live NVMe Identify reflects the current binding. Any check that
  reads sysfs serial would have passed on the failing box. (§3 of the write-up.)
- **A clean reboot restores correct enumeration** — a from-scratch probe of the NVMe
  devices is the known-good state.
- **The systemd sleep hook fires on an EC2-initiated hibernate**: ACPI sleep button →
  `/etc/acpi/actions/sleep.sh` → `systemctl hibernate` → `system-sleep/*` with
  `pre|post hibernate`. (Addendum §A.)
- **The hook must never block or exit non-zero**: `sleep.sh` wraps the hibernate in a
  3-attempt retry with `swapoff`, a 30 s debounce and a 300 s resume-race window, and a
  stalled `pre` hook pushes the box into a state where it stops hibernating until a state
  file is hand-deleted. (Addendum §C.)
- **`/etc/fstab` is mounted on every boot, before any sleep hook exists**, and
  `km stop`/`km resume` (non-hibernating) is a cold boot that never passes a sleep hook.
  A resume-only check leaves the boot path unvalidated. (Addendum §E.)
- **`KM_EBSNVME` is already per-OS** (`command -v ebsnvme-id` on AL2023, the vendored
  python on Ubuntu — `userdata.go:241-252`). Addendum §F is not a defect; recorded here so
  nobody chases it.
- **An empty mountpoint invites a confident wrong diagnosis** — the on-box agent
  recommended restoring 160 GB from snapshot for what was a refused mount. (Addendum §G.)

Decided with the operator, 2026-09-20: the goal is *the box definitely comes back with
`/repos` correct*, on every profile, in every account, with headless boxes preferring an
automatic recovery over preserved session state.

## 2. Design in one paragraph

Take the additional volumes out of `/etc/fstab`. A new sidecar, `km-volumes`, owns them:
on the first cold boot it writes a **manifest** of each volume's physical identity
(`vol-id` from a live Identify, size, filesystem UUID, mountpoint, snapshot provenance);
on **every** boot a oneshot unit runs `km-volumes mount`, which resolves devices **by
live vol-id, never by BDM letter or UUID**, validates size and UUID against the manifest,
and mounts only on a full match; on `pre:hibernate` it unmounts the volumes (bounded,
never blocking) so nothing about them survives into the resumed image; on
`post:hibernate` it **re-probes every non-root NVMe controller unconditionally** (PCI
remove + rescan — the same from-scratch probe a reboot gives) and re-runs `mount`. A
persistent mismatch applies a per-profile policy: `refuse` (default — marker, state file,
loud in `km status`) or `reboot` (headless boxes). Operator-only `repair` verbs and a
runbook cover volumes that were already cross-written before this shipped.

Three layers, in order of preference: **avoid** (unmount + re-probe), **detect**
(validated mount), **recover** (policy, repair). Each is independently useful; together
the mismatch case becomes rare, caught when it happens, and never silent.

## 3. Components

| Piece | Where | Responsibility |
|---|---|---|
| `km-volumes` | `cmd/km-volumes`, cross-compiled linux/amd64, `sidecarBuilds()` → `s3://…/sidecars/km-volumes` → `/opt/km/bin/km-volumes` at boot | every verb in §5 |
| `km-volumes.service` | userdata → `/etc/systemd/system/`; `Type=oneshot`, `RemainAfterExit=yes`, `After=local-fs.target cloud-final.service`, `Before=km-presence.service km-*-inbound-poller.service sshd.service` (the units whose users need the mounts), `WantedBy=multi-user.target` | runs `km-volumes mount` on every boot; **replaces the fstab lines** |
| `/usr/lib/systemd/system-sleep/km-volumes` | userdata, POSIX sh | `pre hibernate` → `timeout 15 /opt/km/bin/km-volumes pre-sleep`; `post hibernate` → `timeout 45 /opt/km/bin/km-volumes post-sleep`; every branch ends `exit 0` |
| `/var/lib/km/volumes.json` | written once by the boot block (§6) | the manifest |
| `/var/lib/km/volumes.state` | rewritten by every `mount` | per-volume outcome, read by `km status`, `km resume`, `km doctor`, the agent |
| `<mountpoint>/.km-mount-refused` | written to the underlying directory on refuse, removed before a successful mount | the §G marker |
| `/var/lib/km/volumes.reboots` | written by the `reboot` policy | one-reboot-per-resume guard |

The sidecar has no network dependency and needs no IAM: everything it reads is an NVMe
admin ioctl, sysfs, `blkid`, and its own files. That is deliberate — the box is least
healthy at exactly the moment this runs.

## 4. The manifest

Written on the **first cold boot only**, by the userdata additional-volume block, at the
point it already does `mkfs`/`blkid` — the one moment the BDM letter → device binding
is trusted (cold boot has never been observed wrong; the control-group box and every
fresh create are fine). One entry per `AdditionalVolumeMounts` element:

```json
{"version":1,"volumes":[
 {"mountpoint":"/data","bdm":"f","volumeId":"vol-0fed1b11de755d9aa",
  "sizeSectors":62914560,"fsUUID":"d9082e9d-…","fsType":"ext4",
  "fromSnapshot":"","label":"additional volume"},
 {"mountpoint":"/repos","bdm":"g","volumeId":"vol-0b79fa8db9f48ee7c",
  "sizeSectors":335544320,"fsUUID":"0fea598a-…","fsType":"ext4",
  "fromSnapshot":"snap-0fe5725492872309c","label":"snapshot snap-0fe5…"}
]}
```

- `volumeId` is the serial from a **live Identify Controller** issued by `km-volumes
  manifest`, never sysfs.
- `sizeSectors` is `BLKGETSIZE64 / 512` at first boot.
- `fsUUID`/`fsType` are what `blkid` reports on the mount source (the whole device, or the
  first child partition when the snapshot carried a partition table — today's rule,
  kept).
- `fromSnapshot` is addendum §H's "rebuildable vs only copy" bit; `repair` (§7) keys on
  it.
- `bdm` is **informational only** from this point on. It is never used to find a device
  again.
- Mode `0644 root:root`. Nothing in it is secret and the on-box agent should read it.

A manifest write failure is logged as a boot WARNING and the box behaves as a
pre-manifest box (§5.1 fallback) — never a boot abort.

## 5. `km-volumes` verbs

### 5.1 `mount` — the only path that ever mounts an additional volume

Runs on every boot (unit) and after every resume (hook). For each manifest entry:

1. **Enumerate live.** For every `/dev/nvme*n1` whose controller is not the root
   volume's, issue Identify Controller (serial → vol-id) and Identify Namespace / `BLKGETSIZE64`
   (size). Build `{volumeId → node, sizeSectors}` fresh, every run.
2. **Resolve by vol-id.** The node for this entry is the one whose live serial equals
   `volumeId`. No node → `absent`. Two nodes → `ambiguous` (refuse both).
3. **Size.** Live size == `sizeSectors`. This is the check that fires on the observed
   failure (serial matched, size didn't).
4. **Filesystem identity.** `blkid` UUID on the mount source == `fsUUID`. Checked
   *against* the identity, never used to find it — the inversion of what fstab did.
5. **Superblock fits.** For ext4, block count × block size ≤ device bytes (belt: a
   superblock from a larger volume cross-written onto a smaller one).
6. Remove `.km-mount-refused`, `mount -t <fsType> -o defaults <src> <mountpoint>`,
   `chown sandbox:sandbox` — same options as today; only the checks changed.

First failing step refuses **that entry only**; the others proceed. On refuse: write the
marker into the underlying directory, record `refused` + the step + expected/actual in
`volumes.state`, emit a `volume_mount_refused` audit event over `/run/km/audit-pipe`
(the `init_failed` shape), and apply the policy (§5.5).

**Pre-manifest fallback.** No `volumes.json` (a box created before this shipped, or a
failed write) → for each `AdditionalVolumeMounts` entry, behave exactly as today
(resolve by BDM letter, mount by resolved device) and record `unvalidated: no manifest`
in the state so `km status` says why the box is not protected. Old boxes keep working;
they are not silently blessed.

### 5.2 `manifest` — first cold boot only

Called from the userdata block once per entry after `mkfs`/`blkid`, with the mountpoint,
BDM letter, resolved device, label and snapshot id. Issues the live Identify for
`volumeId`, reads size and `blkid`, appends the entry. Refuses to overwrite an existing
entry for the same mountpoint (idempotent across a re-run boot script).

### 5.3 `pre-sleep`

Bounded to the hook's `timeout 15`. For each mounted manifest entry, in order:

```
sync                                 # global; cheap
timeout 3 umount <mp>                # clean
 └ busy → fuser -k -TERM -m <mp>; sleep 1; timeout 3 umount <mp>
    └ busy → fuser -k -KILL -m <mp>; sleep 1; timeout 3 umount <mp>
       └ busy → umount -l <mp>; state: lazy=true    # last resort, marked
```

The box is idle (idle-stop) or being paused (`km pause`); the usual holder is a shell
cwd'd in `/repos`, and killing it is the price of not cross-writing the volume. `umount
-l` is never the first choice: a lazily-detached filesystem stays alive for its open
fds and is exactly the stale-state-on-a-swapped-node case; when it is the last resort,
`post-sleep`'s re-probe pulls the PCI device from under it so those fds get `EIO`
rather than a wrong disk. Every step is under its own `timeout`; the verb writes its
outcome to the state and **always exits 0** (addendum §C).

`/shared` (EFS via a local stunnel, `hard` NFS) is **not** handled here — different
failure (a `D`-state hang, not corruption), different remedy (`umount -f -l` or stopping
the stunnel). Separate work item; recorded in §11.

### 5.4 `post-sleep`

**Revised after live UAT (§13.5): runs in its own unit, not the hook.** At resume time
the hypervisor is still restoring the EBS PCI functions; a re-probe issued from inside the
post-hibernate hook found one controller unresponsive — the nvme driver's probe failed
silently and the device sat present-but-unbound, which a plain bus rescan never revisits.
So the hook only does `systemctl start --no-block km-volumes-resume.service`, and that
oneshot (`TimeoutStartSec=240`) does the work in three bounded phases:

1. **Settle** — poll live Identify until every manifest vol-id is visible and the node
   count is stable for two polls (≤ 60 s; proceed anyway if it never settles).
2. **Re-probe + bind** — unconditionally, for every NVMe controller whose namespace is
   **not** the root volume:

```
echo 1 > /sys/bus/pci/devices/<bdf>/remove
echo 1 > /sys/bus/pci/rescan
wait (≤10 s) for the node to reappear
```

   then re-list, and while any manifest vol-id still has no node, remove+rescan every EBS
   controller present on the bus with no driver bound (`vendor 1d0f`, `device 8061`, no
   `driver` symlink), with backoff, ≤ 60 s.
3. **Mount** (§5.1) with the existing one-retry, then policy.

The re-probe is not a reaction to a detected mismatch — it is the normal path, because
the from-scratch probe is what the write-up showed to be correct and detection of
staleness is the part we trust least. The root volume's controller is never touched: it
is the resume device and is mounted. `mount` itself (every run) performs one heal pass
when a manifest device is absent — re-probe unbound controllers, else a bus rescan —
so a cold boot or `systemctl restart km-volumes` recovers the same way.

### 5.5 Policy on persistent mismatch — `spec.runtime.onVolumeMismatch`

`refuse | reboot`, default `refuse`. Purely additive profile field, no `apiVersion`
bump, rendered into the unit's environment (`KM_VOLUMES_ON_MISMATCH`).

- **`refuse`** — the box stays up. Mountpoint empty with the marker, state `refused`,
  audit event, `km status`/`km resume` print the fix. Hibernated RAM state (panes,
  shells) survives. Right for interactive/desktop profiles.
- **`reboot`** — `post-sleep` (never the cold-boot `mount`) issues `systemctl reboot`
  after writing state `rebooting`. The box comes back through the cold-boot path. Guard:
  `volumes.reboots` records the resume it rebooted for; a second refusal on the same
  boot-after-reboot does **not** reboot again — it falls back to `refuse` — so a volume
  that is genuinely damaged cannot produce a reboot loop. Right for headless boxes whose
  turns are worthless without `/repos`. No profile shipped in this repo both hibernates
  and carries an additional volume, so none sets it here; the runbook (§10) shows the
  field, and the operator's `alwayson.*` / `github.v1` / `hackerone.v1` profiles are
  where it belongs.

### 5.6 `status --json`

Prints `volumes.state`. Consumed by `km status`, `km resume`, `km doctor` (over SSM)
and by the `klanker:sandbox` skill on the box.

### 5.7 `repair` — operator-invoked, never automatic

For a volume in state `refused`:

- **Snapshot-derived** (`fromSnapshot` set): `e2fsck -f -y -b <backup>` trying each
  backup superblock `mke2fs -n` would list, until one validates; then re-run `mount`. The
  observed box's backup superblocks past the 30 GB overlap were intact; this is the path
  that recovers it in place.
- **Not snapshot-derived** (the only copy): refuses unless `--i-accept-data-loss`, then
  the same procedure. Never `mkfs`.
- Never touches a volume that currently validates.

Full re-materialisation from snapshot (the clean answer when fsck fails, and for
`/repos` the design intent — it is disposable) is a control-plane runbook, not a verb:
`terraform taint 'module….aws_ebs_volume.snapshot["…"]'` on the sandbox's unit and a
re-apply, then `km resume`. Exact commands in the runbook (§10).

## 6. Userdata changes (`pkg/compiler/userdata.go`)

In the additional-volume block (`~255-327`):

- `mkfs`/partition/`blkid` logic unchanged.
- Replace the `UUID=… >> /etc/fstab; mount -a` lines with: `km-volumes manifest --mountpoint
  … --bdm … --device "$MOUNT_SRC" --label … [--from-snapshot …]`, then a direct
  `mount` of the resolved source **for this first boot only** (the unit takes over from
  the next boot). Existing `nofail` semantics move into the unit (oneshot never fails
  the boot).
- New section: install `km-volumes.service`, the sleep shim, `mkdir -p /var/lib/km`,
  `systemctl enable km-volumes.service`. Rendered **only when `AdditionalVolumeMounts`
  is non-empty** — a profile with no additional volume is byte-identical to today
  (`nofail`-free boxes never had these lines). Goldens: `additional_volume_only` changes
  (expected); the learn/h1 goldens with no volumes must not.
- The sidecar fetch (`aws s3 cp …/sidecars/km-volumes /opt/km/bin/`) joins the existing
  sidecar download block, gated the same way. `TestUserdataDownloadsMatchSidecarBuilds`
  pins the pairing.

The boot selftest is **not** extended: a refused mount is a state, not a boot failure.

## 7. Operator surfaces

- **`km status`** — new `Volumes:` section (present only when the row's profile has
  additional volumes), one line per entry from `volumes.state`:
  `✓ /repos  vol-0b79…  160G  snapshot` /
  `✗ /repos  REFUSED (size: live 30G, expected 160G) — reboot the box or km-volumes repair /repos; see docs/hibernate-volumes.md` /
  `? /repos  unvalidated (no manifest — created before km-volumes; recreate to protect)`.
  Fetched over the same SSM probe `Init: FAILED` uses.
- **`km resume`** — after `StartInstances` and the existing GitHub-token refresh, polls
  `km-volumes status --json` over SSM (bounded, ≤90 s, best-effort like the token
  refresh) and prints the `Volumes:` lines, so a refused `/repos` is visible at the
  moment the operator woke the box, not at the next turn's failure.
- **`km doctor`** — `Additional volumes` check: WARN per running sandbox with any
  `refused`/`lazy`/`rebooting` entry; SKIP when no sandbox has additional volumes.
- **`klanker:sandbox` skill** — section B reads `/var/lib/km/volumes.state` and the
  marker; on `refused` it says so verbatim and points at `km-volumes repair`, and never
  proposes restoring from snapshot on its own.
- **Audit** — `volume_mount_refused` (with mountpoint, step, expected, actual, policy)
  and `volume_reboot` events.

## 8. Profile schema

`spec.runtime.onVolumeMismatch: refuse | reboot` (string enum, optional, default
`refuse`). Added to `RuntimeSpec` and the JSON schema (`additionalProperties: false`
means it must be declared or `km validate` rejects it). `km validate` WARNs if set
without any additional volume (dead field). Tested through YAML → `ValidateSchema`, not
by setting the struct (the `spec.otp` lesson).

## 9. Testing

**Unit (`cmd/km-volumes`)** — the decision logic behind small interfaces
(`Identifier` for the ioctls, `Blkid`, `Mounter`, `Rescanner`), with fakes:
- match by serial, ignore BDM; absent; ambiguous.
- each validation step refuses with the right reason; the first failing step wins.
- observed-failure replay: two entries, serials correct, sizes swapped → both refused
  at step 3, neither mounted, markers written, policy consulted.
- pre-manifest fallback mounts by BDM and records `unvalidated`.
- `pre-sleep` escalation order and that every path returns exit 0.
- `post-sleep` re-probes every non-root controller and never the root one; retries a
  serial/size refusal exactly once.
- policy: `refuse` never reboots; `reboot` reboots once per resume and falls back to
  `refuse` on the next refusal.
- `repair` refuses a non-snapshot volume without the flag; never touches a validating
  one.

**Compiler** — rendered-bash tests execute the sleep shim under `sh` with a fake
`km-volumes` that sleeps past the timeout, asserting exit 0 and the bound; golden
updates for the additional-volume case; byte-identity for the no-volume case;
`TestUserdataDownloadsMatchSidecarBuilds` extended.

**Live, and first in the plan, before the hook is written** — the instrumented cycle
from the write-up's §3 on a two-volume box (`km pause` / `km resume`, checksums of the
first 4 KiB of each device and a known file before/after), plus:
1. confirm the shim fires under `km pause` (a marker file from a trivial hook);
2. confirm a PCI remove/rescan of an unmounted additional controller re-identifies it
   (serial and size agree with AWS afterwards) and does not disturb the root volume;
3. only then implement §5.3/§5.4 on top of what was observed.
`hackerone-v1` (`always-11dbf1f5`, in `paus`, never resumed since the bug) is the
untouched pre-fix control for a before/after comparison.

## 10. Docs

`docs/hibernate-volumes.md` (new runbook: the mechanism, the three layers, the state
file and marker, `onVolumeMismatch`, `km-volumes repair`, the `terraform taint`
re-materialisation, the `/shared` caveat, deploy surface); `docs/operational-gotchas.md`
§ pointer; `OPERATOR-GUIDE.md` § additionalSnapshots note; `klanker:sandbox` skill;
`CLAUDE.md` block; plugin bump.

## 11. Out of scope (recorded, not forgotten)

- **`/shared` EFS across hibernate** — separate item (addendum §D).
- **Root-volume cross-wiring** — not observed, not addressed; the root controller is
  never re-probed.
- **Automatic re-materialisation from snapshot** — runbook only; a verb that runs
  Terraform against a sandbox from the box's own timeline is a control-plane feature.
- **The `util.reposync` device-assignment bug** — a second path to "healthy box, no
  `/repos`"; §7's surface catches its symptom, its cause is not touched here.

## 12. Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`. The sidecar rides
`buildAndUploadSidecars`; the unit, hook, manifest call and schema field ride the
create-handler zip. **Do not split**: a new userdata with no sidecar 404s the fetch and
the unit crash-loops on a missing binary; a new sidecar with old userdata is inert.
**Existing sandboxes get nothing until `km destroy && km create`** — the hook and unit
are userdata. Fleet mitigation until every volume-bearing box is recreated:
`spec.runtime.hibernation: false` on those profiles (idle-stop then cold-boots, which
was never affected), or an `idleTimeout` long enough that the box does not hibernate
unattended — empirically why `irbot-v1` has never been hit. Plugin version bump for the
skill change.

## 13. Spike results (2026-09-20, `sb-3cf8e982`, t3.medium AL2023, 30 GB `/data` + 10 GB snapshot `/repos`)

Plan Task 1, run before any hook code was written. All four questions answered on the
first `km pause` / `km resume` cycle.

1. **The hook fires under `km pause`.** `/usr/lib/systemd/system-sleep/km-spike` logged
   `pre hibernate` and `post hibernate` exactly once each.
2. **The cross-wiring reproduced on cycle 1** (33 s after resume, matching the write-up):
   `nvme1n1: detected capacity change from 20971520 to 62914560` and the inverse on
   `nvme2n1`. `/repos/known.bin` (1 MiB, sha256 recorded before) failed its checksum;
   `/data/marker.txt` read back as garbage.
3. **Only a live Identify tells the truth.** After resume, for `nvme1n1`: sysfs serial
   `vol0b914…` (the 10 GB volume — stale, cached), **live `nvme id-ctrl` serial
   `vol04d0b…` (the 30 GB volume — what is actually behind the node now)**, kernel size
   62914560 (re-read), `blkid` UUID = the 10 GB filesystem's (served from the old page
   cache). So the physical volumes really do swap controllers on resume; the kernel
   re-reads namespace capacity but not controller identity, and the block layer keeps the
   old cache. This corrects the write-up's "serials were right": sysfs was, live was not.
   §5.1's rule — match by live serial, never sysfs, never BDM, never UUID — is confirmed.
4. **PCI remove + rescan re-identifies in 0.7 s and does not touch root.** With both
   volumes unmounted, `echo 1 > /sys/bus/pci/devices/<bdf>/remove` for `1e.0`/`1f.0`
   then `/sys/bus/pci/rescan`: both nodes back in 0.70 s, sysfs serial == live serial ==
   correct size, root (`nvme0n1`, xfs) still mounted and writable. Node names AND BDFs
   changed across the re-probe (`nvme1n1` moved from `1f.0` to `1e.0`) — neither is a
   stable key; the serial is. `blkid` after the re-probe shows the truth: the 10 GB volume
   now carries the `/data` filesystem's UUID and vice-versa — the superblocks were
   cross-written during cycle 1. The box is kept (cold-stopped) as the `repair` test
   subject for plan Task 11.

Consequences for the plan: `post-sleep`'s reappearance wait can stay at 10 s (observed
0.7 s); `ListDevices` must be re-run after every re-probe (names move); the `repair`
UAT step has a real cross-written pair to work on.

### 13.6 Live UAT, repair and re-materialisation (`sb-3cf8e982`, 2026-09-21)

- Validation refused both volumes at `fsuuid` (each disk carries the other's superblock).
- **Every backup superblock on BOTH volumes belonged to the other filesystem** — the 10 GB
  volume lies entirely inside the 30 GB filesystem's extent, so the write-up's "backups
  past the overlap survived" does not apply when the smaller volume is the whole overlap.
  `repair` therefore checks each backup's UUID with `dumpe2fs -o superblock=N -h` (never
  writes) and only runs `e2fsck -b` against one carrying the manifest's filesystem; with
  none it runs no fsck and points at the runbook. (Before that check, `e2fsck -b` against
  a foreign backup aborted with "FILE SYSTEM WAS MODIFIED", and one that happened to fit
  rewrote the primary with the wrong filesystem.)
- Re-materialisation proven: `terragrunt run -- taint 'aws_ebs_volume.snapshot["0"]'` +
  apply on a RUNNING instance replaced the volume in ~1 min; a stopped instance plans an
  instance REPLACEMENT (`associate_public_ip_address false -> true`). A remote-created
  sandbox's unit must first be hydrated from `remote-create/<id>/`. After re-recording the
  entry, `mount` validated the new volume by live serial and mounted it; `/data` (only
  copy, backups gone) stays refused. The box's legacy fstab line had meanwhile remounted
  `/repos` from the wrong volume by UUID — the runbook now says to delete those lines.

### 13.5 Live UAT cycle 1 (`sb-302a43c0`, first build) — the re-probe races the hypervisor

`pre-sleep` unmounted both volumes; after resume `/repos` re-probed and mounted, the 1 MiB
known file verified, zero `EXT4-fs error` lines — the corruption is gone. But `/data` came
back **absent**: the post-hook re-probe removed `0000:00:1f.0` and the rescan's nvme probe
never completed (`nvme nvme1: pci function 0000:00:1f.0` with no queues line), because the
hypervisor had not finished restoring that function. The device stayed on the bus with no
driver; a later plain rescan did nothing. Removing that function again + rescan bound it
in 4 s and `km-volumes mount` mounted `/data` with its marker file intact — nothing was
lost, only not mounted. Hence the revised §5.4: settle first, re-probe from a unit with a
real budget, and re-bind unbound controllers; plus the heal pass in `mount`.
