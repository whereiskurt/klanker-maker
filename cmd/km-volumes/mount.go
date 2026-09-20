package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

type MountOpts struct {
	Policy     string          // "refuse" | "reboot"; only post-sleep acts on it
	FromResume bool            // only post-sleep sets this; reboot policy needs it
	Fallback   []FallbackEntry // AdditionalVolumeMounts from the unit's args, for a pre-manifest box
}

// FallbackEntry is one --fallback mp:letter argument: the only place a BDM
// letter is still used to find a device, and only when there is no manifest.
type FallbackEntry struct{ Mountpoint, BDM string }

// runMount is spec §5.1: the only path that ever mounts an additional volume.
// Each manifest entry is validated independently — the first failing step
// refuses that entry only — and the outcome of every entry is written to the
// state file whether or not anything mounted.
func runMount(ctx context.Context, sys System, m *Manifest, opts MountOpts) (State, error) {
	st := State{UpdatedAt: time.Now().UTC()}
	if m == nil {
		// Pre-manifest box: behave exactly as the fstab lines did, and say so.
		for _, f := range opts.Fallback {
			vs := VolumeState{Mountpoint: f.Mountpoint, Outcome: OutcomeUnvalidated, Reason: "no manifest — created before km-volumes; recreate to protect", At: st.UpdatedAt}
			if node, err := sys.ResolveBDM(f.BDM); err == nil {
				if uuid, fstype, src, err := sys.Blkid(node); err == nil && uuid != "" {
					if err := sys.Mount(src, f.Mountpoint, fstype); err != nil {
						vs.Outcome, vs.Step, vs.Reason = OutcomeRefused, "mount", "mount: "+err.Error()
					}
				} else {
					vs.Reason = "no manifest and no filesystem UUID on " + node
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
		// Enumeration failing (no root device found, sysfs unreadable) refuses
		// everything, loudly: nothing is mounted, and the state says why rather
		// than going empty.
		for _, v := range m.Volumes {
			vs := VolumeState{Mountpoint: v.Mountpoint, VolumeID: v.VolumeID, Outcome: OutcomeRefused, Step: "enumerate", Reason: "cannot enumerate NVMe devices: " + err.Error(), At: st.UpdatedAt}
			_ = sys.WriteFile(filepath.Join(v.Mountpoint, markerName), []byte(vs.Reason+"\n"), 0o644)
			emitAuditRefused(v, vs, opts.Policy)
			st.Volumes = append(st.Volumes, vs)
		}
		_ = writeState(sys, st)
		return st, err
	}
	for _, v := range m.Volumes {
		vs := validateOne(sys, devs, v)
		marker := filepath.Join(v.Mountpoint, markerName)
		if vs.Outcome == outcomeOK {
			switch cur := sys.MountSource(v.Mountpoint); {
			case cur == vs.source:
				// Already mounted from the validated device (the first boot
				// mounts directly, then the unit runs on that same boot; a
				// systemctl restart on a live box lands here too). Idempotent.
				vs.Outcome = OutcomeMounted
				emitAuditMounted(v)
			case cur != "":
				// Mounted, but not by us from this device — precisely the
				// situation validation exists to catch. Never remount over it.
				vs.Outcome, vs.Step = OutcomeRefused, "mounted-elsewhere"
				vs.Reason = fmt.Sprintf("%s is mounted from %s, but the validated device for %s is %s", v.Mountpoint, cur, v.VolumeID, vs.source)
			default:
				_ = sys.Remove(marker) // must go before the mount hides the underlying directory
				if err := sys.Mount(vs.source, v.Mountpoint, v.FSType); err != nil {
					vs.Outcome, vs.Step, vs.Reason = OutcomeRefused, "mount", err.Error()
				} else {
					vs.Outcome = OutcomeMounted
					emitAuditMounted(v)
				}
			}
		}
		switch vs.Outcome {
		case OutcomeRefused, OutcomeAbsent, OutcomeAmbiguous:
			_ = sys.WriteFile(marker, []byte(vs.Reason+"\n"), 0o644)
			emitAuditRefused(v, vs, opts.Policy)
		}
		st.Volumes = append(st.Volumes, vs)
	}
	return st, writeState(sys, st)
}

// validateOne is the spec §5.1 ladder: serial → size → fsuuid → superblock.
// Devices are matched by LIVE serial only; the UUID is checked against the
// identity, never used to find it — the inversion of what fstab did.
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
		vs.Outcome, vs.Step, vs.Reason = OutcomeAbsent, "serial", "no attached NVMe device reports serial "+v.VolumeID
		return vs
	case len(match) > 1:
		vs.Outcome, vs.Step, vs.Reason = OutcomeAmbiguous, "serial", fmt.Sprintf("%d devices report serial %s", len(match), v.VolumeID)
		return vs
	}
	d := match[0]
	want := v.SizeSectors * 512
	if d.LiveBytes != want || d.KernelBytes != want {
		vs.Outcome, vs.Step = OutcomeRefused, "size"
		vs.Expected, vs.Actual = fmt.Sprintf("%d", want), fmt.Sprintf("live=%d kernel=%d", d.LiveBytes, d.KernelBytes)
		vs.Reason = fmt.Sprintf("size: live %s, kernel %s, expected %s", human(d.LiveBytes), human(d.KernelBytes), human(want))
		return vs
	}
	uuid, fstype, src, err := sys.Blkid(d.Node)
	if err != nil || uuid != v.FSUUID {
		vs.Outcome, vs.Step, vs.Expected, vs.Actual = OutcomeRefused, "fsuuid", v.FSUUID, uuid
		vs.Reason = fmt.Sprintf("filesystem UUID on %s is %q, expected %q", d.Node, uuid, v.FSUUID)
		return vs
	}
	if fstype == "ext4" {
		// Belt: a superblock cross-written from a larger volume onto a smaller one.
		if count, bs, err := sys.Ext4BlockCount(src); err == nil && count*bs > d.LiveBytes {
			vs.Outcome, vs.Step = OutcomeRefused, "superblock"
			vs.Expected, vs.Actual = fmt.Sprintf("<=%d", d.LiveBytes), fmt.Sprintf("%d", count*bs)
			vs.Reason = fmt.Sprintf("ext4 superblock describes %s but the device is %s", human(count*bs), human(d.LiveBytes))
			return vs
		}
	}
	vs.Outcome, vs.source = outcomeOK, src
	return vs
}

// human renders bytes as GiB (or MiB below 1 GiB) for the reason strings an
// operator reads in km status.
func human(b uint64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	switch {
	case b >= gib && b%gib == 0:
		return fmt.Sprintf("%d GiB", b/gib)
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/mib)
	}
	return fmt.Sprintf("%d B", b)
}
