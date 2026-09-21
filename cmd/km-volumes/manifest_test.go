package main

import (
	"path/filepath"
	"testing"
)

func TestManifest_AppendIsIdempotentPerMountpoint(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "volumes.json")
	v := Volume{Mountpoint: "/repos", VolumeID: "vol-1", SizeSectors: 10, FSUUID: "u", FSType: "ext4"}
	if err := appendManifestEntry(p, v); err != nil {
		t.Fatal(err)
	}
	v2 := v
	v2.VolumeID = "vol-2"
	if err := appendManifestEntry(p, v2); err == nil {
		t.Error("second entry for same mountpoint must be refused")
	}
	m, err := loadManifest(p)
	if err != nil || len(m.Volumes) != 1 || m.Volumes[0].VolumeID != "vol-1" {
		t.Errorf("%+v %v", m, err)
	}
}

func TestManifest_MissingIsNilNotError(t *testing.T) {
	m, err := loadManifest(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || m != nil {
		t.Errorf("got %+v, %v", m, err)
	}
}

func TestManifest_BuildEntryUsesLiveIdentifyAndBlkidSource(t *testing.T) {
	sys := healthy()
	sys.blkid["/dev/nvme2n1"] = [3]string{"0fea598a", "ext4", "/dev/nvme2n1p1"}
	v, err := buildManifestEntry(sys, manifestOpts{Mountpoint: "/repos", BDM: "g", Device: "/dev/nvme2n1p1", Label: "snapshot", FromSnapshot: "snap-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := Volume{Mountpoint: "/repos", BDM: "g", VolumeID: "vol-0b79fa8db9f48ee7c", SizeSectors: 335544320, FSUUID: "0fea598a", FSType: "ext4", FromSnapshot: "snap-1", Label: "snapshot"}
	if v != want {
		t.Errorf("got %+v\nwant %+v", v, want)
	}
}
