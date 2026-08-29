//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"github.com/whereiskurt/klanker-maker/pkg/pcap"
)

// captureState is the daemon's single in-flight capture. At most one runs at a
// time: two concurrent captures on one interface double the disk cost for
// duplicate data, and the second is almost always a forgotten first.
type captureState struct {
	mu     sync.Mutex
	active bool
	path   string
	stop   chan struct{}
	done   chan struct{}
	err    error
}

// runCaptureDaemon serves capture requests over a Unix socket as root.
//
// It exists because CAP_NET_RAW cannot be delegated through a file. Everything
// else in this binary is an unprivileged operation on an append-only file; this
// is the one place that needs an arbitrating privileged process, and it holds
// no state between captures.
func runCaptureDaemon(o opts) int {
	if err := os.MkdirAll(filepath.Dir(o.captureSock), 0o755); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: %v\n", prog, err)
		return 1
	}
	if err := os.MkdirAll(o.captureDir, 0o755); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: %v\n", prog, err)
		return 1
	}
	// A stale socket from an unclean stop would make Listen fail forever.
	_ = os.Remove(o.captureSock)

	ln, err := net.Listen("unix", o.captureSock)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: listen: %v\n", prog, err)
		return 1
	}
	defer ln.Close()

	// 0660 owned by the sandbox group so an unprivileged agent can ask for a
	// capture without ever holding CAP_NET_RAW. If the profile has no sandbox
	// group, fall back to 0666 rather than refusing to start — the socket only
	// ever accepts the bounded verbs below, and a dead daemon helps nobody.
	if err := os.Chmod(o.captureSock, 0o660); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: chmod: %v\n", prog, err)
	}
	if g, gerr := user.LookupGroup("sandbox"); gerr == nil {
		if gid, cerr := strconv.Atoi(g.Gid); cerr == nil {
			_ = os.Chown(o.captureSock, 0, gid)
		}
	} else {
		_ = os.Chmod(o.captureSock, 0o666)
	}

	st := &captureState{}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0 // listener closed on shutdown
		}
		go serveCapture(conn, st, o)
	}
}

// serveCapture handles one request/response exchange.
func serveCapture(conn net.Conn, st *captureState, o opts) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	var req captureReq
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(captureResp{OK: false, Msg: "bad request: " + err.Error()})
		return
	}

	var resp captureResp
	switch req.Op {
	case "start":
		resp = st.start(req, o)
	case "stop":
		resp = st.stopCapture(o)
	case "status":
		resp = st.status()
	case "list":
		resp = listCaptures(o)
	default:
		resp = captureResp{OK: false, Msg: "unknown op " + req.Op}
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

// start opens the raw socket and begins streaming to a pcap file.
func (s *captureState) start(req captureReq, o opts) captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return captureResp{OK: false, Msg: "a capture is already running; run `km-netpolicy capture stop` first"}
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil || dur <= 0 || dur > maxCaptureDuration {
		return captureResp{OK: false, Msg: "duration out of bounds"}
	}
	if req.MaxSize <= 0 || req.MaxSize > maxCaptureSize {
		return captureResp{OK: false, Msg: "max_size out of bounds"}
	}

	// Refuse unless there is room for the capture twice over. Filling the data
	// volume takes the whole sandbox down, which is a far worse outcome than a
	// refused diagnostic.
	var stat unix.Statfs_t
	if err := unix.Statfs(o.captureDir, &stat); err == nil {
		free := int64(stat.Bavail) * int64(stat.Bsize)
		if free < req.MaxSize*2 {
			return captureResp{OK: false, Msg: fmt.Sprintf(
				"only %d bytes free in %s; need %d (2x max-size)", free, o.captureDir, req.MaxSize*2)}
		}
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return captureResp{OK: false, Msg: "AF_PACKET socket: " + err.Error()}
	}
	if filter := buildFilter(req); len(filter) > 0 {
		if err := attachFilter(fd, filter); err != nil {
			unix.Close(fd)
			return captureResp{OK: false, Msg: "attach filter: " + err.Error()}
		}
	}

	path := filepath.Join(o.captureDir, time.Now().UTC().Format("20060102T150405Z")+".pcap")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		unix.Close(fd)
		return captureResp{OK: false, Msg: "create capture file: " + err.Error()}
	}
	w, err := pcap.NewWriter(f, pcap.DefaultSnaplen)
	if err != nil {
		f.Close()
		unix.Close(fd)
		return captureResp{OK: false, Msg: "pcap header: " + err.Error()}
	}

	s.active, s.path, s.err = true, path, nil
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.loop(fd, f, w, dur, req.MaxSize)

	return captureResp{OK: true, Msg: fmt.Sprintf(
		"capturing to %s (max %s / %d bytes)", path, dur, req.MaxSize)}
}

// loop streams frames until any bound is hit. Both bounds are enforced here,
// not just the one the operator was thinking about when they typed the command.
func (s *captureState) loop(fd int, f *os.File, w *pcap.Writer, dur time.Duration, maxSize int64) {
	defer close(s.done)
	defer unix.Close(fd)
	defer f.Close()

	deadline := time.Now().Add(dur)
	buf := make([]byte, 65536)
	var written int64

	// A read timeout is what lets the loop notice the deadline and an explicit
	// stop on an idle interface, instead of blocking in recvfrom forever.
	tv := unix.Timeval{Sec: 1}
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)

	for {
		select {
		case <-s.stop:
			return
		default:
		}
		if time.Now().After(deadline) || written >= maxSize {
			return
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			s.err = err
			return
		}
		if n <= 0 {
			continue
		}
		if err := w.WritePacket(time.Now().UTC(), buf[:n], n); err != nil {
			s.err = err
			return
		}
		written += int64(n) + 16
	}
}

// stopCapture ends the running capture and uploads the result.
func (s *captureState) stopCapture(o opts) captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return captureResp{OK: false, Msg: "no capture is running"}
	}
	close(s.stop)
	<-s.done
	s.active = false
	path := s.path

	resp := captureResp{OK: true, Msg: "stopped " + path, Files: []string{path}}
	if s.err != nil {
		resp.Msg = fmt.Sprintf("stopped %s (with error: %v)", path, s.err)
	}
	// Upload is a convenience; the file on disk is the deliverable. A failed
	// upload must not lose the capture, so it is reported and the local file
	// left in place — the same stance the learn flush takes.
	if uri, err := uploadCapture(path, o); err != nil {
		resp.Msg += fmt.Sprintf(" (S3 upload failed, file kept locally: %v)", err)
	} else {
		resp.S3URI = uri
	}
	return resp
}

func (s *captureState) status() captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return captureResp{OK: true, Msg: "idle"}
	}
	return captureResp{OK: true, Msg: "capturing to " + s.path}
}

// listCaptures reports finished capture files.
func listCaptures(o opts) captureResp {
	entries, err := os.ReadDir(o.captureDir)
	if err != nil {
		return captureResp{OK: true, Msg: "no captures"}
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".pcap" {
			files = append(files, filepath.Join(o.captureDir, e.Name()))
		}
	}
	if len(files) == 0 {
		return captureResp{OK: true, Msg: "no captures"}
	}
	return captureResp{OK: true, Msg: fmt.Sprintf("%d capture(s):", len(files)), Files: files}
}

// htons converts a uint16 to network byte order, which AF_PACKET's protocol
// argument requires.
func htons(v uint16) uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return binary.LittleEndian.Uint16(b[:])
}

// buildFilter assembles a classic BPF program restricting the capture to the
// requested host and/or port. AF_PACKET pulls every frame off the wire by
// default; without a filter a "just this host" request would still burn
// through --max-size on unrelated traffic before the operator ever sees the
// packets they asked for.
//
// Both clauses are IPv4-only (Ethernet + IPv4 + TCP/UDP) and match either
// source or destination, mirroring how "host"/"port" read in a tcpdump
// expression. Returns nil when neither flag is set, or when --host cannot be
// resolved — the filter is a narrowing convenience, not a precondition, so an
// unresolvable host is dropped rather than failing the whole capture.
//
// The instruction offsets below are hand-derived (see task-11 notes), not
// generated by a general assembler, because the program has exactly four
// possible shapes (neither/host/port/both) and backpatching machinery would
// be more code than the filter itself. ethertypeLen/hostLen/portLen are the
// fixed instruction counts of each block; every jump target is computed from
// them so the shape stays consistent if a block's instruction count changes.
func buildFilter(req captureReq) []unix.SockFilter {
	var hostIP net.IP
	if req.Host != "" {
		if addr, err := net.ResolveIPAddr("ip4", req.Host); err == nil {
			hostIP = addr.IP.To4()
		}
	}
	wantHost := hostIP != nil
	wantPort := req.Port > 0
	if !wantHost && !wantPort {
		return nil
	}

	const (
		ethertypeLen = 2 // load ethertype, jump-if-not-IPv4
		hostLen      = 4 // load src, jump-if-match; load dst, jump-if-match
		portLen      = 8 // load proto, jump TCP, jump UDP, X=IHL*4, load+jump srcport, load+jump dstport
	)
	hostBlockStart := ethertypeLen
	portBlockStart := ethertypeLen
	if wantHost {
		portBlockStart += hostLen
	}
	acceptIdx := ethertypeLen
	if wantHost {
		acceptIdx += hostLen
	}
	if wantPort {
		acceptIdx += portLen
	}
	rejectIdx := acceptIdx + 1

	prog := make([]bpf.Instruction, 0, rejectIdx+1)

	// Ethertype gate: every subsequent offset assumes an IPv4 payload.
	prog = append(prog, bpf.LoadAbsolute{Off: 12, Size: 2})
	prog = append(prog, bpf.JumpIf{
		Cond: bpf.JumpEqual, Val: unix.ETH_P_IP,
		SkipTrue: 0, SkipFalse: uint8(rejectIdx - 2),
	})

	if wantHost {
		hostVal := binary.BigEndian.Uint32(hostIP)
		matchTarget := portBlockStart
		if !wantPort {
			matchTarget = acceptIdx
		}
		prog = append(prog, bpf.LoadAbsolute{Off: 26, Size: 4}) // src IP
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: hostVal,
			SkipTrue: uint8(matchTarget - (hostBlockStart + 2)), SkipFalse: 0,
		})
		prog = append(prog, bpf.LoadAbsolute{Off: 30, Size: 4}) // dst IP
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: hostVal,
			SkipTrue: 0, SkipFalse: uint8(rejectIdx - (hostBlockStart + 4)),
		})
	}

	if wantPort {
		portVal := uint32(req.Port)
		tcpCheck := portBlockStart + 1
		udpCheck := portBlockStart + 2
		srcPortCheck := portBlockStart + 5
		dstPortCheck := portBlockStart + 7
		prog = append(prog, bpf.LoadAbsolute{Off: 23, Size: 1}) // IP protocol
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: unix.IPPROTO_TCP,
			SkipTrue: uint8((portBlockStart + 3) - (tcpCheck + 1)), SkipFalse: 0,
		})
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: unix.IPPROTO_UDP,
			SkipTrue: 0, SkipFalse: uint8(rejectIdx - (udpCheck + 1)),
		})
		prog = append(prog, bpf.LoadMemShift{Off: 14})          // X = IHL*4 (IP header starts at 14)
		prog = append(prog, bpf.LoadIndirect{Off: 14, Size: 2}) // src port
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: portVal,
			SkipTrue: uint8(acceptIdx - (srcPortCheck + 1)), SkipFalse: 0,
		})
		prog = append(prog, bpf.LoadIndirect{Off: 16, Size: 2}) // dst port
		prog = append(prog, bpf.JumpIf{
			Cond: bpf.JumpEqual, Val: portVal,
			SkipTrue: 0, SkipFalse: uint8(rejectIdx - (dstPortCheck + 1)),
		})
	}

	prog = append(prog, bpf.RetConstant{Val: 0xffff}) // ACCEPT: keep the whole frame
	prog = append(prog, bpf.RetConstant{Val: 0})       // REJECT: drop it

	raw, err := bpf.Assemble(prog)
	if err != nil {
		// A bug in the offsets above, not an operator input error. Fail closed
		// to an unfiltered capture rather than crashing the daemon over it.
		return nil
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	return filter
}

// attachFilter installs a classic BPF program on the raw socket via
// SO_ATTACH_FILTER, so the kernel drops non-matching frames before they ever
// reach this process.
func attachFilter(fd int, filter []unix.SockFilter) error {
	prog := unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}
	return unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &prog)
}

// uploadCapture puts the finished pcap file to S3 under this sandbox's own
// captures/ prefix, using the instance role. Best-effort: the caller keeps the
// local file and reports the error on failure — the pcap on disk is the
// deliverable, S3 is a convenience, and losing a capture because an upload
// failed would defeat the point of running one.
func uploadCapture(path string, o opts) (string, error) {
	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if bucket == "" || sandboxID == "" {
		return "", fmt.Errorf("KM_ARTIFACTS_BUCKET/KM_SANDBOX_ID not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("load AWS config: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	key := fmt.Sprintf("captures/%s/%s", sandboxID, filepath.Base(path))
	if _, err := s3.NewFromConfig(cfg).PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        f,
		ContentType: aws.String("application/vnd.tcpdump.pcap"),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("s3://%s/%s", bucket, key), nil
}
