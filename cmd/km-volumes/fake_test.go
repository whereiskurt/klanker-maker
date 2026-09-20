package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

// fakeSystem is the test double behind every decision path in this package.
// It never touches the kernel; every side effect is recorded so a test can
// assert what would have happened to a real box.
type fakeSystem struct {
	mountCalls int
	devices    []Device
	blkid      map[string][3]string // node -> {uuid, fstype, source}
	ext4       map[string][2]uint64 // source -> {block count, block size}
	bdm        map[string]string    // letter -> node

	files    map[string]bool
	contents map[string][]byte

	mounted      map[string]string // target -> source
	unmounted    []string
	lazyUnmounts map[string]bool
	kills        map[string][]syscall.Signal
	busy         map[string]int // target -> remaining EBUSY umount attempts

	reprobed              []string
	fixOnReprobe          map[string]int // bdf -> reprobe count at which KernelBytes heals
	reboots               []string
	rootBDF               string
	swapBDFOnFirstReprobe bool // spike §13.4: the BDF↔node binding moves across a re-probe
	synced                int
	listErr               error // ListDevices fails with this when set

	state             State
	backupSuperblocks map[string][]uint64
	fscks             map[string][]uint64
	fsckSucceedsAt    map[string]uint64
}

func newFake() *fakeSystem {
	return &fakeSystem{
		blkid:             map[string][3]string{},
		ext4:              map[string][2]uint64{},
		bdm:               map[string]string{},
		files:             map[string]bool{},
		contents:          map[string][]byte{},
		mounted:           map[string]string{},
		lazyUnmounts:      map[string]bool{},
		kills:             map[string][]syscall.Signal{},
		busy:              map[string]int{},
		fixOnReprobe:      map[string]int{},
		backupSuperblocks: map[string][]uint64{},
		fscks:             map[string][]uint64{},
		fsckSucceedsAt:    map[string]uint64{},
	}
}

func (f *fakeSystem) ListDevices(context.Context) ([]Device, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []Device
	for _, d := range f.devices {
		if f.rootBDF != "" && d.BDF == f.rootBDF {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeSystem) Identify(node string) (Device, error) {
	for _, d := range f.devices {
		if d.Node == node {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("fake: no device %s", node)
}

func (f *fakeSystem) ResolveBDM(letter string) (string, error) {
	if n, ok := f.bdm[letter]; ok {
		return n, nil
	}
	return "", fmt.Errorf("fake: BDM %s unresolved", letter)
}

func (f *fakeSystem) Blkid(node string) (string, string, string, error) {
	if b, ok := f.blkid[node]; ok {
		return b[0], b[1], b[2], nil
	}
	return "", "", "", fmt.Errorf("fake: no blkid for %s", node)
}

func (f *fakeSystem) Mount(source, target, fstype string) error {
	f.mountCalls++
	f.mounted[target] = source
	return nil
}

func (f *fakeSystem) MountSource(target string) string { return f.mounted[target] }

func (f *fakeSystem) Unmount(target string, lazy bool) error {
	if lazy {
		f.lazyUnmounts[target] = true
		delete(f.mounted, target)
		return nil
	}
	if f.busy[target] > 0 {
		f.busy[target]--
		return errors.New("umount: target is busy")
	}
	f.unmounted = append(f.unmounted, target)
	delete(f.mounted, target)
	return nil
}

func (f *fakeSystem) IsMounted(target string) bool {
	_, ok := f.mounted[target]
	return ok
}

func (f *fakeSystem) KillHolders(target string, sig syscall.Signal) error {
	f.kills[target] = append(f.kills[target], sig)
	return nil
}

func (f *fakeSystem) Sync() { f.synced++ }

func (f *fakeSystem) Reprobe(bdf string) error {
	f.reprobed = append(f.reprobed, bdf)
	if f.swapBDFOnFirstReprobe && len(f.reprobed) == 1 && len(f.devices) >= 2 {
		f.devices[0].BDF, f.devices[1].BDF = f.devices[1].BDF, f.devices[0].BDF
	}
	n := 0
	for _, b := range f.reprobed {
		if b == bdf {
			n++
		}
	}
	if at, ok := f.fixOnReprobe[bdf]; ok && n >= at {
		for i := range f.devices {
			if f.devices[i].BDF == bdf {
				f.devices[i].KernelBytes = f.devices[i].LiveBytes
			}
		}
	}
	return nil
}

func (f *fakeSystem) Ext4BlockCount(source string) (uint64, uint64, error) {
	if e, ok := f.ext4[source]; ok {
		return e[0], e[1], nil
	}
	return 0, 0, fmt.Errorf("fake: no ext4 info for %s", source)
}

func (f *fakeSystem) BackupSuperblocks(node string) ([]uint64, error) {
	if s, ok := f.backupSuperblocks[node]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("fake: no backup superblocks for %s", node)
}

func (f *fakeSystem) Fsck(node string, superblock uint64) error {
	f.fscks[node] = append(f.fscks[node], superblock)
	if at, ok := f.fsckSucceedsAt[node]; ok && at == superblock {
		return nil
	}
	return fmt.Errorf("fake: e2fsck -b %d failed on %s", superblock, node)
}

func (f *fakeSystem) Reboot() error {
	f.reboots = append(f.reboots, "reboot")
	return nil
}

func (f *fakeSystem) WriteFile(path string, b []byte, mode os.FileMode) error {
	f.files[path] = true
	f.contents[path] = append([]byte(nil), b...)
	if path == statePath {
		_ = json.Unmarshal(b, &f.state)
	}
	return nil
}

func (f *fakeSystem) ReadFile(path string) ([]byte, error) {
	if b, ok := f.contents[path]; ok {
		return b, nil
	}
	if path == statePath && len(f.state.Volumes) > 0 {
		return json.Marshal(f.state)
	}
	return nil, os.ErrNotExist
}

func (f *fakeSystem) Remove(path string) error {
	delete(f.files, path)
	delete(f.contents, path)
	return nil
}

func (f *fakeSystem) Exists(path string) bool { return f.files[path] }

// auditEvent is one captured emitAudit call.
type auditEvent struct {
	Type   string
	Detail map[string]string
}

// captureAudit swaps the audit sink for the duration of a test.
func captureAudit(t *testing.T) *[]auditEvent {
	t.Helper()
	var events []auditEvent
	prev := emitAudit
	emitAudit = func(eventType string, detail map[string]string) {
		events = append(events, auditEvent{Type: eventType, Detail: detail})
	}
	t.Cleanup(func() { emitAudit = prev })
	return &events
}

func count(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}
