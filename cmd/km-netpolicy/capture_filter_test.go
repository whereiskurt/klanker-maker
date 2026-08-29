package main

import (
	"testing"

	"golang.org/x/net/bpf"
)

// buildFrame constructs a synthetic Ethernet + IPv4 + TCP frame with a
// standard 20-byte IP header (no options), matching the fixed-offset
// assumptions buildBPFProgram bakes in (pkg comments there name the same
// offsets):
//
//	 0- 5  dst MAC        (unused by the filter)
//	 6-11  src MAC        (unused by the filter)
//	12-13  EtherType      0x0800 = IPv4
//	14     IP version/IHL 0x45   = v4, 5×4=20-byte header, no options
//	15     DSCP/ECN       0x00
//	16-17  total length
//	18-19  identification
//	20-21  flags/frag offset
//	22     TTL
//	23     protocol       6 (TCP) or 17 (UDP)
//	24-25  header checksum (unvalidated by the filter)
//	26-29  source IP
//	30-33  dest IP
//	34-35  source port    (start of TCP/UDP header, offset 14+20)
//	36-37  dest port
//	38-53  rest of a 20-byte TCP header, zeroed
func buildFrame(proto byte, srcIP, dstIP [4]byte, srcPort, dstPort uint16) []byte {
	f := make([]byte, 54)
	// dst/src MAC left zero — the filter never reads them.
	f[12], f[13] = 0x08, 0x00 // EtherType: IPv4
	f[14] = 0x45              // version 4, IHL 5 (20-byte header)
	f[16], f[17] = 0x00, 0x28 // total length 40 (20 IP + 20 TCP)
	f[22] = 64                // TTL
	f[23] = proto
	copy(f[26:30], srcIP[:])
	copy(f[30:34], dstIP[:])
	f[34], f[35] = byte(srcPort>>8), byte(srcPort)
	f[36], f[37] = byte(dstPort>>8), byte(dstPort)
	return f
}

var (
	ipA = [4]byte{10, 0, 0, 5}  // the "matching" host in every test below
	ipB = [4]byte{10, 0, 0, 99} // a non-matching host
)

// runProgram assembles+validates prog exactly as buildFilter does (catching a
// malformed jump target the same way SO_ATTACH_FILTER's kernel-side validator
// would) and then runs it through bpf.NewVM's pure-Go interpreter — no
// socket, no CAP_NET_RAW, no root, no Linux required.
func runProgram(t *testing.T, prog []bpf.Instruction, frame []byte) bool {
	t.Helper()
	if _, err := bpf.Assemble(prog); err != nil {
		t.Fatalf("bpf.Assemble: %v", err)
	}
	vm, err := bpf.NewVM(prog)
	if err != nil {
		t.Fatalf("bpf.NewVM: %v", err)
	}
	n, err := vm.Run(frame)
	if err != nil {
		t.Fatalf("vm.Run: %v", err)
	}
	return n > 0
}

func TestBuildBPFProgram_NeitherFlagIsNilNotAcceptAll(t *testing.T) {
	prog := buildBPFProgram(captureReq{})
	if len(prog) != 0 {
		t.Fatalf("neither host nor port set: want a nil/empty program, got %d instructions", len(prog))
	}
}

func TestBuildBPFProgram_HostOnly(t *testing.T) {
	prog := buildBPFProgram(captureReq{Host: "10.0.0.5"})

	match := buildFrame(ipProtoTCP, ipA, ipB, 1234, 443) // src == the wanted host
	if !runProgram(t, prog, match) {
		t.Error("frame with matching source host was rejected")
	}

	matchDst := buildFrame(ipProtoTCP, ipB, ipA, 1234, 443) // dst == the wanted host
	if !runProgram(t, prog, matchDst) {
		t.Error("frame with matching dest host was rejected")
	}

	noMatch := buildFrame(ipProtoTCP, ipB, ipB, 1234, 443) // neither side is the wanted host
	if runProgram(t, prog, noMatch) {
		t.Error("frame with no matching host was accepted")
	}
}

func TestBuildBPFProgram_PortOnly(t *testing.T) {
	prog := buildBPFProgram(captureReq{Port: 443})

	match := buildFrame(ipProtoTCP, ipA, ipB, 1234, 443) // dst port == the wanted port
	if !runProgram(t, prog, match) {
		t.Error("frame with matching dest port was rejected")
	}

	matchSrc := buildFrame(ipProtoTCP, ipA, ipB, 443, 1234) // src port == the wanted port
	if !runProgram(t, prog, matchSrc) {
		t.Error("frame with matching source port was rejected")
	}

	noMatch := buildFrame(ipProtoTCP, ipA, ipB, 1234, 5678) // neither port is 443
	if runProgram(t, prog, noMatch) {
		t.Error("frame with no matching port was accepted")
	}
}

// TestBuildBPFProgram_HostAndPortRequiresBoth is the case a wrong jump offset
// turns into an OR: a program that accepts on right-host-wrong-port (or vice
// versa) would pass every other test in this file and still be a real bug —
// it would over-capture in exactly the way the filter exists to prevent.
func TestBuildBPFProgram_HostAndPortRequiresBoth(t *testing.T) {
	prog := buildBPFProgram(captureReq{Host: "10.0.0.5", Port: 443})

	both := buildFrame(ipProtoTCP, ipA, ipB, 1234, 443) // right host AND right port
	if !runProgram(t, prog, both) {
		t.Error("frame matching both host and port was rejected")
	}

	rightHostWrongPort := buildFrame(ipProtoTCP, ipA, ipB, 1234, 5678)
	if runProgram(t, prog, rightHostWrongPort) {
		t.Error("frame with the right host but wrong port was accepted — filter is OR, not AND")
	}

	wrongHostRightPort := buildFrame(ipProtoTCP, ipB, ipB, 1234, 443)
	if runProgram(t, prog, wrongHostRightPort) {
		t.Error("frame with the right port but wrong host was accepted — filter is OR, not AND")
	}
}

func TestBuildBPFProgram_AssemblesForEveryShape(t *testing.T) {
	shapes := map[string]captureReq{
		"host only":     {Host: "10.0.0.5"},
		"port only":     {Port: 443},
		"host and port": {Host: "10.0.0.5", Port: 443},
	}
	for name, req := range shapes {
		t.Run(name, func(t *testing.T) {
			prog := buildBPFProgram(req)
			if len(prog) == 0 {
				t.Fatal("expected a non-empty program")
			}
			if _, err := bpf.Assemble(prog); err != nil {
				t.Errorf("bpf.Assemble: %v", err)
			}
		})
	}
}
