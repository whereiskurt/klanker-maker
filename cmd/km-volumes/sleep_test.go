package main

import (
	"context"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// Production values, captured before the test overrides below so a guard test
// can still see them.
var prodSettleBudget, prodBindBudget = settleBudget, bindBudget

func init() {
	holderGrace = 0 // the real 1s grace between kill and retry is not under test
	settleInterval, bindInterval = 0, 0
	settleBudget, bindBudget = 20*time.Millisecond, 20*time.Millisecond
}

// post-sleep now runs under its unit's TimeoutStartSec=240; the budgets plus
// the bounded re-probe waits must land well inside it.
func TestPostSleep_BudgetsFitTheUnitTimeout(t *testing.T) {
	if sum := prodSettleBudget + prodBindBudget; sum > 150*time.Second {
		t.Errorf("settleBudget+bindBudget = %s, must leave room under TimeoutStartSec=240s", sum)
	}
}

func TestPreSleep_CleanUnmount(t *testing.T) {
	sys := healthy()
	sys.mounted = map[string]string{"/data": "/dev/nvme1n1", "/repos": "/dev/nvme2n1"}
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.unmounted) != 2 || len(sys.kills) != 0 {
		t.Errorf("unmounted=%v kills=%v", sys.unmounted, sys.kills)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "unmounted" {
			t.Errorf("%+v", v)
		}
	}
	if sys.synced != 1 {
		t.Errorf("synced %d times, want 1", sys.synced)
	}
}

func TestPreSleep_BusyEscalatesTERMThenKILLThenLazy(t *testing.T) {
	sys := healthy()
	sys.mounted = map[string]string{"/repos": "/dev/nvme2n1"}
	sys.busy["/repos"] = 3 // first three umount attempts return EBUSY
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	wantSigs := []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	if !reflect.DeepEqual(sys.kills["/repos"], wantSigs) {
		t.Errorf("kills = %v", sys.kills["/repos"])
	}
	if !sys.lazyUnmounts["/repos"] {
		t.Error("must fall back to umount -l after KILL")
	}
	if st.Volumes[1].Outcome != "lazy" {
		t.Errorf("%+v", st.Volumes[1])
	}
}

func TestPreSleep_BusyClearsAfterTERM(t *testing.T) {
	sys := healthy()
	sys.mounted = map[string]string{"/repos": "/dev/nvme2n1"}
	sys.busy["/repos"] = 1
	runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.kills["/repos"]) != 1 || sys.lazyUnmounts["/repos"] {
		t.Errorf("kills=%v lazy=%v", sys.kills, sys.lazyUnmounts)
	}
}

func TestPreSleep_NotMountedIsNoop(t *testing.T) {
	sys := healthy()
	st := runPreSleep(context.Background(), sys, twoVolumeManifest())
	if len(sys.unmounted) != 0 {
		t.Error("nothing to unmount")
	}
	for _, v := range st.Volumes {
		if v.Outcome != "not-mounted" {
			t.Errorf("%+v", v)
		}
	}
}

func TestPreSleep_NilManifestIsNoop(t *testing.T) {
	sys := healthy()
	runPreSleep(context.Background(), sys, nil)
	if len(sys.unmounted) != 0 {
		t.Error("no manifest → touch nothing")
	}
}

func TestPostSleep_ReprobesEveryNonRootControllerThenMounts(t *testing.T) {
	sys := healthy()
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if !reflect.DeepEqual(sys.reprobed, []string{"0000:00:1f.0", "0000:00:1e.0"}) {
		t.Errorf("reprobed = %v", sys.reprobed)
	}
	if len(sys.mounted) != 2 {
		t.Errorf("mounted = %v", sys.mounted)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "mounted" {
			t.Errorf("%+v", v)
		}
	}
}

func TestPostSleep_MismatchReprobesOnceMoreThenRefuses(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 62914560 * 512          // /repos stale until the second reprobe
	sys.fixOnReprobe = map[string]int{"0000:00:1e.0": 2} // fake: after the 2nd reprobe of this BDF, KernelBytes becomes correct
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if st.Volumes[1].Outcome != "mounted" {
		t.Errorf("second reprobe should have healed it: %+v", st.Volumes[1])
	}
	if n := count(sys.reprobed, "0000:00:1e.0"); n != 2 {
		t.Errorf("reprobed /repos %d times, want 2", n)
	}
	if n := count(sys.reprobed, "0000:00:1f.0"); n != 1 {
		t.Errorf("reprobed the healthy /data controller %d times, want 1", n)
	}
}

// Spike §13.4: node names AND the BDF↔node binding move across a re-probe, so
// the retry must locate the refused volume's controller from a FRESH listing.
func TestPostSleep_RetryUsesPostReprobeBinding(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 1 // /repos never heals
	sys.swapBDFOnFirstReprobe = true
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	// After the first re-probe /repos's volume sits behind 1f.0, so the retry
	// must hit 1f.0 — the pre-re-probe list would have said 1e.0.
	want := []string{"0000:00:1f.0", "0000:00:1e.0", "0000:00:1f.0"}
	if !reflect.DeepEqual(sys.reprobed, want) {
		t.Errorf("reprobed = %v, want %v", sys.reprobed, want)
	}
}

func TestPostSleep_RebootPolicyRebootsOncePerResume(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 1 // never heals
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 1 || st.Volumes[1].Outcome != "rebooting" {
		t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1])
	}
	if st.Volumes[0].Outcome != "mounted" {
		t.Errorf("a validating volume must keep its outcome: %+v", st.Volumes[0])
	}
	// Same resume id again (the guard file persisted) → refuse, no second reboot.
	sys.reboots = nil
	st = runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 0 || st.Volumes[1].Outcome != "refused" {
		t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1])
	}
	// A different resume id (a later hibernate cycle) may reboot again.
	st = runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-2")
	if len(sys.reboots) != 1 || st.Volumes[1].Outcome != "rebooting" {
		t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1])
	}
}

func TestPostSleep_RefusePolicyNeverReboots(t *testing.T) {
	sys := healthy()
	sys.devices[1].KernelBytes = 1
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if len(sys.reboots) != 0 {
		t.Error("refuse must not reboot")
	}
}

func TestPostSleep_NeverReprobesRoot(t *testing.T) {
	// ListDevices already excludes root; assert the fake's root BDF never appears.
	sys := healthy()
	sys.rootBDF = "0000:00:04.0"
	sys.devices = append(sys.devices, Device{Node: "/dev/nvme0n1", BDF: sys.rootBDF, VolumeID: "vol-root", LiveBytes: 8 << 30, KernelBytes: 8 << 30})
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	for _, b := range sys.reprobed {
		if b == sys.rootBDF {
			t.Fatal("root controller re-probed")
		}
	}
	if len(sys.reprobed) != 2 {
		t.Errorf("reprobed = %v", sys.reprobed)
	}
}

func TestPostSleep_NilManifestIsNoop(t *testing.T) {
	sys := healthy()
	runPostSleep(context.Background(), sys, nil, "reboot", "boot-1")
	if len(sys.reprobed) != 0 || len(sys.reboots) != 0 {
		t.Errorf("no manifest → touch nothing: reprobed=%v reboots=%v", sys.reprobed, sys.reboots)
	}
}

// Live UAT: the unconditional re-probe hit a controller the hypervisor had not
// finished restoring; the nvme probe failed silently and left the PCI function
// on the bus with no driver. A plain rescan never revisits it — only removing
// THAT function again and rescanning binds it.
func TestPostSleep_UnboundControllerIsReprobedUntilBound(t *testing.T) {
	sys := healthy()
	sys.absentUntilReprobes["0000:00:1e.0"] = 3 // the unconditional pass + two more
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if len(sys.mounted) != 2 {
		t.Errorf("mounted = %v", sys.mounted)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "mounted" {
			t.Errorf("%+v", v)
		}
	}
	if n := count(sys.reprobed, "0000:00:1e.0"); n < 3 {
		t.Errorf("reprobed 1e.0 %d times, want >= 3", n)
	}
}

func TestPostSleep_SettlesBeforeReprobing(t *testing.T) {
	sys := healthy()
	// The hypervisor is still restoring: the first two listings see one device.
	sys.listSequence = [][]Device{{sys.devices[0]}, {sys.devices[0]}}
	runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if len(sys.reprobed) == 0 {
		t.Fatal("expected the unconditional re-probe to run")
	}
	if sys.fullListsAtReprobe[0] < 1 {
		t.Errorf("first Reprobe happened after %d full listings; must settle first", sys.fullListsAtReprobe[0])
	}
	if len(sys.mounted) != 2 {
		t.Errorf("mounted = %v", sys.mounted)
	}
}

func TestPostSleep_BindBudgetExhaustedRefusesAbsent(t *testing.T) {
	sys := healthy()
	sys.absentUntilReprobes["0000:00:1e.0"] = 1 << 30 // never binds
	st := runPostSleep(context.Background(), sys, twoVolumeManifest(), "refuse", "boot-1")
	if st.Volumes[1].Outcome != "absent" || sys.mounted["/repos"] != "" {
		t.Errorf("%+v mounted=%v", st.Volumes[1], sys.mounted)
	}
	if st.Volumes[0].Outcome != "mounted" {
		t.Errorf("the bound volume must still mount: %+v", st.Volumes[0])
	}
	if len(sys.reboots) != 0 {
		t.Error("refuse must not reboot")
	}

	sys = healthy()
	sys.absentUntilReprobes["0000:00:1e.0"] = 1 << 30
	st = runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 1 || st.Volumes[1].Outcome != "rebooting" {
		t.Errorf("reboots=%v state=%+v", sys.reboots, st.Volumes[1])
	}
	sys.reboots = nil
	st = runPostSleep(context.Background(), sys, twoVolumeManifest(), "reboot", "boot-1")
	if len(sys.reboots) != 0 || st.Volumes[1].Outcome != "absent" {
		t.Errorf("second pass on the same resume: reboots=%v state=%+v", sys.reboots, st.Volumes[1])
	}
}
