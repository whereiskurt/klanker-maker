package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const manifestPath = "/var/lib/km/volumes.json"

// Manifest is the first-cold-boot record of every additional volume's physical
// identity (spec §4). bdm is informational only once written — nothing ever
// resolves a device by letter again.
type Manifest struct {
	Version int      `json:"version"`
	Volumes []Volume `json:"volumes"`
}

type Volume struct {
	Mountpoint   string `json:"mountpoint"`
	BDM          string `json:"bdm"`
	VolumeID     string `json:"volumeId"`
	SizeSectors  uint64 `json:"sizeSectors"`
	FSUUID       string `json:"fsUUID"`
	FSType       string `json:"fsType"`
	FromSnapshot string `json:"fromSnapshot"`
	Label        string `json:"label"`
}

// loadManifest returns (nil, nil) when the file does not exist: a pre-manifest
// box is a supported state (spec §5.1 fallback), not an error.
func loadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// appendManifestEntry adds one volume, refusing a second entry for the same
// mountpoint so a re-run boot script cannot overwrite the trusted first record.
func appendManifestEntry(path string, v Volume) error {
	m, err := loadManifest(path)
	if err != nil {
		return err
	}
	if m == nil {
		m = &Manifest{Version: 1}
	}
	for _, e := range m.Volumes {
		if e.Mountpoint == v.Mountpoint {
			return fmt.Errorf("manifest already has an entry for %s (%s); refusing to overwrite", v.Mountpoint, e.VolumeID)
		}
	}
	m.Volumes = append(m.Volumes, v)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o644)
}

// atomicWrite is temp+rename in the target directory, so a reader never sees a
// half-written manifest or state file.
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

type manifestOpts struct {
	Mountpoint, BDM, Device, Label, FromSnapshot string
}

// buildManifestEntry issues the live Identify for the namespace behind
// opts.Device (a whole node or a partition of one) and reads the filesystem
// identity off the mount source blkid picks — the whole device, or the first
// child partition when a snapshot carried a partition table.
func buildManifestEntry(sys System, opts manifestOpts) (Volume, error) {
	node := namespaceNode(opts.Device)
	d, err := sys.Identify(node)
	if err != nil {
		return Volume{}, fmt.Errorf("identify %s: %w", node, err)
	}
	uuid, fstype, _, err := sys.Blkid(node)
	if err != nil {
		return Volume{}, fmt.Errorf("blkid %s: %w", node, err)
	}
	if uuid == "" {
		return Volume{}, fmt.Errorf("blkid %s: no filesystem UUID", node)
	}
	if fstype == "" {
		fstype = "ext4"
	}
	return Volume{
		Mountpoint:   opts.Mountpoint,
		BDM:          opts.BDM,
		VolumeID:     d.VolumeID,
		SizeSectors:  d.KernelBytes / 512,
		FSUUID:       uuid,
		FSType:       fstype,
		FromSnapshot: opts.FromSnapshot,
		Label:        opts.Label,
	}, nil
}

// namespaceNode strips a partition suffix: /dev/nvme2n1p1 -> /dev/nvme2n1.
func namespaceNode(dev string) string {
	i := strings.LastIndex(dev, "p")
	if i > 0 && strings.Contains(dev[:i], "n") && isDigits(dev[i+1:]) {
		return dev[:i]
	}
	return dev
}

// controllerNode strips the namespace: /dev/nvme2n1 -> /dev/nvme2.
func controllerNode(dev string) string {
	dev = namespaceNode(dev)
	i := strings.LastIndex(dev, "n")
	if i > 0 && isDigits(dev[i+1:]) {
		return dev[:i]
	}
	return dev
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
