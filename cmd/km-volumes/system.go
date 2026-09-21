package main

import (
	"context"
	"os"
	"syscall"
)

// Device is one non-root NVMe namespace as the kernel presents it RIGHT NOW,
// identified by a live Identify — never by the cached sysfs serial, which the
// spike showed stale after a resume while the namespace size was re-read.
type Device struct {
	Node        string // /dev/nvmeXn1
	BDF         string // PCI address of the controller, e.g. 0000:00:1f.0
	Serial      string // raw Identify Controller SN
	VolumeID    string // Serial rendered as vol-…
	LiveBytes   uint64 // Identify Namespace NSZE × LBA size
	KernelBytes uint64 // BLKGETSIZE64 on the node
}

// System is everything km-volumes needs from the kernel. The real
// implementation (system_linux.go) bounds every call with a timeout; the fake
// in fake_test.go records every side effect. Nothing outside these methods
// touches a device.
type System interface {
	// ListDevices enumerates every /dev/nvme*n1 whose controller is not the
	// root volume's, each with a fresh live Identify. Must be re-run after any
	// Reprobe: node names and BDFs move.
	ListDevices(ctx context.Context) ([]Device, error)
	// Identify does the same for one namespace node.
	Identify(node string) (Device, error)
	// ResolveBDM maps an AWS BDM letter to a node (pre-manifest fallback only).
	ResolveBDM(letter string) (node string, err error)
	// Blkid reports the filesystem UUID/type on the mount source for node —
	// the node itself, or its first child partition with a filesystem.
	Blkid(node string) (uuid, fstype, source string, err error)
	Mount(source, target, fstype string) error
	Unmount(target string, lazy bool) error
	IsMounted(target string) bool
	// MountSource is the device currently mounted at target ("" when none).
	MountSource(target string) string
	KillHolders(target string, sig syscall.Signal) error
	Sync()
	// Reprobe removes the PCI device and rescans the bus, then waits (bounded)
	// for the node count to be restored.
	Reprobe(bdf string) error
	// Rescan is a bare bus rescan (no remove): picks up a function that
	// dropped off the bus, but does NOT revisit one that is present and
	// driverless — that needs Reprobe of that exact function.
	Rescan() error
	// UnboundEBSControllers lists the BDFs of EBS NVMe PCI functions (vendor
	// 0x1d0f, device 0x8061) present on the bus with no driver bound — the
	// live-UAT shape left behind when a probe races the hypervisor's restore.
	UnboundEBSControllers() ([]string, error)
	// Ext4BlockCount reads "Block count"/"Block size" from dumpe2fs -h.
	Ext4BlockCount(source string) (count, blockSize uint64, err error)
	// BackupSuperblocks lists the backup superblocks mke2fs -n would place.
	BackupSuperblocks(node string) ([]uint64, error)
	// SuperblockUUID reads the filesystem UUID recorded in the backup
	// superblock at that block (dumpe2fs -o superblock=N -h) WITHOUT writing.
	SuperblockUUID(node string, superblock uint64) (string, error)
	// Fsck runs e2fsck -f -y -b <superblock> on node.
	Fsck(node string, superblock uint64) error
	Reboot() error

	WriteFile(path string, b []byte, mode os.FileMode) error
	ReadFile(path string) ([]byte, error)
	Remove(path string) error
	Exists(path string) bool
}
