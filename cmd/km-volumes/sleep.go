package main

import (
	"context"
	"fmt"
	"os"
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

// post-sleep budgets. The verb runs under km-volumes-resume.service
// (TimeoutStartSec=240); settle + bind plus the bounded per-controller
// re-probe waits (reprobeWait each) must land well inside that.
// TestPostSleep_BudgetsFitTheUnitTimeout pins the sum.
var (
	settleInterval = 2 * time.Second
	settleBudget   = 60 * time.Second
	bindInterval   = 2 * time.Second
	bindBudget     = 60 * time.Second
)

// runPostSleep is spec §5.4 as revised after live UAT: settle, re-probe,
// bind, then validate and apply policy.
//
//   - SETTLE: the hypervisor is still restoring the EBS PCI functions when the
//     hook fires. Re-probing a function before it is responsive makes the nvme
//     probe fail silently and leaves it on the bus driverless. So first poll
//     ListDevices until every manifest vol-id is live AND the node count has
//     been stable for two polls (bounded; proceed anyway if it never settles).
//   - RE-PROBE every non-root controller unconditionally — the from-scratch
//     probe the spike showed to be the known-good state — then list again:
//     node names and the BDF↔node binding move across a re-probe (spike §13.4).
//   - BIND: while any manifest vol-id has no device, re-probe each EBS function
//     that is present with no driver (a bare rescan never revisits one), or a
//     bare rescan when none is unbound; bounded.
//   - MOUNT, one retry on an identity/size refusal, then policy — the reboot
//     policy fires only after BIND has given up.
func runPostSleep(ctx context.Context, sys System, m *Manifest, policy, resumeID string) State {
	if m == nil {
		return State{UpdatedAt: time.Now().UTC()}
	}
	want := manifestVolumeIDs(m)

	if !settle(ctx, sys, want) {
		fmt.Fprintf(os.Stderr, "km-volumes post-sleep: devices did not settle within %s; proceeding\n", settleBudget)
	}
	if devs, err := sys.ListDevices(ctx); err == nil {
		for _, d := range devs {
			_ = sys.Reprobe(d.BDF) // bounded inside System; a failure just leaves the old node
		}
	}
	if !bind(ctx, sys, want) {
		fmt.Fprintf(os.Stderr, "km-volumes post-sleep: not every manifest volume bound within %s; proceeding\n", bindBudget)
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

// settle polls until the live set covers every manifest vol-id and the node
// count has held for two consecutive polls. Reports false on budget exhaustion.
func settle(ctx context.Context, sys System, want map[string]bool) bool {
	deadline := time.Now().Add(settleBudget)
	prevCount, stable := -1, 0
	for {
		devs, err := sys.ListDevices(ctx)
		if err == nil {
			if len(devs) == prevCount {
				stable++
			} else {
				prevCount, stable = len(devs), 1
			}
			if stable >= 2 && covers(devs, want) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(settleInterval)
	}
}

// bind re-probes driverless EBS functions until every manifest vol-id is live
// or the budget is spent. Reports whether it converged.
func bind(ctx context.Context, sys System, want map[string]bool) bool {
	deadline := time.Now().Add(bindBudget)
	for {
		devs, err := sys.ListDevices(ctx)
		if err == nil && covers(devs, want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		bdfs, _ := sys.UnboundEBSControllers()
		if len(bdfs) == 0 {
			_ = sys.Rescan()
		}
		for _, b := range bdfs {
			_ = sys.Reprobe(b)
		}
		time.Sleep(bindInterval)
	}
}

func manifestVolumeIDs(m *Manifest) map[string]bool {
	want := make(map[string]bool, len(m.Volumes))
	for _, v := range m.Volumes {
		want[v.VolumeID] = true
	}
	return want
}

// covers is true when every wanted vol-id is reported by some live device.
func covers(devs []Device, want map[string]bool) bool {
	seen := make(map[string]bool, len(devs))
	for _, d := range devs {
		seen[d.VolumeID] = true
	}
	for id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
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
