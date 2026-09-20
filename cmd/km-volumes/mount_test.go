package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func twoVolumeManifest() *Manifest {
	return &Manifest{Version: 1, Volumes: []Volume{
		{Mountpoint: "/data", BDM: "f", VolumeID: "vol-0fed1b11de755d9aa", SizeSectors: 62914560, FSUUID: "d9082e9d", FSType: "ext4", Label: "additional volume"},
		{Mountpoint: "/repos", BDM: "g", VolumeID: "vol-0b79fa8db9f48ee7c", SizeSectors: 335544320, FSUUID: "0fea598a", FSType: "ext4", FromSnapshot: "snap-0fe5725492872309c", Label: "snapshot"},
	}}
}

func healthy() *fakeSystem {
	f := newFake()
	f.devices = []Device{
		{Node: "/dev/nvme1n1", BDF: "0000:00:1f.0", VolumeID: "vol-0fed1b11de755d9aa", LiveBytes: 62914560 * 512, KernelBytes: 62914560 * 512},
		{Node: "/dev/nvme2n1", BDF: "0000:00:1e.0", VolumeID: "vol-0b79fa8db9f48ee7c", LiveBytes: 335544320 * 512, KernelBytes: 335544320 * 512},
	}
	f.blkid = map[string][3]string{"/dev/nvme1n1": {"d9082e9d", "ext4", "/dev/nvme1n1"}, "/dev/nvme2n1": {"0fea598a", "ext4", "/dev/nvme2n1"}}
	f.ext4 = map[string][2]uint64{"/dev/nvme1n1": {7864320, 4096}, "/dev/nvme2n1": {41943040, 4096}}
	return f
}

func TestMount_HealthyMountsBothByLiveSerial(t *testing.T) {
	sys := healthy()
	events := captureAudit(t)
	// Nodes deliberately NOT in BDM order: /data's device is nvme2 by letter but the
	// serial says nvme1 — serial must win.
	st, err := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sys.mounted) != 2 || sys.mounted["/data"] != "/dev/nvme1n1" || sys.mounted["/repos"] != "/dev/nvme2n1" {
		t.Errorf("mounted = %v", sys.mounted)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "mounted" {
			t.Errorf("%s: %+v", v.Mountpoint, v)
		}
	}
	// km doctor keys on the LATEST event per mountpoint, so a success must be
	// on the stream too, not only refusals.
	want := []auditEvent{
		{Type: "volume_mounted", Detail: map[string]string{"mountpoint": "/data", "volume_id": "vol-0fed1b11de755d9aa"}},
		{Type: "volume_mounted", Detail: map[string]string{"mountpoint": "/repos", "volume_id": "vol-0b79fa8db9f48ee7c"}},
	}
	if !reflect.DeepEqual(*events, want) {
		t.Errorf("audit events = %+v, want %+v", *events, want)
	}
}

// The observed failure: serials right, sizes swapped.
func TestMount_ObservedCrossWiringRefusesBothAtSizeStep(t *testing.T) {
	sys := healthy()
	events := captureAudit(t)
	sys.devices[0].KernelBytes, sys.devices[1].KernelBytes = sys.devices[1].KernelBytes, sys.devices[0].KernelBytes
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if len(sys.mounted) != 0 {
		t.Fatalf("must mount nothing, mounted %v", sys.mounted)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "refused" || v.Step != "size" {
			t.Errorf("%s: %+v", v.Mountpoint, v)
		}
	}
	if !sys.Exists("/repos/.km-mount-refused") || !sys.Exists("/data/.km-mount-refused") {
		t.Error("markers not written")
	}
	if len(sys.reboots) != 0 {
		t.Error("refuse policy must not reboot")
	}
	if len(*events) != 2 {
		t.Fatalf("audit events = %+v", *events)
	}
	for _, e := range *events {
		if e.Type != "volume_mount_refused" || e.Detail["step"] != "size" || e.Detail["policy"] != "refuse" || e.Detail["volume_id"] == "" {
			t.Errorf("audit event = %+v", e)
		}
		for _, k := range []string{"mountpoint", "volume_id", "step", "expected", "actual", "reason", "policy"} {
			if _, ok := e.Detail[k]; !ok {
				t.Errorf("audit detail missing %q: %+v", k, e.Detail)
			}
		}
	}
}

func TestMount_UUIDMismatchRefusesOnlyThatEntry(t *testing.T) {
	sys := healthy()
	sys.blkid["/dev/nvme2n1"] = [3]string{"d9082e9d", "ext4", "/dev/nvme2n1"} // /repos node carries /data's superblock
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if sys.mounted["/data"] == "" || sys.mounted["/repos"] != "" {
		t.Errorf("mounted = %v", sys.mounted)
	}
	if st.Volumes[1].Outcome != "refused" || st.Volumes[1].Step != "fsuuid" {
		t.Errorf("%+v", st.Volumes[1])
	}
}

func TestMount_AbsentAndAmbiguous(t *testing.T) {
	sys := healthy()
	sys.devices = sys.devices[:1] // /repos volume gone
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[1].Outcome != "absent" {
		t.Errorf("%+v", st.Volumes[1])
	}
	sys = healthy()
	sys.devices = append(sys.devices, sys.devices[1]) // two nodes claim /repos's serial
	st, _ = runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[1].Outcome != "ambiguous" || sys.mounted["/repos"] != "" {
		t.Errorf("%+v %v", st.Volumes[1], sys.mounted)
	}
}

func TestMount_SuperblockLargerThanDeviceRefuses(t *testing.T) {
	sys := healthy()
	sys.ext4["/dev/nvme1n1"] = [2]uint64{41943040, 4096} // 160G superblock on the 30G device
	st, _ := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if st.Volumes[0].Outcome != "refused" || st.Volumes[0].Step != "superblock" {
		t.Errorf("%+v", st.Volumes[0])
	}
}

func TestMount_SuccessRemovesMarker(t *testing.T) {
	sys := healthy()
	sys.files["/repos/.km-mount-refused"] = true
	runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if sys.Exists("/repos/.km-mount-refused") {
		t.Error("marker must be removed before a successful mount")
	}
}

func TestMount_NoManifestFallsBackToBDMAndRecordsUnvalidated(t *testing.T) {
	sys := healthy()
	sys.bdm = map[string]string{"f": "/dev/nvme1n1", "g": "/dev/nvme2n1"}
	st, _ := runMount(context.Background(), sys, nil, MountOpts{Policy: "refuse", Fallback: []FallbackEntry{{Mountpoint: "/data", BDM: "f"}, {Mountpoint: "/repos", BDM: "g"}}})
	if len(sys.mounted) != 2 {
		t.Errorf("mounted = %v", sys.mounted)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "unvalidated" {
			t.Errorf("%+v", v)
		}
	}
}

func TestMount_WritesStateFile(t *testing.T) {
	sys := healthy()
	if _, err := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"}); err != nil {
		t.Fatal(err)
	}
	st, err := readState(sys)
	if err != nil || len(st.Volumes) != 2 {
		t.Fatalf("readState = %+v, %v", st, err)
	}
}

func TestMount_EnumerationFailureRefusesAllLoudly(t *testing.T) {
	sys := healthy()
	sys.listErr = errors.New("findmnt: root source unknown")
	st, err := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if err == nil {
		t.Fatal("must surface the enumeration error")
	}
	if len(sys.mounted) != 0 {
		t.Errorf("mounted = %v", sys.mounted)
	}
	if len(st.Volumes) != 2 {
		t.Fatalf("state = %+v", st)
	}
	for _, v := range st.Volumes {
		if v.Outcome != "refused" || v.Step != "enumerate" {
			t.Errorf("%+v", v)
		}
	}
	if !sys.Exists("/data/.km-mount-refused") || !sys.Exists(statePath) {
		t.Error("marker and state must still be written")
	}
}

// A first boot mounts directly (the unit is enabled after the volume block);
// the unit then runs mount on that same boot. An entry already mounted FROM
// THE VALIDATED SOURCE is simply "mounted" — never a second mount call, never a
// refusal — and one mounted from anything else is refused, because a mount km
// did not make is exactly the situation this exists to catch.
func TestMount_AlreadyMountedFromValidatedSourceIsIdempotent(t *testing.T) {
	sys := healthy()
	sys.mounted["/data"] = "/dev/nvme1n1"  // right device
	sys.mounted["/repos"] = "/dev/nvme1n1" // WRONG device for /repos
	st, err := runMount(context.Background(), sys, twoVolumeManifest(), MountOpts{Policy: "refuse"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Volumes[0].Outcome != OutcomeMounted {
		t.Errorf("/data already mounted from its validated source must read mounted: %+v", st.Volumes[0])
	}
	if sys.mountCalls != 0 {
		t.Errorf("no mount call may be issued for an already-mounted entry, got %d", sys.mountCalls)
	}
	if st.Volumes[1].Outcome != OutcomeRefused || st.Volumes[1].Step != "mounted-elsewhere" {
		t.Errorf("/repos mounted from the wrong device must be refused at mounted-elsewhere: %+v", st.Volumes[1])
	}
}
