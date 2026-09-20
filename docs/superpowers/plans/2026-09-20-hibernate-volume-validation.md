# Hibernate-Safe Additional Volumes (`km-volumes`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A sandbox with `hibernation: true` and additional EBS volumes comes back from every hibernate/resume with each volume mounted on the physical device it belongs to — or refuses loudly — instead of silently cross-writing them.

**Architecture:** New sidecar `cmd/km-volumes` owns the additional volumes end to end: a first-boot manifest of physical identity (live NVMe Identify serial, size, fs UUID), a per-boot validated `mount` (resolve by live serial, never BDM letter or UUID) run by a oneshot unit that replaces the fstab lines, a bounded `pre-sleep` unmount and an unconditional `post-sleep` PCI re-probe via a `system-sleep` shim, a per-profile `refuse|reboot` policy, and operator `repair`. Everything kernel-facing sits behind four small interfaces so the decision logic is unit-tested with fakes; the ioctl and the re-probe are proven in a live spike before the hook is written.

**Tech Stack:** Go 1.25 (`golang.org/x/sys/unix` for ioctls; no cgo — sidecars build `CGO_ENABLED=0`), systemd, bash userdata template, JSON schema, AWS SSM.

**Spec:** `docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md`

## Global Constraints

- Every kernel-touching operation (`umount`, `fuser`, ioctl, PCI remove/rescan, `e2fsck`) runs under a bounded timeout; `pre-sleep` and `post-sleep` **always exit 0** (spec §5.3, addendum §C).
- Devices are matched by **live** Identify Controller serial. `/sys/block/*/device/serial` is never read for validation (spec §1). The BDM letter is used only by `manifest` on first cold boot.
- The root volume's controller is never re-probed (spec §5.4).
- `reboot` policy fires only from `post-sleep`, at most once per resume (spec §5.5).
- No `apiVersion` bump; `spec.runtime.onVolumeMismatch` is additive, default `refuse`.
- Userdata for a profile with **no** additional volumes must stay byte-identical (spec §6).
- Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`; do not split (spec §12).
- Live work uses `AWS_PROFILE=klanker-application AWS_REGION=us-east-1`; SSM scripts go through a scratchpad script (`bash <script>`), base64-wrapped for `AWS-RunShellScript` (dash, not bash). `hackerone-v1` on the other install is a control and must not be resumed by this work.
- Commit with `git commit -m … -- <paths>` and check `git status --short` for staged (`M `) files before every commit in the primary checkout.
- Every commit message ends with the attribution block from the session reminder. Branch: `feat/hibernate-volume-validation`.
- `internal/app/cmd` has 5 known pre-existing failures (`TestBootstrapSCP*`, `TestCluster*`); `pkg/ebpf`/`pkg/ebpf/audit` are linux-tagged and mostly skip on macOS.

---

### Task 1: Live spike — hook fires, re-probe re-identifies (nothing merged from this task)

**Files:**
- Create (scratchpad only): `spike_hook.sh`, `spike_reprobe.sh`
- Record results in: `docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md` § 1 (append a dated "Spike results" subsection)

This task exists because the two things the design leans on hardest — the sleep hook firing under `km pause`, and a PCI remove/rescan re-identifying an EBS controller — cannot be proven from a laptop. Do it before any Go is written for §5.3/§5.4.

- [ ] **Step 1: Create a two-volume hibernating sandbox in this install**

`profiles/learner.yaml` has `additionalVolume` but no hibernation. Write a throwaway profile in the scratchpad (not committed):

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: spike-vol
extends:
  - base/os/redhat
  - base/network/safenetwork
  - base/platform
spec:
  runtime:
    substrate: ec2
    instanceType: t3.medium
    hibernation: true
    additionalVolume: { size: 10, mountPoint: /data }
    additionalSnapshots:
      - snapshotId: <a small snapshot you own, or omit this list and use two additionalVolume sizes via a second profile>
        mountPoint: /repos
  lifecycle: { ttl: "3h", idleTimeout: "3h", teardownPolicy: stop }
```

If no small snapshot is available, `km ami list` / `aws ec2 describe-snapshots --owner-ids self --query 'Snapshots[?VolumeSize<=\`20\`].[SnapshotId,VolumeSize]'` finds one; otherwise create a 10 GB volume, `mkfs.ext4`, snapshot it (`aws ec2 create-snapshot`). Two volumes of **different** sizes matter (spec §1).

Run: `km validate <profile> && km create <profile> spike-vol`; wait for `running` + SSM online (the `wait_ssm.sh` pattern from earlier sessions).

- [ ] **Step 2: Baseline capture script**

`spike_capture.sh` (run over SSM, base64-wrapped):

```sh
for n in /dev/nvme*n1; do
  d=$(basename $n)
  echo "$d sysfs_serial=$(cat /sys/block/$d/device/serial) size=$(cat /sys/block/$d/size) \
       live_sn=$(nvme id-ctrl $n 2>/dev/null | awk '/^sn/ {print $3}') \
       bdf=$(basename $(readlink -f /sys/block/$d/device/device)) \
       head4k=$(dd if=$n bs=4096 count=1 2>/dev/null | sha256sum | cut -c1-12)"
done
findmnt -no SOURCE,TARGET /data /repos
```

(`nvme-cli` may be absent on AL2023 — `dnf install -y nvme-cli` in the script, it is in the base repo; the SG allows the mirror.) Record the output in the spec's "Spike results".

- [ ] **Step 3: Prove the hook fires under `km pause`**

Install a trivial hook over SSM:

```sh
cat > /usr/lib/systemd/system-sleep/km-spike <<'H'
#!/bin/sh
echo "$(date -u +%FT%TZ) $1 $2" >> /var/lib/km/spike-hook.log
exit 0
H
chmod 0755 /usr/lib/systemd/system-sleep/km-spike
```

Then `km pause spike-vol`, wait for `stopped`, `km resume spike-vol`, wait SSM online, `cat /var/lib/km/spike-hook.log`. Expected: one `pre hibernate` line and one `post hibernate` line. Record.

- [ ] **Step 4: Prove the cross-wiring (or its absence) on this box**

Re-run `spike_capture.sh` after the resume. Compare: sysfs serial vs live `nvme id-ctrl` serial vs size vs `head4k`. Record whichever of these disagree. (If nothing disagrees on this box on the first cycle, run two more cycles — the write-up's 33 s window suggests it is timing-dependent; do not proceed past this task claiming the mechanism if it never reproduced, but the re-probe proof in Step 5 stands on its own.)

- [ ] **Step 5: Prove the PCI remove/rescan re-identifies without disturbing root**

Over SSM, with `/data` and `/repos` unmounted first:

```sh
umount /data /repos 2>/dev/null
root_dev=$(findmnt -no SOURCE / | sed 's/p[0-9]*$//; s#/dev/##')
for n in /sys/block/nvme*n1; do
  d=$(basename $n); [ "$d" = "$root_dev" ] && continue
  bdf=$(basename $(readlink -f $n/device/device))
  echo "removing $d ($bdf)"; echo 1 > /sys/bus/pci/devices/$bdf/remove
done
echo 1 > /sys/bus/pci/rescan; sleep 3
```

then `spike_capture.sh` again and `findmnt /` still healthy. Expected: every non-root node reappears, live serial and size agree with `aws ec2 describe-volumes`, root untouched. Record the timing (how long until nodes reappear — this sets `post-sleep`'s wait bound).

- [ ] **Step 6: Record, tear down**

Append "Spike results (YYYY-MM-DD)" to the spec §1 with the four observations. `km destroy spike-vol --remote --yes`. Commit the spec edit:

```bash
git add docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md
git commit -m "docs(spec): km-volumes spike results — hook fires under km pause; PCI re-probe re-identifies" -- docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md
```

If Step 3 or Step 5 fails, **stop and report** — §5.3/§5.4 need a different mechanism and the plan from Task 5 on must change.

---

### Task 2: `pkg/nvme` — Identify Controller/Namespace over ioctl, parsers tested with fixed bytes

**Files:**
- Create: `pkg/nvme/identify.go` (parsers + types), `pkg/nvme/ioctl_linux.go` (`//go:build linux`), `pkg/nvme/ioctl_other.go` (`//go:build !linux`, returns `ErrUnsupported`)
- Test: `pkg/nvme/identify_test.go`

**Interfaces:**
- Produces:
  - `type Controller struct { Serial, Model, BDMName string }` — `Serial` trimmed (`vol0b79…` as the EBS-formatted id, i.e. no dash — provide `VolumeID()` returning `vol-0b79…`)
  - `type Namespace struct { SizeLBAs uint64; LBABytes uint32 }` with `Bytes()`
  - `func ParseIdentifyController(buf []byte) (Controller, error)` — needs 4096 bytes
  - `func ParseIdentifyNamespace(buf []byte) (Namespace, error)` — needs 4096 bytes
  - `func IdentifyController(devPath string) (Controller, error)`; `func IdentifyNamespace(devPath string, nsid uint32) (Namespace, error)` (linux: real ioctl; other: `ErrUnsupported`)

- [ ] **Step 1: Write the failing parser tests**

```go
package nvme

import "testing"

func idCtrlBytes(sn, model, bdm string) []byte {
	b := make([]byte, 4096)
	copy(b[4:24], padRight(sn, 20))
	copy(b[24:64], padRight(model, 40))
	copy(b[3072:3104], padRight(bdm, 32)) // EBS vendor-specific: BDM name
	return b
}

func padRight(s string, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	copy(out, s)
	return out
}

func TestParseIdentifyController(t *testing.T) {
	c, err := ParseIdentifyController(idCtrlBytes("vol0b79fa8db9f48ee7c", "Amazon Elastic Block Store", "/dev/sdg"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Serial != "vol0b79fa8db9f48ee7c" || c.VolumeID() != "vol-0b79fa8db9f48ee7c" {
		t.Errorf("serial=%q volumeID=%q", c.Serial, c.VolumeID())
	}
	if c.BDMName != "/dev/sdg" || c.Model != "Amazon Elastic Block Store" {
		t.Errorf("bdm=%q model=%q", c.BDMName, c.Model)
	}
	if _, err := ParseIdentifyController(make([]byte, 100)); err == nil {
		t.Error("short buffer must error")
	}
}

func TestParseIdentifyNamespace(t *testing.T) {
	b := make([]byte, 4096)
	// nsze (u64 LE) at 0; flbas at 26 selects LBA format 0; lbaf0 at 128: ms(u16) rp... lbads at byte 130
	nsze := uint64(335544320)
	for i := 0; i < 8; i++ {
		b[i] = byte(nsze >> (8 * i))
	}
	b[26] = 0     // flbas: format 0
	b[130] = 9    // lbads = 9 → 512-byte LBAs
	ns, err := ParseIdentifyNamespace(b)
	if err != nil {
		t.Fatal(err)
	}
	if ns.SizeLBAs != 335544320 || ns.LBABytes != 512 || ns.Bytes() != 335544320*512 {
		t.Errorf("got %+v", ns)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/nvme/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the parsers**

`pkg/nvme/identify.go`:

```go
// Package nvme issues NVMe admin Identify commands and parses the parts km
// needs: the controller serial (an EBS volume id), the EBS vendor-specific
// block-device-mapping name, and a namespace's size. It exists because the
// kernel's cached view (/sys/block/*/device/serial) is stale after a
// hibernate/resume while the namespace size is re-read — only a live Identify
// tells the truth about which volume a node is bound to right now.
package nvme

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupported = errors.New("nvme: identify not supported on this platform")

const identifyLen = 4096

type Controller struct {
	Serial  string // e.g. "vol0b79fa8db9f48ee7c" (EBS puts the volume id, minus dash, in SN)
	Model   string
	BDMName string // EBS vendor-specific bytes 3072..3103: "/dev/sdg" or "sdg"
}

// VolumeID renders the serial as an EBS volume id ("vol-…").
func (c Controller) VolumeID() string {
	s := c.Serial
	if strings.HasPrefix(s, "vol") && !strings.HasPrefix(s, "vol-") {
		return "vol-" + s[3:]
	}
	return s
}

type Namespace struct {
	SizeLBAs uint64
	LBABytes uint32
}

func (n Namespace) Bytes() uint64 { return n.SizeLBAs * uint64(n.LBABytes) }

func ParseIdentifyController(buf []byte) (Controller, error) {
	if len(buf) < identifyLen {
		return Controller{}, fmt.Errorf("nvme: identify controller buffer %d bytes, want %d", len(buf), identifyLen)
	}
	return Controller{
		Serial:  ascii(buf[4:24]),
		Model:   ascii(buf[24:64]),
		BDMName: ascii(buf[3072:3104]),
	}, nil
}

func ParseIdentifyNamespace(buf []byte) (Namespace, error) {
	if len(buf) < identifyLen {
		return Namespace{}, fmt.Errorf("nvme: identify namespace buffer %d bytes, want %d", len(buf), identifyLen)
	}
	nsze := binary.LittleEndian.Uint64(buf[0:8])
	flbas := buf[26] & 0x0F
	lbads := buf[128+flbas*4+2]
	if lbads == 0 || lbads > 16 {
		return Namespace{}, fmt.Errorf("nvme: implausible lbads %d", lbads)
	}
	return Namespace{SizeLBAs: nsze, LBABytes: 1 << lbads}, nil
}

func ascii(b []byte) string {
	return strings.TrimRight(strings.TrimRight(string(b), "\x00"), " ")
}
```

`pkg/nvme/ioctl_linux.go`:

```go
//go:build linux

package nvme

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// struct nvme_admin_cmd from <linux/nvme_ioctl.h>, 72 bytes.
type adminCmd struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	NSID        uint32
	CDW2, CDW3  uint32
	Metadata    uint64
	Addr        uint64
	MetadataLen uint32
	DataLen     uint32
	CDW10       uint32
	CDW11       uint32
	CDW12       uint32
	CDW13       uint32
	CDW14       uint32
	CDW15       uint32
	TimeoutMS   uint32
	Result      uint32
}

// _IOWR('N', 0x41, struct nvme_admin_cmd)
const nvmeIoctlAdminCmd = 0xC0484E41

func identify(devPath string, nsid, cns uint32) ([]byte, error) {
	f, err := os.OpenFile(devPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, identifyLen)
	cmd := adminCmd{
		Opcode:    0x06, // Identify
		NSID:      nsid,
		Addr:      uint64(uintptr(unsafe.Pointer(&buf[0]))),
		DataLen:   identifyLen,
		CDW10:     cns,
		TimeoutMS: 2000,
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd))); e != 0 {
		return nil, fmt.Errorf("nvme identify(cns=%d) %s: %w", cns, devPath, e)
	}
	return buf, nil
}

func IdentifyController(devPath string) (Controller, error) {
	b, err := identify(devPath, 0, 1)
	if err != nil {
		return Controller{}, err
	}
	return ParseIdentifyController(b)
}

func IdentifyNamespace(devPath string, nsid uint32) (Namespace, error) {
	b, err := identify(devPath, nsid, 0)
	if err != nil {
		return Namespace{}, err
	}
	return ParseIdentifyNamespace(b)
}
```

`pkg/nvme/ioctl_other.go`:

```go
//go:build !linux

package nvme

func IdentifyController(string) (Controller, error)      { return Controller{}, ErrUnsupported }
func IdentifyNamespace(string, uint32) (Namespace, error) { return Namespace{}, ErrUnsupported }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/nvme/ -v && GOOS=linux GOARCH=amd64 go vet ./pkg/nvme/`
Expected: PASS; the linux cross-vet compiles the ioctl file.

- [ ] **Step 5: Commit**

```bash
git add pkg/nvme/
git commit -m "feat(nvme): live Identify Controller/Namespace over ioctl, parsers pinned by tests" -- pkg/nvme/
```

---

### Task 3: `cmd/km-volumes` core — manifest, state, validated `mount`

**Files:**
- Create: `cmd/km-volumes/main.go` (verb dispatch), `cmd/km-volumes/manifest.go`, `cmd/km-volumes/state.go`, `cmd/km-volumes/system.go` (interfaces + real linux impl), `cmd/km-volumes/mount.go`
- Test: `cmd/km-volumes/mount_test.go`, `cmd/km-volumes/manifest_test.go`

**Interfaces:**
- Produces (package `main`, all exported-within-package):
  ```go
  type Manifest struct { Version int `json:"version"`; Volumes []Volume `json:"volumes"` }
  type Volume struct { Mountpoint, BDM, VolumeID string; SizeSectors uint64; FSUUID, FSType, FromSnapshot, Label string }  // json tags as in spec §4
  const manifestPath = "/var/lib/km/volumes.json"; const statePath = "/var/lib/km/volumes.state"
  type Outcome string // "mounted" | "refused" | "unvalidated" | "absent" | "ambiguous" | "lazy" | "rebooting"
  type VolumeState struct { Mountpoint, VolumeID string; Outcome Outcome; Step, Expected, Actual, Reason string; At time.Time }
  type State struct { UpdatedAt time.Time; Volumes []VolumeState; RebootedForResume string }
  type Device struct { Node, BDF string; Serial, VolumeID string; LiveBytes, KernelBytes uint64 }
  type System interface {
      ListDevices(ctx) ([]Device, error)          // every /dev/nvme*n1 with a LIVE identify; root controller excluded
      Blkid(node string) (uuid, fstype, source string, err error)  // source = node or first child partition
      Mount(source, target, fstype string) error
      Unmount(target string, lazy bool) error
      KillHolders(target string, sig syscall.Signal) error
      Reprobe(bdf string) error                   // pci remove + rescan + wait for node
      Ext4BlockCount(source string) (count, blockSize uint64, err error)  // via dumpe2fs -h
      Reboot() error
      WriteFile(path string, b []byte, mode os.FileMode) error
      Exists(path string) bool
  }
  func runMount(ctx, sys System, m *Manifest, opts MountOpts) (State, error)  // MountOpts{Policy string; FromResume bool}
  ```

- [ ] **Step 1: Write the failing tests**

`cmd/km-volumes/mount_test.go` — a `fakeSystem` implementing `System` with a device table, blkid table, recorded mounts/unmounts/kills/reprobes/reboots, then:

```go
func twoVolumeManifest() *Manifest {
	return &Manifest{Version: 1, Volumes: []Volume{
		{Mountpoint: "/data", BDM: "f", VolumeID: "vol-0fed1b11de755d9aa", SizeSectors: 62914560, FSUUID: "d9082e9d", FSType: "ext4", Label: "additional volume"},
		{Mountpoint: "/repos", BDM: "g", VolumeID: "vol-0b79fa8db9f48ee7c", SizeSectors: 335544320, FSUUID: "0fea598a", FSType: "ext4", FromSnapshot: "snap-0fe5725492872309c", Label: "snapshot"},
	}}
}

func healthy() *fakeSystem {
	return &fakeSystem{
		devices: []Device{
			{Node: "/dev/nvme1n1", BDF: "0000:00:1f.0", VolumeID: "vol-0fed1b11de755d9aa", LiveBytes: 62914560 * 512, KernelBytes: 62914560 * 512},
			{Node: "/dev/nvme2n1", BDF: "0000:00:1e.0", VolumeID: "vol-0b79fa8db9f48ee7c", LiveBytes: 335544320 * 512, KernelBytes: 335544320 * 512},
		},
		blkid: map[string][3]string{"/dev/nvme1n1": {"d9082e9d", "ext4", "/dev/nvme1n1"}, "/dev/nvme2n1": {"0fea598a", "ext4", "/dev/nvme2n1"}},
		ext4:  map[string][2]uint64{"/dev/nvme1n1": {7864320, 4096}, "/dev/nvme2n1": {41943040, 4096}},
	}
}

func TestMount_HealthyMountsBothByLiveSerial(t *testing.T) {
	sys := healthy()
	// Nodes deliberately NOT in BDM order: /data's device is nvme2 by letter but the
	// serial says nvme1 — serial must win.
	st, err := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if err != nil { t.Fatal(err) }
	if len(sys.mounted) != 2 || sys.mounted["/data"] != "/dev/nvme1n1" || sys.mounted["/repos"] != "/dev/nvme2n1" {
		t.Errorf("mounted = %v", sys.mounted)
	}
	for _, v := range st.Volumes { if v.Outcome != "mounted" { t.Errorf("%s: %+v", v.Mountpoint, v) } }
}

// The observed failure: serials right, sizes swapped.
func TestMount_ObservedCrossWiringRefusesBothAtSizeStep(t *testing.T) {
	sys := healthy()
	sys.devices[0].KernelBytes, sys.devices[1].KernelBytes = sys.devices[1].KernelBytes, sys.devices[0].KernelBytes
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if len(sys.mounted) != 0 { t.Fatalf("must mount nothing, mounted %v", sys.mounted) }
	for _, v := range st.Volumes {
		if v.Outcome != "refused" || v.Step != "size" { t.Errorf("%s: %+v", v.Mountpoint, v) }
	}
	if !sys.Exists("/repos/.km-mount-refused") || !sys.Exists("/data/.km-mount-refused") { t.Error("markers not written") }
	if len(sys.reboots) != 0 { t.Error("refuse policy must not reboot") }
}

func TestMount_UUIDMismatchRefusesOnlyThatEntry(t *testing.T) {
	sys := healthy()
	sys.blkid["/dev/nvme2n1"] = [3]string{"d9082e9d", "ext4", "/dev/nvme2n1"} // /repos node carries /data's superblock
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if sys.mounted["/data"] == "" || sys.mounted["/repos"] != "" { t.Errorf("mounted = %v", sys.mounted) }
	if st.Volumes[1].Outcome != "refused" || st.Volumes[1].Step != "fsuuid" { t.Errorf("%+v", st.Volumes[1]) }
}

func TestMount_AbsentAndAmbiguous(t *testing.T) {
	sys := healthy()
	sys.devices = sys.devices[:1] // /repos volume gone
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[1].Outcome != "absent" { t.Errorf("%+v", st.Volumes[1]) }
	sys = healthy()
	sys.devices = append(sys.devices, sys.devices[1]) // two nodes claim /repos's serial
	st, _ = runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[1].Outcome != "ambiguous" || sys.mounted["/repos"] != "" { t.Errorf("%+v %v", st.Volumes[1], sys.mounted) }
}

func TestMount_SuperblockLargerThanDeviceRefuses(t *testing.T) {
	sys := healthy()
	sys.ext4["/dev/nvme1n1"] = [2]uint64{41943040, 4096} // 160G superblock on the 30G device
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[0].Outcome != "refused" || st.Volumes[0].Step != "superblock" { t.Errorf("%+v", st.Volumes[0]) }
}

func TestMount_SuccessRemovesMarker(t *testing.T) {
	sys := healthy()
	sys.files["/repos/.km-mount-refused"] = true
	runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if sys.Exists("/repos/.km-mount-refused") { t.Error("marker must be removed before a successful mount") }
}

func TestMount_NoManifestFallsBackToBDMAndRecordsUnvalidated(t *testing.T) {
	sys := healthy()
	sys.bdm = map[string]string{"f": "/dev/nvme1n1", "g": "/dev/nvme2n1"}
	st, _ := runMount(context.Background(), sys, nil, MountOpts{Policy: "refuse", Fallback: []FallbackEntry{{Mountpoint: "/data", BDM: "f"}, {Mountpoint: "/repos", BDM: "g"}}})
	if len(sys.mounted) != 2 { t.Errorf("mounted = %v", sys.mounted) }
	for _, v := range st.Volumes { if v.Outcome != "unvalidated" { t.Errorf("%+v", v) } }
}
```

(`MountOpts.Fallback` carries the `AdditionalVolumeMounts` entries the unit passes on the command line — mountpoint+letter — so a pre-manifest box still mounts. The fake's `ResolveBDM(letter)` backs it; add it to `System`.)

`cmd/km-volumes/manifest_test.go`:

```go
func TestManifest_AppendIsIdempotentPerMountpoint(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "volumes.json")
	v := Volume{Mountpoint: "/repos", VolumeID: "vol-1", SizeSectors: 10, FSUUID: "u", FSType: "ext4"}
	if err := appendManifestEntry(p, v); err != nil { t.Fatal(err) }
	v2 := v; v2.VolumeID = "vol-2"
	if err := appendManifestEntry(p, v2); err == nil { t.Error("second entry for same mountpoint must be refused") }
	m, err := loadManifest(p)
	if err != nil || len(m.Volumes) != 1 || m.Volumes[0].VolumeID != "vol-1" { t.Errorf("%+v %v", m, err) }
}

func TestManifest_MissingIsNilNotError(t *testing.T) {
	m, err := loadManifest(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || m != nil { t.Errorf("got %+v, %v", m, err) }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/km-volumes/ 2>&1 | head -5`
Expected: build FAIL — nothing exists yet.

- [ ] **Step 3: Implement**

`manifest.go`: the types with the spec §4 JSON tags; `loadManifest(path) (*Manifest, error)` (nil, nil on not-exist); `appendManifestEntry(path, Volume) error` (load-or-new, refuse duplicate mountpoint, write `0644` atomically via temp+rename).

`state.go`: `State`/`VolumeState`; `writeState(sys System, st State)` marshals to `statePath`; `readState()`.

`system.go`: the `System` interface exactly as in Interfaces plus `ResolveBDM(letter string) (node string, err error)`; `realSystem` (linux) implementing it with: `ListDevices` = glob `/dev/nvme*n1`, skip the root controller (root device from `findmnt -no SOURCE /`, strip partition suffix, compare controller dir `/sys/block/<dev>/device`), for each: `nvme.IdentifyController` + `nvme.IdentifyNamespace(…,1)` for `LiveBytes`, `BLKGETSIZE64` (`unix.IoctlGetUint64`… use `unix.BLKGETSIZE64` via `Syscall`) for `KernelBytes`, `BDF` = `filepath.Base(readlink /sys/block/<dev>/device/device)`; `Blkid` = `blkid -s UUID -s TYPE -o value` on the node, falling back to the first child partition (today's `lsblk` rule); `Ext4BlockCount` = `dumpe2fs -h` parse of `Block count:` / `Block size:`; `Mount`/`Unmount`/`KillHolders`/`Reprobe`/`Reboot` shell out with `exec.CommandContext` under per-call timeouts (mount 10 s, umount 3 s, fuser 3 s, reprobe wait ≤ 10 s polling for the node). `realSystem` is `//go:build linux`; a `!linux` stub returns errors so the binary still compiles for tests on macOS (only `main` calls the real one).

`mount.go` — `runMount`:

```go
type MountOpts struct {
	Policy     string          // "refuse" | "reboot"
	FromResume bool            // only post-sleep sets this; reboot policy needs it
	Fallback   []FallbackEntry // AdditionalVolumeMounts from the unit's args, for a pre-manifest box
}
type FallbackEntry struct{ Mountpoint, BDM string }

func runMount(ctx context.Context, sys System, m *Manifest, opts MountOpts) (State, error) {
	st := State{UpdatedAt: time.Now().UTC()}
	if m == nil {
		for _, f := range opts.Fallback {
			vs := VolumeState{Mountpoint: f.Mountpoint, Outcome: "unvalidated", Reason: "no manifest — created before km-volumes; recreate to protect", At: st.UpdatedAt}
			if node, err := sys.ResolveBDM(f.BDM); err == nil {
				if uuid, fstype, src, err := sys.Blkid(node); err == nil && uuid != "" {
					if err := sys.Mount(src, f.Mountpoint, fstype); err != nil {
						vs.Outcome, vs.Reason = "refused", "mount: "+err.Error()
					}
				}
			} else {
				vs.Reason = "no manifest and BDM " + f.BDM + " unresolved"
			}
			st.Volumes = append(st.Volumes, vs)
		}
		return st, writeState(sys, st)
	}
	devs, err := sys.ListDevices(ctx)
	if err != nil {
		return st, err
	}
	for _, v := range m.Volumes {
		vs := validateOne(sys, devs, v)
		if vs.Outcome == "ok" {
			sys.WriteFile(filepath.Join(v.Mountpoint, ".km-mount-refused"), nil, 0) // WriteFile with nil body = remove, see system.go
			if err := sys.Mount(vs.source, v.Mountpoint, v.FSType); err != nil {
				vs.Outcome, vs.Step, vs.Reason = "refused", "mount", err.Error()
			} else {
				vs.Outcome = "mounted"
			}
		}
		if vs.Outcome == "refused" || vs.Outcome == "absent" || vs.Outcome == "ambiguous" {
			sys.WriteFile(filepath.Join(v.Mountpoint, ".km-mount-refused"), []byte(vs.Reason+"\n"), 0o644)
			emitAudit(sys, "volume_mount_refused", v, vs)
		}
		st.Volumes = append(st.Volumes, vs)
	}
	return st, writeState(sys, st)
}

// validateOne is the spec §5.1 ladder: serial → size → fsuuid → superblock.
func validateOne(sys System, devs []Device, v Volume) VolumeState {
	vs := VolumeState{Mountpoint: v.Mountpoint, VolumeID: v.VolumeID, At: time.Now().UTC()}
	var match []Device
	for _, d := range devs {
		if d.VolumeID == v.VolumeID {
			match = append(match, d)
		}
	}
	switch {
	case len(match) == 0:
		vs.Outcome, vs.Step, vs.Reason = "absent", "serial", "no attached NVMe device reports serial "+v.VolumeID
		return vs
	case len(match) > 1:
		vs.Outcome, vs.Step, vs.Reason = "ambiguous", "serial", fmt.Sprintf("%d devices report serial %s", len(match), v.VolumeID)
		return vs
	}
	d := match[0]
	want := v.SizeSectors * 512
	if d.LiveBytes != want || d.KernelBytes != want {
		vs.Outcome, vs.Step = "refused", "size"
		vs.Expected, vs.Actual = fmt.Sprintf("%d", want), fmt.Sprintf("live=%d kernel=%d", d.LiveBytes, d.KernelBytes)
		vs.Reason = fmt.Sprintf("size: live %s, kernel %s, expected %s", human(d.LiveBytes), human(d.KernelBytes), human(want))
		return vs
	}
	uuid, fstype, src, err := sys.Blkid(d.Node)
	if err != nil || uuid != v.FSUUID {
		vs.Outcome, vs.Step, vs.Expected, vs.Actual = "refused", "fsuuid", v.FSUUID, uuid
		vs.Reason = fmt.Sprintf("filesystem UUID on %s is %q, expected %q", d.Node, uuid, v.FSUUID)
		return vs
	}
	if fstype == "ext4" {
		if count, bs, err := sys.Ext4BlockCount(src); err == nil && count*bs > d.LiveBytes {
			vs.Outcome, vs.Step = "refused", "superblock"
			vs.Reason = fmt.Sprintf("ext4 superblock describes %s but the device is %s", human(count*bs), human(d.LiveBytes))
			return vs
		}
	}
	vs.Outcome, vs.source = "ok", src
	return vs
}
```

(`VolumeState` gets an unexported `source string` field; `human()` formats GiB; `emitAudit` writes the `init_failed`-shaped JSON line to `/run/km/audit-pipe` under `timeout 0.1`-equivalent — a non-blocking `O_WRONLY|O_NONBLOCK` open that gives up on `ENXIO`.)

`main.go`: verbs `manifest`, `mount`, `status`, `pre-sleep`, `post-sleep`, `repair` dispatching on `os.Args[1]`, flags via `flag.NewFlagSet`. `mount` reads `KM_VOLUMES_ON_MISMATCH` (default `refuse`) and `--fallback mp:letter` (repeatable). `manifest` takes `--mountpoint --bdm --device --label [--from-snapshot]`, runs `nvme.IdentifyController` on the controller node (strip the `p<N>` partition suffix and the `n1`), `BLKGETSIZE64`, `blkid`, and `appendManifestEntry`. `status` prints `readState()` as JSON. `pre-sleep`/`post-sleep`/`repair` land in Tasks 4/5.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/km-volumes/ -v 2>&1 | grep -E '^(--- |ok|FAIL)' && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/km-volumes`
Expected: all PASS; the linux cross-build succeeds (this is the build `buildAndUploadSidecars` performs).

- [ ] **Step 5: Commit**

```bash
git add cmd/km-volumes/
git commit -m "feat(km-volumes): manifest, state, validated mount by live NVMe serial" -- cmd/km-volumes/
```

---

### Task 4: `pre-sleep` — bounded unmount ladder, always exit 0

**Files:**
- Create: `cmd/km-volumes/sleep.go`
- Test: `cmd/km-volumes/sleep_test.go`

**Interfaces:**
- Produces: `func runPreSleep(ctx, sys System, m *Manifest) State` — never returns an error; records `lazy` per volume that needed `umount -l`.

- [ ] **Step 1: Write the failing tests**

```go
func TestPreSleep_CleanUnmount(t *testing.T) {
	sys := healthy(); sys.mounted = map[string]string{"/data": "/dev/nvme1n1", "/repos": "/dev/nvme2n1"}
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.unmounted) != 2 || len(sys.kills) != 0 { t.Errorf("unmounted=%v kills=%v", sys.unmounted, sys.kills) }
	for _, v := range st.Volumes { if v.Outcome != "unmounted" { t.Errorf("%+v", v) } }
}

func TestPreSleep_BusyEscalatesTERMThenKILLThenLazy(t *testing.T) {
	sys := healthy(); sys.mounted = map[string]string{"/repos": "/dev/nvme2n1"}
	sys.busy["/repos"] = 3 // first three umount attempts return EBUSY
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	wantSigs := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	if !reflect.DeepEqual(sys.kills["/repos"], wantSigs) { t.Errorf("kills = %v", sys.kills["/repos"]) }
	if !sys.lazyUnmounts["/repos"] { t.Error("must fall back to umount -l after KILL") }
	if st.Volumes[1].Outcome != "lazy" { t.Errorf("%+v", st.Volumes[1]) }
}

func TestPreSleep_BusyClearsAfterTERM(t *testing.T) {
	sys := healthy(); sys.mounted = map[string]string{"/repos": "/dev/nvme2n1"}
	sys.busy["/repos"] = 1
	runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.kills["/repos"]) != 1 || sys.lazyUnmounts["/repos"] { t.Errorf("kills=%v lazy=%v", sys.kills, sys.lazyUnmounts) }
}

func TestPreSleep_NotMountedIsNoop(t *testing.T) {
	sys := healthy()
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.unmounted) != 0 { t.Error("nothing to unmount") }
	for _, v := range st.Volumes { if v.Outcome != "not-mounted" { t.Errorf("%+v", v) } }
}

func TestPreSleep_NilManifestIsNoop(t *testing.T) {
	sys := healthy()
	runPreSleep(context.Background(), sys, nil)
	if len(sys.unmounted) != 0 { t.Error("no manifest → touch nothing") }
}
```

(Add `busy map[string]int`, `unmounted []string`, `lazyUnmounts map[string]bool`, `kills map[string][]syscall.Signal`, `IsMounted(target)` to the fake and `IsMounted` to `System`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/km-volumes/ -run TestPreSleep 2>&1 | head -3`
Expected: FAIL — `runPreSleep` undefined.

- [ ] **Step 3: Implement**

```go
// runPreSleep is spec §5.3. Every step is bounded inside System; nothing here
// may block the systemd transition (addendum §C), so the ladder is fixed-length
// and the outcome — including a lazy detach — is recorded rather than failed.
func runPreSleep(ctx context.Context, sys System, m *Manifest) State {
	st := State{UpdatedAt: time.Now().UTC()}
	if m == nil {
		return st
	}
	sys.Sync()
	for _, v := range m.Volumes {
		vs := VolumeState{Mountpoint: v.Mountpoint, VolumeID: v.VolumeID, At: time.Now().UTC()}
		if !sys.IsMounted(v.Mountpoint) {
			vs.Outcome = "not-mounted"
			st.Volumes = append(st.Volumes, vs)
			continue
		}
		vs.Outcome = "unmounted"
		if err := sys.Unmount(v.Mountpoint, false); err != nil {
			sys.KillHolders(v.Mountpoint, syscall.SIGTERM)
			time.Sleep(time.Second)
			if err := sys.Unmount(v.Mountpoint, false); err != nil {
				sys.KillHolders(v.Mountpoint, syscall.SIGKILL)
				time.Sleep(time.Second)
				if err := sys.Unmount(v.Mountpoint, false); err != nil {
					_ = sys.Unmount(v.Mountpoint, true) // last resort; post-sleep's re-probe EIOs any survivor
					vs.Outcome, vs.Reason = "lazy", "holders survived SIGKILL; detached lazily"
				}
			}
		}
		st.Volumes = append(st.Volumes, vs)
	}
	_ = writeState(sys, st)
	return st
}
```

Add `Sync()` and `IsMounted(string) bool` to `System`; real impls: `unix.Sync()`, `findmnt -no TARGET <mp>`.

- [ ] **Step 4: Run, commit**

Run: `go test ./cmd/km-volumes/ -run TestPreSleep -v | grep -E '^(--- |ok|FAIL)'`

```bash
git add cmd/km-volumes/sleep.go cmd/km-volumes/sleep_test.go cmd/km-volumes/system.go cmd/km-volumes/mount_test.go
git commit -m "feat(km-volumes): pre-sleep — bounded unmount ladder, lazy only as a marked last resort" -- cmd/km-volumes/
```

---

### Task 5: `post-sleep` — unconditional re-probe, retry once, policy

**Files:**
- Modify: `cmd/km-volumes/sleep.go`
- Test: `cmd/km-volumes/sleep_test.go`

**Interfaces:**
- Produces: `func runPostSleep(ctx, sys System, m *Manifest, policy string, resumeID string) State` — `resumeID` = boot id (`/proc/sys/kernel/random/boot_id`) so the reboot guard is per-resume.

- [ ] **Step 1: Write the failing tests**

```go
func TestPostSleep_ReprobesEveryNonRootControllerThenMounts(t *testing.T) {
	sys := healthy()
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if !reflect.DeepEqual(sys.reprobed, []string{"0000:00:1f.0", "0000:00:1e.0"}) { t.Errorf("reprobed = %v", sys.reprobed) }
	if len(sys.mounted) != 2 { t.Errorf("mounted = %v", sys.mounted) }
	_ = st
}

func TestPostSleep_MismatchReprobesOnceMoreThenRefuses(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 62914560 * 512 // /repos stale until the second reprobe
	sys.fixOnReprobe = map[string]int{"0000:00:1e.0": 2} // fake: after the 2nd reprobe of this BDF, KernelBytes becomes correct
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if st.Volumes[1].Outcome != "mounted" { t.Errorf("second reprobe should have healed it: %+v", st.Volumes[1]) }
	if n := count(sys.reprobed, "0000:00:1e.0"); n != 2 { t.Errorf("reprobed /repos %d times, want 2", n) }
}

func TestPostSleep_RebootPolicyRebootsOncePerResume(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 1 // never heals
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 1 || st.Volumes[1].Outcome != "rebooting" { t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1]) }
	// Same resume id again (the guard file persisted) → refuse, no second reboot.
	sys.reboots = nil
	st = runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 0 || st.Volumes[1].Outcome != "refused" { t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1]) }
}

func TestPostSleep_RefusePolicyNeverReboots(t *testing.T) {
	sys := healthy(); sys.devices[1].KernelBytes = 1
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if len(sys.reboots) != 0 { t.Error("refuse must not reboot") }
}

func TestPostSleep_NeverReprobesRoot(t *testing.T) {
	// ListDevices already excludes root; assert the fake's root BDF never appears.
	sys := healthy(); sys.rootBDF = "0000:00:04.0"
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	for _, b := range sys.reprobed { if b == sys.rootBDF { t.Fatal("root controller re-probed") } }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/km-volumes/ -run TestPostSleep 2>&1 | head -3`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
// runPostSleep is spec §5.4: from-scratch probe first, validation second,
// policy last. The re-probe is unconditional because the write-up showed a
// fresh probe (a reboot) is the known-good state, and detecting staleness is
// the part we trust least.
func runPostSleep(ctx context.Context, sys System, m *Manifest, policy, resumeID string) State {
	if m == nil {
		return State{UpdatedAt: time.Now().UTC()}
	}
	devs, err := sys.ListDevices(ctx)
	if err == nil {
		for _, d := range devs {
			_ = sys.Reprobe(d.BDF) // bounded inside System; a failure just leaves the old node
		}
	}
	st, _ := runMount(ctx, sys, m, MountOpts{Policy: policy, FromResume: true})
	// One more re-probe for any entry that refused on identity or size.
	retried := false
	for i, vs := range st.Volumes {
		if vs.Outcome == "refused" && (vs.Step == "serial" || vs.Step == "size") || vs.Outcome == "absent" {
			if bdf := bdfFor(devs, vs.VolumeID); bdf != "" {
				_ = sys.Reprobe(bdf)
				retried = true
			}
		}
		_ = i
	}
	if retried {
		st, _ = runMount(ctx, sys, m, MountOpts{Policy: policy, FromResume: true})
	}
	if policy == "reboot" && anyRefused(st) && !rebootedFor(sys, resumeID) {
		markRebootedFor(sys, resumeID)
		for i := range st.Volumes {
			if st.Volumes[i].Outcome != "mounted" {
				st.Volumes[i].Outcome = "rebooting"
			}
		}
		_ = writeState(sys, st)
		emitAuditReboot(sys, st)
		_ = sys.Reboot()
		return st
	}
	_ = writeState(sys, st)
	return st
}
```

`rebootedFor`/`markRebootedFor` read/write `/var/lib/km/volumes.reboots` with the resume id (boot id read once in `main` before the reboot, so the *post-reboot* boot id differs and the guard is naturally per-resume — document this in a comment). `anyRefused` = any outcome in {refused, absent, ambiguous}.

- [ ] **Step 4: Wire the verbs in `main.go`**

`pre-sleep`: load manifest, `runPreSleep`, exit 0 always. `post-sleep`: load manifest, policy from `KM_VOLUMES_ON_MISMATCH`, resume id from `/proc/sys/kernel/random/boot_id`, `runPostSleep`, exit 0 always. Both wrap the whole body in a `recover()` that logs and exits 0 — a panic must never fail the systemd transition.

- [ ] **Step 5: Run, cross-build, commit**

Run: `go test ./cmd/km-volumes/ -v | grep -E '^(--- |ok|FAIL)' && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/km-volumes`

```bash
git add cmd/km-volumes/
git commit -m "feat(km-volumes): post-sleep — unconditional PCI re-probe, one retry, refuse|reboot policy" -- cmd/km-volumes/
```

---

### Task 6: `repair` — operator-only backup-superblock recovery

**Files:**
- Create: `cmd/km-volumes/repair.go`
- Test: `cmd/km-volumes/repair_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestRepair_RefusesNonSnapshotWithoutFlag(t *testing.T) {
	sys := healthy(); sys.state = State{Volumes: []VolumeState{{Mountpoint: "/data", Outcome: "refused"}}}
	err := runRepair(context.Background(), sys, twoVolumeManifest(), "/data", false)
	if err == nil || !strings.Contains(err.Error(), "--i-accept-data-loss") { t.Errorf("err = %v", err) }
	if len(sys.fscks) != 0 { t.Error("must not touch the volume") }
}

func TestRepair_TriesBackupSuperblocksUntilOneValidates(t *testing.T) {
	sys := healthy(); sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "refused"}}}
	sys.backupSuperblocks["/dev/nvme2n1"] = []uint64{32768, 98304, 11239424}
	sys.fsckSucceedsAt = map[string]uint64{"/dev/nvme2n1": 11239424}
	if err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", false); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(sys.fscks["/dev/nvme2n1"], []uint64{32768, 98304, 11239424}) { t.Errorf("fscks = %v", sys.fscks) }
	if sys.mounted["/repos"] == "" { t.Error("must re-run mount after a successful fsck") }
}

func TestRepair_NeverTouchesAValidatingVolume(t *testing.T) {
	sys := healthy(); sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "mounted"}}}
	err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", true)
	if err == nil || len(sys.fscks) != 0 { t.Errorf("err=%v fscks=%v", err, sys.fscks) }
}
```

(Add `BackupSuperblocks(node) ([]uint64, error)` — parses `mke2fs -n` — and `Fsck(node string, superblock uint64) error` to `System` and the fake.)

- [ ] **Step 2: Run to verify it fails; Step 3: implement `runRepair` per spec §5.7** (resolve the manifest entry by mountpoint; refuse if state is not refused/absent; refuse a non-snapshot volume without the flag; find the device by live serial; for each backup superblock `sys.Fsck(node, sb)`; on the first success `runMount` again and return nil; else return an error naming the `terraform taint` runbook).

- [ ] **Step 4: Run, commit**

```bash
git add cmd/km-volumes/
git commit -m "feat(km-volumes): repair — operator-only backup-superblock recovery for refused volumes" -- cmd/km-volumes/
```

---

### Task 7: Userdata — sidecar fetch, manifest call, unit, sleep shim, no more fstab lines

**Files:**
- Modify: `pkg/compiler/userdata.go` (~1181 sidecar download block; ~296-327 additional-volume block; add a section after the km-presence unit ~4034)
- Modify: `internal/app/cmd/init.go` (`sidecarBuilds()` gains `{name: "km-volumes", srcDir: "cmd/km-volumes"}`)
- Test: `pkg/compiler/userdata_volumes_test.go` (new), goldens under `pkg/compiler/testdata/`, `internal/app/cmd/init_sidecars_test.go` (existing pairing test must pass)

- [ ] **Step 1: Write the failing tests**

`pkg/compiler/userdata_volumes_test.go`:

```go
// The sleep shim is the one thing that can wedge hibernation (addendum §C):
// it must bound km-volumes and exit 0 whatever happens. Execute the rendered
// shim under sh with a km-volumes that hangs.
func TestUserdataVolumes_SleepShimIsBoundedAndExitsZero(t *testing.T) {
	out := renderUserdataFor(t, "testdata/profile_additional_volume_only.yaml")
	shim := extractHeredoc(t, out, "/usr/lib/systemd/system-sleep/km-volumes", "KMVOLSLEEP")
	dir := t.TempDir()
	fake := filepath.Join(dir, "km-volumes")
	os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	shim = strings.ReplaceAll(shim, "/opt/km/bin/km-volumes", fake)
	shim = strings.ReplaceAll(shim, "timeout 15", "timeout 1")
	start := time.Now()
	cmd := exec.Command("sh", "-c", shim, "sh", "pre", "hibernate")
	if err := cmd.Run(); err != nil {
		t.Fatalf("shim must exit 0 even when km-volumes hangs: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("shim did not bound km-volumes (took %s)", time.Since(start))
	}
}

func TestUserdataVolumes_NoFstabLineAndUnitPresent(t *testing.T) {
	out := renderUserdataFor(t, "testdata/profile_additional_volume_only.yaml")
	if strings.Contains(out, ">> /etc/fstab") {
		t.Error("additional volumes must no longer be written to /etc/fstab")
	}
	for _, want := range []string{"km-volumes.service", "km-volumes manifest --mountpoint", "KM_VOLUMES_ON_MISMATCH=refuse", "sidecars/km-volumes"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered userdata missing %q", want)
		}
	}
}

func TestUserdataVolumes_NoVolumesIsByteIdentical(t *testing.T) {
	out := renderUserdataFor(t, "testdata/profile_no_volumes.yaml")
	for _, absent := range []string{"km-volumes", "system-sleep"} {
		if strings.Contains(out, absent) {
			t.Errorf("profile without additional volumes must not render %q", absent)
		}
	}
}

func TestUserdataVolumes_PolicyRendersFromProfile(t *testing.T) {
	out := renderUserdataFor(t, "testdata/profile_additional_volume_reboot.yaml") // onVolumeMismatch: reboot
	if !strings.Contains(out, "KM_VOLUMES_ON_MISMATCH=reboot") {
		t.Error("policy not rendered")
	}
}
```

(`renderUserdataFor`/`extractHeredoc` — check `pkg/compiler/userdata_test.go` for the existing helpers that load a profile and call `generateUserData`; reuse them, else add thin ones. Create the two small testdata profiles; `profile_additional_volume_only.yaml` likely already exists under another name — `ls pkg/compiler/testdata/*.yaml`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserdataVolumes 2>&1 | head`
Expected: FAIL (fstab line present, unit absent).

- [ ] **Step 3: Template edits**

(a) Sidecar fetch, next to `km-presence` (~1181), gated:

```
{{- if .AdditionalVolumeMounts }}
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-volumes" /opt/km/bin/km-volumes
chmod 0755 /opt/km/bin/km-volumes
{{- end }}
```

(b) Additional-volume block: replace the `UUID=…` / `grep -q … /etc/fstab` / `mount -a` lines with:

```
  # km-volumes owns this volume from the next boot on: record its PHYSICAL
  # identity now — the first cold boot is the one moment the BDM letter →
  # device binding is trusted — then mount directly for this boot only.
  # No fstab line: /etc/fstab mounts by UUID with no check that the device
  # behind the UUID is the volume it should be, which is how a hibernate/resume
  # cross-wrote two volumes (spec 2026-09-20-hibernate-volume-validation).
  /opt/km/bin/km-volumes manifest --mountpoint "{{ .MountPoint }}" --bdm "{{ .DeviceLetter }}" \
    --device "$MOUNT_SRC" --label "{{ .Label }}"{{ if .FromSnapshot }} --from-snapshot "{{ .FromSnapshot }}"{{ end }} \
    || echo "[km-bootstrap] WARNING: km-volumes manifest failed for {{ .MountPoint }}; this volume will mount unvalidated"
  mount -t "$FSTYPE" "$MOUNT_SRC" "{{ .MountPoint }}" || true
```

Add `FromSnapshot string` to `AdditionalVolumeMountEntry` and populate it in the `mounts` construction (~7160: `FromSnapshot: snap.SnapshotID`).

(c) New section after the km-presence unit, gated on `{{ if .AdditionalVolumeMounts }}`:

```
# ============================================================
# 7.x. km-volumes: validated mounts on every boot + hibernate hooks
# ============================================================
mkdir -p /var/lib/km
cat > /etc/systemd/system/km-volumes.service << 'UNIT'
[Unit]
Description=Klankrmkr additional EBS volumes — validate by live NVMe identity, then mount
After=local-fs.target cloud-final.service
Before=km-presence.service sshd.service{{ range .PollerUnitNames }} {{ . }}{{ end }}
[Service]
Type=oneshot
RemainAfterExit=yes
Environment=KM_VOLUMES_ON_MISMATCH={{ .VolumeMismatchPolicy }}
ExecStart=/opt/km/bin/km-volumes mount{{ range .AdditionalVolumeMounts }} --fallback {{ .MountPoint }}:{{ .DeviceLetter }}{{ end }}
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable km-volumes.service
cat > /usr/lib/systemd/system-sleep/km-volumes << 'KMVOLSLEEP'
#!/bin/sh
# Bounded and always exit 0: a system-sleep script that blocks or fails stalls
# systemd-hibernate.service and trips ec2-hibinit's retry/debounce state machine.
case "$1:$2" in
  pre:hibernate)  timeout 15 /opt/km/bin/km-volumes pre-sleep  >> /var/log/km-volumes.log 2>&1 ;;
  post:hibernate) timeout 45 /opt/km/bin/km-volumes post-sleep >> /var/log/km-volumes.log 2>&1 ;;
esac
exit 0
KMVOLSLEEP
chmod 0755 /usr/lib/systemd/system-sleep/km-volumes
echo "[km-bootstrap] km-volumes.service + sleep hook installed"
```

`VolumeMismatchPolicy` is a new `userDataParams` field (default `"refuse"`, from `p.Spec.Runtime.OnVolumeMismatch` — Task 8 adds the field; render `refuse` when empty). `PollerUnitNames` — check whether a list of the rendered poller unit names already exists in `userDataParams`; if not, hardcode the four `km-*-inbound-poller.service` names (systemd ignores `Before=` on units that don't exist).

(d) `sidecarBuilds()` entry.

- [ ] **Step 4: Run the compiler + init tests, regenerate goldens**

Run: `go test ./pkg/compiler/ ./internal/app/cmd/ -run 'TestUserdataVolumes|Golden|ByteIdent|Sidecar' 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`. Regenerate the `additional_volume_only` golden with the sanctioned capture flag named in `pkg/compiler/userdata_test.go` (NOT `CAPTURE_PRE92_BASELINE`); confirm the learn/h1 goldens are untouched (`git status` shows only the volume golden changed).

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_volumes_test.go pkg/compiler/testdata/ internal/app/cmd/init.go
git commit -m "feat(userdata): km-volumes unit + sleep hook replace the additional-volume fstab lines" -- pkg/compiler/userdata.go pkg/compiler/userdata_volumes_test.go pkg/compiler/testdata/ internal/app/cmd/init.go
```

---

### Task 8: Profile field `spec.runtime.onVolumeMismatch`

**Files:**
- Modify: `pkg/profile/types.go` (`RuntimeSpec`, next to `Hibernation`), `pkg/profile/schemas/sandbox_profile.schema.json` (runtime object, next to `hibernation`), `pkg/profile/validate.go` (WARN when set without volumes)
- Test: `pkg/profile/validate_volume_policy_test.go`

- [ ] **Step 1: Write the failing test (through YAML → schema, never the struct)**

```go
func TestOnVolumeMismatch_SchemaAcceptsEnumRejectsOther(t *testing.T) {
	base := "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata: {name: t}\nspec:\n  runtime:\n    substrate: ec2\n    instanceType: t3.medium\n    hibernation: true\n    additionalVolume: {size: 10, mountPoint: /data}\n    onVolumeMismatch: %s\n"
	for _, v := range []string{"refuse", "reboot"} {
		if err := ValidateSchema([]byte(fmt.Sprintf(base, v))); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	err := ValidateSchema([]byte(fmt.Sprintf(base, "panic")))
	if err == nil || !strings.Contains(err.Error(), "onVolumeMismatch") {
		t.Errorf("bad enum must be rejected naming the field; got %v", err)
	}
}

func TestOnVolumeMismatch_WarnsWithoutVolumes(t *testing.T) {
	y := "apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata: {name: t}\nspec:\n  runtime:\n    substrate: ec2\n    instanceType: t3.medium\n    onVolumeMismatch: reboot\n" // no additionalVolume/Snapshots
	warns := ValidateWarnings(mustLoad(t, y))
	if !containsWarn(warns, "onVolumeMismatch") { t.Errorf("warnings = %v", warns) }
}
```

(Match the real names: `grep -n "func ValidateSchema\|func .*Warn" pkg/profile/*.go`.)

- [ ] **Step 2–4:** implement field (`OnVolumeMismatch string \`yaml:"onVolumeMismatch,omitempty"\`` with a doc comment quoting spec §5.5), schema enum `["refuse","reboot"]`, WARN; run `go test ./pkg/profile/`; commit:

```bash
git add pkg/profile/
git commit -m "feat(profile): spec.runtime.onVolumeMismatch (refuse|reboot) for km-volumes" -- pkg/profile/
```

Then wire `VolumeMismatchPolicy` in `userdata.go` (Task 7's placeholder) and rerun `TestUserdataVolumes_PolicyRendersFromProfile`.

---

### Task 9: Operator surfaces — `km status`, `km resume`, `km doctor`, `klanker:sandbox`

**Files:**
- Create: `internal/app/cmd/volumes_state.go` (`fetchVolumeState(ctx, ssm, instanceID) (*volumesState, error)` — one `sendSSMAndWait` of `/opt/km/bin/km-volumes status 2>/dev/null || echo KM_NO_VOLUMES`, JSON-decoded; `renderVolumeLines(w, st)`)
- Modify: `internal/app/cmd/status.go` (after the `Init:` block), `internal/app/cmd/resume.go` (after `refreshGitHubTokenOnResume`, bounded 90 s poll until SSM answers), `internal/app/cmd/doctor.go` (+ `doctor_volumes.go`), `skills/sandbox/SKILL.md` (Section B)
- Test: `internal/app/cmd/volumes_state_test.go`

- [ ] **Step 1: Write failing tests** for `renderVolumeLines` (mounted ✓ line, refused ✗ line naming `km-volumes repair` and reboot, unvalidated ? line, `KM_NO_VOLUMES` → nothing printed) and for `fetchVolumeState` decoding via the `recordingSSM` stub pattern from `agent_auth_probe_shim_test.go`.

- [ ] **Step 2–4:** implement; in `runStatus` print a `Volumes:` section only when the fetch returns a state; in `runResume` poll (5 s interval, ≤90 s, best-effort, never fails the command) and print the same lines; `checkAdditionalVolumes` in doctor: for running sandboxes, fan out on the list_enrich pool of 8, WARN per sandbox with any refused/lazy/rebooting entry, SKIP when none has volumes. Skill: Section B gains a "Additional volumes" probe reading `/var/lib/km/volumes.state` and the `.km-mount-refused` marker with the literal wording from spec §7.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/volumes_state.go internal/app/cmd/volumes_state_test.go internal/app/cmd/status.go internal/app/cmd/resume.go internal/app/cmd/doctor.go internal/app/cmd/doctor_volumes.go skills/sandbox/SKILL.md
git commit -m "feat(status,resume,doctor): surface km-volumes state; sandbox skill reads the refuse marker" -- internal/app/cmd/ skills/sandbox/SKILL.md
```

---

### Task 10: Docs, CLAUDE.md, plugin bump

**Files:**
- Create: `docs/hibernate-volumes.md`
- Modify: `docs/operational-gotchas.md` (new section + pointer), `OPERATOR-GUIDE.md` (§ additionalSnapshots note + `onVolumeMismatch`), `CLAUDE.md` (phase block + "Where to look" row + CLI list: nothing new operator-side except the field), `.claude-plugin/{plugin,marketplace}.json` (0.4.17 → 0.4.18)

- [ ] **Step 1:** `docs/hibernate-volumes.md` — the mechanism (three sentences), the three layers, the manifest and state files, what `km status` prints and what each line means, `onVolumeMismatch`, `km-volumes repair` usage and its refusal rules, the **re-materialise from snapshot** runbook with exact commands:

```bash
# On the operator laptop, for sandbox <id> whose /repos volume is refused and unrepairable:
cd infra/live/<region>/sandboxes/<id>        # the unit km create rendered
terragrunt state list | grep aws_ebs_volume.snapshot
terragrunt taint 'module.sandbox.aws_ebs_volume.snapshot["<key>"]'
terragrunt apply                                # recreates the volume from the snapshot, re-attaches
km resume <id>                                  # km-volumes mount will refuse: the manifest's volumeId is the OLD id
km shell --root <id> -- rm /var/lib/km/volumes.json   # then reboot: first cold boot rewrites the manifest
```

(Verify the resource address against `infra/modules/ec2spot/v1.7.0/main.tf:892` and the sandbox template's module name before writing it down.) Then the `/shared` caveat, deploy surface, and the interim mitigation.

- [ ] **Step 2:** CLAUDE.md block modelled on the shared-credentials one: what shipped, the three non-obvious facts (sysfs serial is stale; re-probe is unconditional; reboot is once per resume), deploy surface, existing boxes need recreate + `hibernation: false` meanwhile.

- [ ] **Step 3:** Commit

```bash
git add docs/hibernate-volumes.md docs/operational-gotchas.md OPERATOR-GUIDE.md CLAUDE.md .claude-plugin/plugin.json .claude-plugin/marketplace.json
git commit -m "docs: hibernate-safe additional volumes runbook; plugin 0.4.18" -- docs/ OPERATOR-GUIDE.md CLAUDE.md .claude-plugin/
```

---

### Task 11: Full verification, live UAT, PR

- [ ] **Step 1:** `go build ./... && go vet ./... && go test ./...` → only the known pre-existing failures. `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/km-volumes ./cmd/km` (the sidecar build shape).

- [ ] **Step 2:** `make build && make build-lambdas && km init --dry-run=false` on this install (the create-handler must carry the new userdata; do not use `--sidecars`).

- [ ] **Step 3: Live UAT** — recreate the Task-1 spike profile with `hibernation: true`, two volumes of different sizes, `onVolumeMismatch: refuse`:
1. First boot: `/var/lib/km/volumes.json` has both entries with `vol-` ids matching `aws ec2 describe-volumes`; `km status` shows `✓ /data`, `✓ /repos`; `grep -c fstab` shows no additional-volume line.
2. Write a known file to `/repos`, checksum it.
3. `km pause` → `stopped` → `km resume`: `/var/log/km-volumes.log` shows `pre-sleep` unmounted both and `post-sleep` re-probed both BDFs and mounted; `km resume` printed the `Volumes:` lines; checksum unchanged; `dmesg` shows no `EXT4-fs error`. Repeat the cycle 3×.
4. Simulate the failure: with the box running, `km shell --root`, edit `volumes.json` to swap the two `sizeSectors`; `systemctl restart km-volumes` → both refused at `size`, markers present, `km status` shows the ✗ lines with the fix text, `.km-mount-refused` exists in the empty dirs. Restore the file, restart, mounted again, markers gone.
5. Policy: `onVolumeMismatch: reboot` variant (second sandbox or `systemctl set-environment` on the unit): same swap, `km pause`/`km resume` → box reboots once, comes up, refused (file still swapped), does **not** reboot again. Restore.
6. `repair`: on a snapshot-derived volume, `dd if=/dev/zero of=<node> bs=4k count=1` the primary superblock, `km-volumes repair /repos` → recovers via a backup superblock, mounts.
7. Destroy both.
Record every step's output in the PR body.

- [ ] **Step 4:** push `feat/hibernate-volume-validation` with the explicit refspec, `gh pr create` with the results, merge on approval, delete the branch.
