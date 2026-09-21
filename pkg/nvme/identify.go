// Package nvme issues NVMe admin Identify commands and parses the parts km
// needs: the controller serial (an EBS volume id), the EBS vendor-specific
// block-device-mapping name, and a namespace's size. It exists because the
// kernel's cached view (/sys/block/*/device/serial) is stale after a
// hibernate/resume while the namespace size is re-read — only a live Identify
// tells the truth about which volume a node is bound to right now.
package nvme

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrUnsupported = errors.New("nvme: identify not supported on this platform")

const identifyLen = 4096

type Controller struct {
	Serial  string // e.g. "vol0b79fa8db9f48ee7c" (EBS puts the volume id, minus dash, in SN)
	Model   string
	BDMName string // EBS vendor-specific bytes 3072..3103: "/dev/sdg" or "sdg"
}

// VolumeID renders the serial as an EBS volume id ("vol-…").
func (c Controller) VolumeID() string {
	s := c.Serial
	if strings.HasPrefix(s, "vol") && !strings.HasPrefix(s, "vol-") {
		return "vol-" + s[3:]
	}
	return s
}

type Namespace struct {
	SizeLBAs uint64
	LBABytes uint32
}

func (n Namespace) Bytes() uint64 { return n.SizeLBAs * uint64(n.LBABytes) }

func ParseIdentifyController(buf []byte) (Controller, error) {
	if len(buf) < identifyLen {
		return Controller{}, fmt.Errorf("nvme: identify controller buffer %d bytes, want %d", len(buf), identifyLen)
	}
	return Controller{
		Serial:  ascii(buf[4:24]),
		Model:   ascii(buf[24:64]),
		BDMName: ascii(buf[3072:3104]),
	}, nil
}

func ParseIdentifyNamespace(buf []byte) (Namespace, error) {
	if len(buf) < identifyLen {
		return Namespace{}, fmt.Errorf("nvme: identify namespace buffer %d bytes, want %d", len(buf), identifyLen)
	}
	nsze := binary.LittleEndian.Uint64(buf[0:8])
	flbas := buf[26] & 0x0F
	lbads := buf[128+flbas*4+2]
	if lbads == 0 || lbads > 16 {
		return Namespace{}, fmt.Errorf("nvme: implausible lbads %d", lbads)
	}
	return Namespace{SizeLBAs: nsze, LBABytes: 1 << lbads}, nil
}

func ascii(b []byte) string {
	return strings.TrimRight(strings.TrimRight(string(b), "\x00"), " ")
}
