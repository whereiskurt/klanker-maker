package nvme

import "testing"

func idCtrlBytes(sn, model, bdm string) []byte {
	b := make([]byte, 4096)
	copy(b[4:24], padRight(sn, 20))
	copy(b[24:64], padRight(model, 40))
	copy(b[3072:3104], padRight(bdm, 32)) // EBS vendor-specific: BDM name
	return b
}

func padRight(s string, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	copy(out, s)
	return out
}

func TestParseIdentifyController(t *testing.T) {
	c, err := ParseIdentifyController(idCtrlBytes("vol0b79fa8db9f48ee7c", "Amazon Elastic Block Store", "/dev/sdg"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Serial != "vol0b79fa8db9f48ee7c" || c.VolumeID() != "vol-0b79fa8db9f48ee7c" {
		t.Errorf("serial=%q volumeID=%q", c.Serial, c.VolumeID())
	}
	if c.BDMName != "/dev/sdg" || c.Model != "Amazon Elastic Block Store" {
		t.Errorf("bdm=%q model=%q", c.BDMName, c.Model)
	}
	if _, err := ParseIdentifyController(make([]byte, 100)); err == nil {
		t.Error("short buffer must error")
	}
}

func TestParseIdentifyNamespace(t *testing.T) {
	b := make([]byte, 4096)
	// nsze (u64 LE) at 0; flbas at 26 selects LBA format 0; lbaf0 at 128: ms(u16) rp... lbads at byte 130
	nsze := uint64(335544320)
	for i := 0; i < 8; i++ {
		b[i] = byte(nsze >> (8 * i))
	}
	b[26] = 0  // flbas: format 0
	b[130] = 9 // lbads = 9 → 512-byte LBAs
	ns, err := ParseIdentifyNamespace(b)
	if err != nil {
		t.Fatal(err)
	}
	if ns.SizeLBAs != 335544320 || ns.LBABytes != 512 || ns.Bytes() != 335544320*512 {
		t.Errorf("got %+v", ns)
	}
}
