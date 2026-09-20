package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestRepair_RefusesNonSnapshotWithoutFlag(t *testing.T) {
	sys := healthy()
	sys.state = State{Volumes: []VolumeState{{Mountpoint: "/data", Outcome: "refused"}}}
	err := runRepair(context.Background(), sys, twoVolumeManifest(), "/data", false)
	if err == nil || !strings.Contains(err.Error(), "--i-accept-data-loss") {
		t.Errorf("err = %v", err)
	}
	if len(sys.fscks) != 0 {
		t.Error("must not touch the volume")
	}
}

func TestRepair_TriesBackupSuperblocksUntilOneValidates(t *testing.T) {
	sys := healthy()
	sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "refused"}}}
	sys.backupSuperblocks["/dev/nvme2n1"] = []uint64{32768, 98304, 11239424}
	sys.fsckSucceedsAt = map[string]uint64{"/dev/nvme2n1": 11239424}
	if err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sys.fscks["/dev/nvme2n1"], []uint64{32768, 98304, 11239424}) {
		t.Errorf("fscks = %v", sys.fscks)
	}
	if sys.mounted["/repos"] == "" {
		t.Error("must re-run mount after a successful fsck")
	}
}

func TestRepair_NeverTouchesAValidatingVolume(t *testing.T) {
	sys := healthy()
	sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "mounted"}}}
	err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", true)
	if err == nil || len(sys.fscks) != 0 {
		t.Errorf("err=%v fscks=%v", err, sys.fscks)
	}
}

func TestRepair_AllBackupsFailNamesTheTaintRunbook(t *testing.T) {
	sys := healthy()
	sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "refused"}}}
	sys.backupSuperblocks["/dev/nvme2n1"] = []uint64{32768, 98304}
	err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", false)
	if err == nil || !strings.Contains(err.Error(), "terraform taint") {
		t.Errorf("err = %v", err)
	}
	if len(sys.fscks["/dev/nvme2n1"]) != 2 || sys.mounted["/repos"] != "" {
		t.Errorf("fscks=%v mounted=%v", sys.fscks, sys.mounted)
	}
}

func TestRepair_UnknownMountpointAndAbsentDevice(t *testing.T) {
	sys := healthy()
	if err := runRepair(context.Background(), sys, twoVolumeManifest(), "/nope", true); err == nil {
		t.Error("unknown mountpoint must error")
	}
	if err := runRepair(context.Background(), sys, nil, "/repos", true); err == nil {
		t.Error("no manifest must error")
	}
	sys = healthy()
	sys.state = State{Volumes: []VolumeState{{Mountpoint: "/repos", Outcome: "absent"}}}
	sys.devices = sys.devices[:1]
	if err := runRepair(context.Background(), sys, twoVolumeManifest(), "/repos", false); err == nil || len(sys.fscks) != 0 {
		t.Errorf("absent device: err=%v fscks=%v", err, sys.fscks)
	}
}
