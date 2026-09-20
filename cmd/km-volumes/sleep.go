package main

import (
	"context"
	"syscall"
	"time"
)

// holderGrace is the pause between killing a mountpoint's holders and the
// next umount attempt; a var so tests can zero it.
var holderGrace = time.Second

// runPreSleep is spec §5.3. Every step is bounded inside System; nothing here
// may block the systemd transition (addendum §C), so the ladder is fixed-length
// and the outcome — including a lazy detach — is recorded rather than failed.
// umount -l is never the first choice: a lazily-detached filesystem stays alive
// for its open fds, which is exactly the stale-state-on-a-swapped-node case;
// when it is the last resort, post-sleep's re-probe pulls the PCI device from
// under those fds so they get EIO rather than a wrong disk.
func runPreSleep(ctx context.Context, sys System, m *Manifest) State {
	st := State{UpdatedAt: time.Now().UTC()}
	if m == nil {
		return st
	}
	sys.Sync()
	for _, v := range m.Volumes {
		vs := VolumeState{Mountpoint: v.Mountpoint, VolumeID: v.VolumeID, At: time.Now().UTC()}
		if !sys.IsMounted(v.Mountpoint) {
			vs.Outcome = OutcomeNotMounted
			st.Volumes = append(st.Volumes, vs)
			continue
		}
		vs.Outcome = OutcomeUnmounted
		if err := sys.Unmount(v.Mountpoint, false); err != nil {
			_ = sys.KillHolders(v.Mountpoint, syscall.SIGTERM)
			time.Sleep(holderGrace)
			if err := sys.Unmount(v.Mountpoint, false); err != nil {
				_ = sys.KillHolders(v.Mountpoint, syscall.SIGKILL)
				time.Sleep(holderGrace)
				if err := sys.Unmount(v.Mountpoint, false); err != nil {
					_ = sys.Unmount(v.Mountpoint, true) // last resort; post-sleep's re-probe EIOs any survivor
					vs.Outcome, vs.Reason = OutcomeLazy, "holders survived SIGKILL; detached lazily"
				}
			}
		}
		st.Volumes = append(st.Volumes, vs)
	}
	_ = writeState(sys, st)
	return st
}

// runPostSleep is spec §5.4: from-scratch probe first, validation second,
// policy last. The re-probe is unconditional because the spike showed a fresh
// probe (what a reboot gives) is the known-good state, and detecting staleness
// is the part we trust least. Every device list is taken FRESH after a
// re-probe: node names and the BDF↔node binding both move (spike §13.4), so a
// list taken before the probe cannot be used to find a controller after it.
func runPostSleep(ctx context.Context, sys System, m *Manifest, policy, resumeID string) State {
	if m == nil {
		return State{UpdatedAt: time.Now().UTC()}
	}
	if devs, err := sys.ListDevices(ctx); err == nil {
		for _, d := range devs {
			_ = sys.Reprobe(d.BDF) // bounded inside System; a failure just leaves the old node
		}
	}
	st, _ := runMount(ctx, sys, m, MountOpts{Policy: policy, FromResume: true})

	// One more re-probe for any entry that refused on identity or size, located
	// in a post-re-probe listing.
	retried := false
	if needsRetry(st) {
		devs, err := sys.ListDevices(ctx)
		if err == nil {
			for _, vs := range st.Volumes {
				if !identityOrSizeRefusal(vs) {
					continue
				}
				if bdf := bdfFor(devs, vs.VolumeID); bdf != "" {
					_ = sys.Reprobe(bdf)
					retried = true
				}
			}
		}
	}
	if retried {
		st, _ = runMount(ctx, sys, m, MountOpts{Policy: policy, FromResume: true})
	}

	if policy == "reboot" && anyRefused(st) && !rebootedFor(sys, resumeID) {
		markRebootedFor(sys, resumeID)
		st.RebootedForResume = resumeID
		for i := range st.Volumes {
			if st.Volumes[i].Outcome != OutcomeMounted {
				st.Volumes[i].Outcome = OutcomeRebooting
			}
		}
		_ = writeState(sys, st)
		emitAuditReboot(st, resumeID)
		_ = sys.Reboot()
		return st
	}
	_ = writeState(sys, st)
	return st
}

// identityOrSizeRefusal is the retry criterion: the refusals a fresh probe
// could plausibly fix. A wrong filesystem UUID or an oversized superblock is
// on-disk state and no re-probe changes it.
func identityOrSizeRefusal(vs VolumeState) bool {
	return vs.Outcome == OutcomeAbsent || (vs.Outcome == OutcomeRefused && (vs.Step == "serial" || vs.Step == "size"))
}

func needsRetry(st State) bool {
	for _, vs := range st.Volumes {
		if identityOrSizeRefusal(vs) {
			return true
		}
	}
	return false
}

// bdfFor finds the controller currently behind volumeID, or "" when no
// (or more than one) device reports it.
func bdfFor(devs []Device, volumeID string) string {
	bdf := ""
	for _, d := range devs {
		if d.VolumeID == volumeID {
			if bdf != "" {
				return ""
			}
			bdf = d.BDF
		}
	}
	return bdf
}
