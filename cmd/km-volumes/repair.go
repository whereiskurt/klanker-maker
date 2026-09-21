package main

import (
	"context"
	"fmt"
	"os"
)

// runRepair is spec §5.7: operator-invoked, never automatic. For a volume the
// last mount refused, try e2fsck against each backup superblock mke2fs -n
// would list until one validates, then run mount again. A volume that is not
// snapshot-derived is the only copy, so it needs --i-accept-data-loss; a
// volume that currently validates is never touched; and mkfs is never run.
func runRepair(ctx context.Context, sys System, m *Manifest, mountpoint string, acceptDataLoss bool) error {
	if m == nil {
		return fmt.Errorf("repair: no manifest at %s — this box predates km-volumes; recreate it", manifestPath)
	}
	var vol *Volume
	for i := range m.Volumes {
		if m.Volumes[i].Mountpoint == mountpoint {
			vol = &m.Volumes[i]
		}
	}
	if vol == nil {
		return fmt.Errorf("repair: %s is not in the manifest", mountpoint)
	}
	st, err := readState(sys)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	var last *VolumeState
	for i := range st.Volumes {
		if st.Volumes[i].Mountpoint == mountpoint {
			last = &st.Volumes[i]
		}
	}
	if last == nil {
		return fmt.Errorf("repair: %s has no recorded outcome; run `km-volumes mount` first", mountpoint)
	}
	switch last.Outcome {
	case OutcomeRefused, OutcomeAbsent, OutcomeAmbiguous:
	default:
		return fmt.Errorf("repair: %s last outcome is %q, not refused — refusing to touch a volume that validates", mountpoint, last.Outcome)
	}
	if vol.FromSnapshot == "" && !acceptDataLoss {
		return fmt.Errorf("repair: %s (%s) is not snapshot-derived — it is the only copy; re-run with --i-accept-data-loss to fsck it anyway", mountpoint, vol.VolumeID)
	}

	devs, err := sys.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	var node string
	for _, d := range devs {
		if d.VolumeID == vol.VolumeID {
			if node != "" {
				return fmt.Errorf("repair: more than one device reports serial %s; cannot pick one", vol.VolumeID)
			}
			node = d.Node
		}
	}
	if node == "" {
		return fmt.Errorf("repair: no attached NVMe device reports serial %s", vol.VolumeID)
	}
	// fsck the mount source (a partition when the snapshot carried a partition
	// table); when blkid cannot read the superblock at all, fall back to the node.
	target := node
	if _, _, src, err := sys.Blkid(node); err == nil && src != "" {
		target = src
	}
	sbs, err := sys.BackupSuperblocks(target)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	foreign := 0
	for _, sb := range sbs {
		// Never e2fsck against a backup that belongs to another filesystem:
		// after a cross-write every backup on a volume may carry the OTHER
		// volume's superblock (proven live), e2fsck -b then aborts having
		// "MODIFIED" the primary, or — if the foreign geometry happens to
		// fit — rewrites the primary with the wrong filesystem entirely.
		if u, err := sys.SuperblockUUID(target, sb); err != nil || u != vol.FSUUID {
			fmt.Fprintf(os.Stderr, "km-volumes repair: backup superblock %d on %s carries filesystem %q, not %s — skipped\n", sb, target, u, vol.FSUUID)
			foreign++
			continue
		}
		fmt.Fprintf(os.Stderr, "km-volumes repair: e2fsck -f -y -b %d %s\n", sb, target)
		if err := sys.Fsck(target, sb); err != nil {
			fmt.Fprintf(os.Stderr, "km-volumes repair:   %v\n", err)
			continue
		}
		fmt.Fprintf(os.Stderr, "km-volumes repair: backup superblock %d validated; re-running mount\n", sb)
		st, err := runMount(ctx, sys, m, MountOpts{Policy: "refuse"})
		if err != nil {
			return fmt.Errorf("repair: mount after fsck: %w", err)
		}
		for _, vs := range st.Volumes {
			if vs.Mountpoint == mountpoint && vs.Outcome != OutcomeMounted {
				return fmt.Errorf("repair: fsck succeeded but mount still %s at %s: %s", vs.Outcome, vs.Step, vs.Reason)
			}
		}
		return nil
	}
	if foreign == len(sbs) {
		return fmt.Errorf("repair: %d backup superblock(s) on %s examined, none carries filesystem %s (the cross-write reached every backup); "+
			"re-materialise the volume — see docs/hibernate-volumes.md § Re-materialise from snapshot", len(sbs), target, vol.FSUUID)
	}
	return fmt.Errorf("repair: no backup superblock on %s validated (%d tried, %d foreign); re-materialise the volume: "+
		"see docs/hibernate-volumes.md § Re-materialise from snapshot", target, len(sbs), foreign)
}
