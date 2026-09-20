//go:build linux

package nvme

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// struct nvme_admin_cmd from <linux/nvme_ioctl.h>, 72 bytes.
type adminCmd struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	NSID        uint32
	CDW2, CDW3  uint32
	Metadata    uint64
	Addr        uint64
	MetadataLen uint32
	DataLen     uint32
	CDW10       uint32
	CDW11       uint32
	CDW12       uint32
	CDW13       uint32
	CDW14       uint32
	CDW15       uint32
	TimeoutMS   uint32
	Result      uint32
}

// _IOWR('N', 0x41, struct nvme_admin_cmd)
const nvmeIoctlAdminCmd = 0xC0484E41

func identify(devPath string, nsid, cns uint32) ([]byte, error) {
	f, err := os.OpenFile(devPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, identifyLen)
	cmd := adminCmd{
		Opcode:    0x06, // Identify
		NSID:      nsid,
		Addr:      uint64(uintptr(unsafe.Pointer(&buf[0]))),
		DataLen:   identifyLen,
		CDW10:     cns,
		TimeoutMS: 2000,
	}
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, f.Fd(), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd))); e != 0 {
		return nil, fmt.Errorf("nvme identify(cns=%d) %s: %w", cns, devPath, e)
	}
	return buf, nil
}

func IdentifyController(devPath string) (Controller, error) {
	b, err := identify(devPath, 0, 1)
	if err != nil {
		return Controller{}, err
	}
	return ParseIdentifyController(b)
}

func IdentifyNamespace(devPath string, nsid uint32) (Namespace, error) {
	b, err := identify(devPath, nsid, 0)
	if err != nil {
		return Namespace{}, err
	}
	return ParseIdentifyNamespace(b)
}
