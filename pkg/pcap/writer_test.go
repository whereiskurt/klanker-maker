package pcap_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/pcap"
)

func TestWriter_GlobalHeaderIsByteExact(t *testing.T) {
	var buf bytes.Buffer
	if _, err := pcap.NewWriter(&buf, 65535); err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	got := buf.Bytes()
	if len(got) != 24 {
		t.Fatalf("global header must be exactly 24 bytes, got %d", len(got))
	}
	if magic := binary.LittleEndian.Uint32(got[0:4]); magic != 0xa1b2c3d4 {
		t.Errorf("magic = %#x, want 0xa1b2c3d4", magic)
	}
	if major := binary.LittleEndian.Uint16(got[4:6]); major != 2 {
		t.Errorf("version major = %d, want 2", major)
	}
	if minor := binary.LittleEndian.Uint16(got[6:8]); minor != 4 {
		t.Errorf("version minor = %d, want 4", minor)
	}
	if snap := binary.LittleEndian.Uint32(got[16:20]); snap != 65535 {
		t.Errorf("snaplen = %d, want 65535", snap)
	}
	if link := binary.LittleEndian.Uint32(got[20:24]); link != pcap.LinkTypeEthernet {
		t.Errorf("linktype = %d, want %d", link, pcap.LinkTypeEthernet)
	}
}

func TestWriter_PacketRecordHeader(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.NewWriter(&buf, 65535)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Unix(1756389731, 123456000).UTC()
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := w.WritePacket(ts, payload, 128); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	rec := buf.Bytes()[24:]
	if len(rec) != 16+len(payload) {
		t.Fatalf("record = %d bytes, want %d", len(rec), 16+len(payload))
	}
	if sec := binary.LittleEndian.Uint32(rec[0:4]); sec != 1756389731 {
		t.Errorf("ts_sec = %d", sec)
	}
	if usec := binary.LittleEndian.Uint32(rec[4:8]); usec != 123456 {
		t.Errorf("ts_usec = %d, want 123456", usec)
	}
	if incl := binary.LittleEndian.Uint32(rec[8:12]); incl != 4 {
		t.Errorf("incl_len = %d, want 4", incl)
	}
	// orig_len must report the wire length even when the capture was truncated,
	// or Wireshark silently misreports every truncated packet.
	if orig := binary.LittleEndian.Uint32(rec[12:16]); orig != 128 {
		t.Errorf("orig_len = %d, want 128", orig)
	}
	if !bytes.Equal(rec[16:], payload) {
		t.Errorf("payload = %x", rec[16:])
	}
}

func TestWriter_TruncatesToSnaplen(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.NewWriter(&buf, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePacket(time.Now(), make([]byte, 64), 64); err != nil {
		t.Fatal(err)
	}
	rec := buf.Bytes()[24:]
	if incl := binary.LittleEndian.Uint32(rec[8:12]); incl != 8 {
		t.Errorf("incl_len = %d, want snaplen 8", incl)
	}
	if orig := binary.LittleEndian.Uint32(rec[12:16]); orig != 64 {
		t.Errorf("orig_len = %d, want the full wire length 64", orig)
	}
}
