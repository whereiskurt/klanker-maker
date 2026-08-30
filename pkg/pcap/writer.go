// Package pcap writes libpcap-format capture files.
//
// The format is small enough to implement directly — a 24-byte global header
// and a 16-byte header per packet — and doing so keeps the sidecars free of
// cgo. That is a hard requirement, not a preference: sidecars cross-compile to
// linux/amd64 from macOS with CGO_ENABLED=0, and libpcap would break the build.
package pcap

import (
	"encoding/binary"
	"io"
	"time"
)

// LinkTypeEthernet is LINKTYPE_ETHERNET, the link type for frames captured
// from an AF_PACKET socket on an Ethernet interface.
const LinkTypeEthernet = 1

// DefaultSnaplen captures whole frames on a standard-MTU interface.
const DefaultSnaplen uint32 = 65535

// magic is the little-endian microsecond-resolution pcap magic. Writing native
// little-endian keeps readers on x86 and arm from having to byte-swap.
const magic uint32 = 0xa1b2c3d4

// Writer emits a pcap stream. It is not safe for concurrent use; the capture
// daemon owns exactly one per capture.
type Writer struct {
	w       io.Writer
	snaplen uint32
	buf     [16]byte
}

// NewWriter writes the global header and returns a Writer ready for packets.
func NewWriter(w io.Writer, snaplen uint32) (*Writer, error) {
	if snaplen == 0 {
		snaplen = DefaultSnaplen
	}
	var hdr [24]byte
	binary.LittleEndian.PutUint32(hdr[0:4], magic)
	binary.LittleEndian.PutUint16(hdr[4:6], 2)  // version major
	binary.LittleEndian.PutUint16(hdr[6:8], 4)  // version minor
	binary.LittleEndian.PutUint32(hdr[8:12], 0) // thiszone: timestamps are UTC
	binary.LittleEndian.PutUint32(hdr[12:16], 0)
	binary.LittleEndian.PutUint32(hdr[16:20], snaplen)
	binary.LittleEndian.PutUint32(hdr[20:24], LinkTypeEthernet)
	if _, err := w.Write(hdr[:]); err != nil {
		return nil, err
	}
	return &Writer{w: w, snaplen: snaplen}, nil
}

// WritePacket appends one packet.
//
// origLen is the length on the wire, which may exceed len(data) when the
// capture was truncated to snaplen. Reporting it honestly is what lets
// Wireshark mark a packet as truncated instead of silently presenting a short
// frame as complete.
func (p *Writer) WritePacket(ts time.Time, data []byte, origLen int) error {
	if uint32(len(data)) > p.snaplen {
		data = data[:p.snaplen]
	}
	if origLen < len(data) {
		origLen = len(data)
	}
	binary.LittleEndian.PutUint32(p.buf[0:4], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(p.buf[4:8], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(p.buf[8:12], uint32(len(data)))
	binary.LittleEndian.PutUint32(p.buf[12:16], uint32(origLen))
	if _, err := p.w.Write(p.buf[:]); err != nil {
		return err
	}
	_, err := p.w.Write(data)
	return err
}
