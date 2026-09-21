//go:build linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/whereiskurt/klanker-maker/pkg/nvme"
)

// Per-call bounds. pre-sleep runs under the shim's `timeout 15` and post-sleep
// under `timeout 45`; every kernel-facing call here must finish well inside
// those so a wedged device costs one step, not the systemd transition.
const (
	mountTimeout   = 10 * time.Second
	umountTimeout  = 3 * time.Second
	fuserTimeout   = 3 * time.Second
	dumpe2fsTimout = 5 * time.Second
	blkidTimeout   = 5 * time.Second
	findmntTimeout = 3 * time.Second
	mke2fsTimeout  = 10 * time.Second
	e2fsckTimeout  = 600 * time.Second
	reprobeWait    = 10 * time.Second
	reprobePoll    = 200 * time.Millisecond
)

var bdfRe = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-9a-f]$`)

type realSystem struct{}

func newRealSystem() (System, error) { return realSystem{}, nil }

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() != nil {
			return s, fmt.Errorf("%s %s: timed out after %s", name, strings.Join(args, " "), timeout)
		}
		if s != "" {
			return s, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, s)
		}
		return s, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return s, nil
}

// rootController resolves the controller sysfs directory behind /, so it can
// be excluded from enumeration and never re-probed (spec §5.4). Failing to
// find it is an error, not "no root": the safe disposition is to refuse to
// touch anything rather than risk re-probing the resume device.
func rootController() (string, error) {
	src, err := run(findmntTimeout, "findmnt", "-no", "SOURCE", "/")
	if err != nil {
		return "", err
	}
	src = strings.TrimSpace(strings.SplitN(src, "\n", 2)[0])
	if !strings.HasPrefix(src, "/dev/") {
		return "", fmt.Errorf("root source %q is not a device", src)
	}
	dev := filepath.Base(namespaceNode(src))
	ctrl, err := filepath.EvalSymlinks(filepath.Join("/sys/block", dev, "device"))
	if err != nil {
		return "", fmt.Errorf("root controller for %s: %w", dev, err)
	}
	return ctrl, nil
}

func nodeCount() int {
	nodes, _ := filepath.Glob("/dev/nvme*n1")
	return len(nodes)
}

func (realSystem) ListDevices(ctx context.Context) ([]Device, error) {
	root, err := rootController()
	if err != nil {
		return nil, err
	}
	nodes, err := filepath.Glob("/dev/nvme*n1")
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, node := range nodes {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		ctrl, err := filepath.EvalSymlinks(filepath.Join("/sys/block", filepath.Base(node), "device"))
		if err != nil {
			continue // node vanished between the glob and now (mid re-probe)
		}
		if ctrl == root {
			continue
		}
		d, err := (realSystem{}).Identify(node)
		if err != nil {
			fmt.Fprintf(os.Stderr, "km-volumes: identify %s: %v\n", node, err)
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (realSystem) Identify(node string) (Device, error) {
	node = namespaceNode(node)
	ctrlNode := controllerNode(node)
	c, err := nvme.IdentifyController(ctrlNode)
	if err != nil {
		return Device{}, err
	}
	ns, err := nvme.IdentifyNamespace(node, 1)
	if err != nil {
		return Device{}, err
	}
	kb, err := blockDeviceBytes(node)
	if err != nil {
		return Device{}, err
	}
	d := Device{Node: node, Serial: c.Serial, VolumeID: c.VolumeID(), LiveBytes: ns.Bytes(), KernelBytes: kb}
	// /sys/block/nvmeXn1/device -> the nvme controller; its device -> the PCI function.
	if p, err := filepath.EvalSymlinks(filepath.Join("/sys/block", filepath.Base(node), "device", "device")); err == nil {
		if b := filepath.Base(p); bdfRe.MatchString(b) {
			d.BDF = b
		}
	}
	return d, nil
}

// blockDeviceBytes is BLKGETSIZE64 — the kernel's current view of the size,
// which the spike showed IS re-read across a resume even while the controller
// identity is not.
func blockDeviceBytes(node string) (uint64, error) {
	f, err := os.OpenFile(node, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var size uint64
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.BLKGETSIZE64, uintptr(unsafe.Pointer(&size))); e != 0 {
		return 0, fmt.Errorf("BLKGETSIZE64 %s: %w", node, e)
	}
	return size, nil
}

// ResolveBDM mirrors the userdata's resolve_ebs_device: classic symlinks
// first, then the BDM name EBS embeds in each controller's vendor bytes.
func (realSystem) ResolveBDM(letter string) (string, error) {
	for _, d := range []string{"/dev/sd" + letter, "/dev/xvd" + letter} {
		if fi, err := os.Stat(d); err == nil && fi.Mode()&os.ModeDevice != 0 {
			return d, nil
		}
	}
	nodes, _ := filepath.Glob("/dev/nvme*n1")
	for _, node := range nodes {
		c, err := nvme.IdentifyController(controllerNode(node))
		if err != nil {
			continue
		}
		bdm := strings.TrimPrefix(c.BDMName, "/dev/")
		bdm = strings.TrimPrefix(bdm, "xvd")
		bdm = strings.TrimPrefix(bdm, "sd")
		if bdm == letter {
			return node, nil
		}
	}
	return "", fmt.Errorf("BDM sd%s: no device", letter)
}

// Blkid picks the mount source by today's rule (whole device unless it has a
// partition table and no top-level filesystem, then the first child partition
// carrying one) and reports that source's UUID/TYPE.
func (realSystem) Blkid(node string) (string, string, string, error) {
	src := node
	if t, _ := run(blkidTimeout, "blkid", "-s", "TYPE", "-o", "value", node); t == "" {
		if out, err := run(blkidTimeout, "lsblk", "-nro", "NAME,FSTYPE", node); err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := strings.Fields(line)
				if len(f) == 2 && f[1] != "" && "/dev/"+f[0] != node {
					src = "/dev/" + f[0]
					break
				}
			}
		}
	}
	uuid, err := run(blkidTimeout, "blkid", "-s", "UUID", "-o", "value", src)
	if err != nil {
		return "", "", src, err
	}
	fstype, _ := run(blkidTimeout, "blkid", "-s", "TYPE", "-o", "value", src)
	return uuid, fstype, src, nil
}

func (realSystem) Mount(source, target, fstype string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if _, err := run(mountTimeout, "mount", "-t", fstype, "-o", "defaults", source, target); err != nil {
		return err
	}
	_, _ = run(mountTimeout, "chown", "sandbox:sandbox", target)
	return nil
}

func (realSystem) Unmount(target string, lazy bool) error {
	if lazy {
		_, err := run(umountTimeout, "umount", "-l", target)
		return err
	}
	_, err := run(umountTimeout, "umount", target)
	return err
}

func (realSystem) MountSource(target string) string {
	out, err := run(findmntTimeout, "findmnt", "-no", "SOURCE", "--mountpoint", target)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

func (realSystem) IsMounted(target string) bool {
	out, err := run(findmntTimeout, "findmnt", "-no", "TARGET", target)
	return err == nil && strings.TrimSpace(out) == target
}

func (realSystem) KillHolders(target string, sig syscall.Signal) error {
	name := "TERM"
	if sig == syscall.SIGKILL {
		name = "KILL"
	}
	// fuser exits 1 when there are no holders; that is not a failure here.
	_, err := run(fuserTimeout, "fuser", "-k", "-"+name, "-m", target)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return nil
	}
	return err
}

func (realSystem) Sync() { unix.Sync() }

// Reprobe is the spike's from-scratch probe: PCI remove, bus rescan, then wait
// (bounded) for the node count to come back. Callers must ListDevices again
// afterwards — the node name and the BDF↔node binding both move.
func (realSystem) Reprobe(bdf string) error {
	if !bdfRe.MatchString(bdf) {
		return fmt.Errorf("reprobe: %q is not a PCI address", bdf)
	}
	before := nodeCount()
	if err := os.WriteFile(filepath.Join("/sys/bus/pci/devices", bdf, "remove"), []byte("1"), 0); err != nil {
		return fmt.Errorf("reprobe %s: remove: %w", bdf, err)
	}
	if err := os.WriteFile("/sys/bus/pci/rescan", []byte("1"), 0); err != nil {
		return fmt.Errorf("reprobe %s: rescan: %w", bdf, err)
	}
	deadline := time.Now().Add(reprobeWait)
	for time.Now().Before(deadline) {
		if nodeCount() >= before {
			return nil
		}
		time.Sleep(reprobePoll)
	}
	return fmt.Errorf("reprobe %s: node did not reappear within %s", bdf, reprobeWait)
}

func (realSystem) Rescan() error {
	if err := os.WriteFile("/sys/bus/pci/rescan", []byte("1"), 0); err != nil {
		return fmt.Errorf("rescan: %w", err)
	}
	return nil
}

const (
	ebsVendorID = "0x1d0f"
	ebsDeviceID = "0x8061"
)

// UnboundEBSControllers walks /sys/bus/pci/devices for EBS NVMe functions
// with no `driver` symlink. The root controller is never in this list: it is
// the resume device and is bound by definition.
func (realSystem) UnboundEBSControllers() ([]string, error) {
	entries, err := os.ReadDir("/sys/bus/pci/devices")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		bdf := e.Name()
		if !bdfRe.MatchString(bdf) {
			continue
		}
		dir := filepath.Join("/sys/bus/pci/devices", bdf)
		if sysfsValue(filepath.Join(dir, "vendor")) != ebsVendorID || sysfsValue(filepath.Join(dir, "device")) != ebsDeviceID {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, "driver")); err == nil {
			continue
		}
		out = append(out, bdf)
	}
	return out, nil
}

func sysfsValue(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (realSystem) Ext4BlockCount(source string) (uint64, uint64, error) {
	out, err := run(dumpe2fsTimout, "dumpe2fs", "-h", source)
	if err != nil {
		return 0, 0, err
	}
	var count, bs uint64
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Block count":
			count, _ = strconv.ParseUint(v, 10, 64)
		case "Block size":
			bs, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if count == 0 || bs == 0 {
		return 0, 0, fmt.Errorf("dumpe2fs -h %s: no block count/size", source)
	}
	return count, bs, nil
}

// BackupSuperblocks parses the "Superblock backups stored on blocks:" line(s)
// of `mke2fs -n` (a dry run; -n never writes).
// SuperblockUUID asks dumpe2fs -h with an alternate superblock; -h never
// writes, so this is safe against a backup that belongs to another
// filesystem — the reason repair checks it before ever running e2fsck.
func (realSystem) SuperblockUUID(node string, superblock uint64) (string, error) {
	out, err := run(dumpe2fsTimout, "dumpe2fs", "-o", "superblock="+strconv.FormatUint(superblock, 10), "-h", node)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Filesystem UUID:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Filesystem UUID:")), nil
		}
	}
	return "", fmt.Errorf("dumpe2fs superblock=%d %s: no Filesystem UUID line", superblock, node)
}

func (realSystem) BackupSuperblocks(node string) ([]uint64, error) {
	out, err := run(mke2fsTimeout, "mke2fs", "-n", node)
	if err != nil {
		return nil, err
	}
	_, after, ok := strings.Cut(out, "Superblock backups stored on blocks:")
	if !ok {
		return nil, fmt.Errorf("mke2fs -n %s: no backup superblock list", node)
	}
	var sbs []uint64
	for _, tok := range strings.FieldsFunc(after, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		n, err := strconv.ParseUint(tok, 10, 64)
		if err != nil {
			break // past the list
		}
		sbs = append(sbs, n)
	}
	if len(sbs) == 0 {
		return nil, fmt.Errorf("mke2fs -n %s: empty backup superblock list", node)
	}
	return sbs, nil
}

func (realSystem) Fsck(node string, superblock uint64) error {
	_, err := run(e2fsckTimeout, "e2fsck", "-f", "-y", "-b", strconv.FormatUint(superblock, 10), node)
	var ee *exec.ExitError
	// e2fsck exit 1 = errors corrected, 2 = corrected + reboot suggested; both
	// mean the backup superblock was usable.
	if errors.As(err, &ee) && (ee.ExitCode() == 1 || ee.ExitCode() == 2) {
		return nil
	}
	return err
}

func (realSystem) Reboot() error {
	_, err := run(mountTimeout, "systemctl", "reboot")
	return err
}

func (realSystem) WriteFile(path string, b []byte, mode os.FileMode) error {
	return atomicWrite(path, b, mode)
}

func (realSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (realSystem) Remove(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (realSystem) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
