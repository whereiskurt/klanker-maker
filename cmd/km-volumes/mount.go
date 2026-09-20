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
	results := make([]VolumeState, len(m.Volumes))
	for i, v := range m.Volumes {
		results[i] = validateOne(sys, devs, v)
	}
	if hasOutcome(results, OutcomeAbsent) {
		healAbsent(ctx, sys, m, results)
	}
	for i, v := range m.Volumes {
		vs := results[i]
		marker := filepath.Join(v.Mountpoint, markerName)
		if vs.Outcome == outcomeOK {
			// A first boot mounts directly and the unit then runs on that same
			// boot: an entry already mounted FROM THE VALIDATED SOURCE is
			// simply mounted, never a second mount call. Anything else on the
			// mountpoint is a mount km did not make — exactly what this exists
			// to catch — and is refused.
			switch cur := sys.MountSource(v.Mountpoint); cur {
			case "":
				_ = sys.Remove(marker) // must go before the mount hides the underlying directory
				if err := sys.Mount(vs.source, v.Mountpoint, v.FSType); err != nil {
					vs.Outcome, vs.Step, vs.Reason = OutcomeRefused, "mount", err.Error()
				} else {
					vs.Outcome = OutcomeMounted
					emitAuditMounted(v)
				}
			case vs.source:
				vs.Outcome = OutcomeMounted
				emitAuditMounted(v)
			default:
				vs.Outcome, vs.Step, vs.Expected, vs.Actual = OutcomeRefused, "mounted-elsewhere", vs.source, cur
				vs.Reason = fmt.Sprintf("%s is already mounted from %s, not the validated %s", v.Mountpoint, cur, vs.source)
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

// healAbsent is the ONE self-heal pass for entries no live device reports
// (live UAT, spec §5.4 revised): a probe that raced the hypervisor leaves the
// PCI function on the bus with no driver, and a bare rescan never revisits a
// present function — so re-probe each unbound EBS function (or, when none is
// unbound, do the bare rescan for a function that fell off the bus entirely),
// re-list, and re-validate only the absent entries. Fixed-length by
// construction: it runs once per runMount, whatever the outcome, so cold boot
// and `systemctl restart km-volumes` recover without a loop.
func healAbsent(ctx context.Context, sys System, m *Manifest, results []VolumeState) {
	bdfs, _ := sys.UnboundEBSControllers()
	if len(bdfs) == 0 {
		_ = sys.Rescan()
	}
	for _, b := range bdfs {
		_ = sys.Reprobe(b)
	}
	devs, err := sys.ListDevices(ctx)
	if err != nil {
		return
	}
	for i, v := range m.Volumes {
		if results[i].Outcome == OutcomeAbsent {
			results[i] = validateOne(sys, devs, v)
		}
	}
}

func hasOutcome(results []VolumeState, o Outcome) bool {
	for _, r := range results {
		if r.Outcome == o {
			return true
		}
	}
	return false
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
