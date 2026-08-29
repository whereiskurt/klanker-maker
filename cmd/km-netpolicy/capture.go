package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Hard ceilings. These are not defaults an operator can raise with a flag: an
// unbounded capture on a sandbox with a 30GB data volume fills the disk and
// takes the box down, and a capture is a diagnostic, not a recording studio.
const (
	maxCaptureDuration     = 60 * time.Minute
	maxCaptureSize         = int64(2) << 30 // 2 GiB
	defaultCaptureDuration = 5 * time.Minute
	defaultCaptureSize     = int64(250) << 20
)

// DefaultCaptureSock is where the root daemon listens. Mode 0660, group
// sandbox, so an unprivileged agent can request a capture without ever holding
// CAP_NET_RAW itself.
const DefaultCaptureSock = "/run/km/capture.sock"

// DefaultCaptureDir is where finished captures land before upload.
const DefaultCaptureDir = "/var/lib/km/capture"

// captureReq is the client→daemon message.
type captureReq struct {
	Op       string `json:"op"` // "start" | "stop" | "status" | "list"
	Duration string `json:"duration,omitempty"`
	MaxSize  int64  `json:"max_size,omitempty"`
	Port     int    `json:"port,omitempty"`
	Host     string `json:"host,omitempty"`
}

// captureResp is the daemon→client reply.
type captureResp struct {
	OK    bool     `json:"ok"`
	Msg   string   `json:"msg,omitempty"`
	Files []string `json:"files,omitempty"`
	S3URI string   `json:"s3_uri,omitempty"`
}

// runCapture is the unprivileged client. It validates bounds locally so an
// obviously-wrong request never reaches the privileged daemon.
func runCapture(args []string, o opts) int {
	if len(args) == 0 {
		fmt.Fprintf(o.stderr, "%s capture: needs a subverb (start|stop|status|list)\n", prog)
		return 2
	}

	req := captureReq{Op: args[0]}
	dur := defaultCaptureDuration
	size := defaultCaptureSize

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--duration", "--max-size", "--port", "--host":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s capture: %s needs a value\n", prog, args[i])
				return 2
			}
			flag, val := args[i], args[i+1]
			i++
			switch flag {
			case "--duration":
				d, err := time.ParseDuration(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a duration: %v\n", prog, val, err)
					return 2
				}
				dur = d
			case "--max-size":
				n, err := parseSize(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a size: %v\n", prog, val, err)
					return 2
				}
				size = n
			case "--port":
				n, err := strconv.Atoi(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a port\n", prog, val)
					return 2
				}
				req.Port = n
			case "--host":
				req.Host = val
			}
		default:
			fmt.Fprintf(o.stderr, "%s capture: unknown flag %q\n", prog, args[i])
			return 2
		}
	}

	switch req.Op {
	case "start":
		if dur <= 0 {
			fmt.Fprintf(o.stderr, "%s capture: --duration must be positive; there is no unbounded capture\n", prog)
			return 2
		}
		if dur > maxCaptureDuration {
			fmt.Fprintf(o.stderr, "%s capture: --duration %s exceeds the hard ceiling of %s\n",
				prog, dur, maxCaptureDuration)
			return 2
		}
		if size <= 0 || size > maxCaptureSize {
			fmt.Fprintf(o.stderr, "%s capture: --max-size must be between 1 and %d bytes\n", prog, maxCaptureSize)
			return 2
		}
		req.Duration = dur.String()
		req.MaxSize = size
	case "stop", "status", "list":
		// no bounds to check
	default:
		fmt.Fprintf(o.stderr, "%s capture: unknown subverb %q (want start|stop|status|list)\n", prog, req.Op)
		return 2
	}

	resp, err := captureRPC(o.captureSock, req)
	if err != nil {
		fmt.Fprintf(o.stderr,
			"%s capture: cannot reach the capture daemon at %s: %v\n"+
				"  Check: systemctl status km-capture\n", prog, o.captureSock, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(o.stderr, "%s capture: %s\n", prog, resp.Msg)
		return 1
	}
	if resp.Msg != "" {
		fmt.Fprintln(o.stdout, resp.Msg)
	}
	for _, f := range resp.Files {
		fmt.Fprintf(o.stdout, "  %s\n", f)
	}
	if resp.S3URI != "" {
		fmt.Fprintf(o.stdout, "uploaded: %s\n", resp.S3URI)
	}
	return 0
}

// captureRPC sends one request over the Unix socket and reads one reply.
func captureRPC(sock string, req captureReq) (captureResp, error) {
	var resp captureResp
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return resp, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, err
	}
	err = json.NewDecoder(conn).Decode(&resp)
	return resp, err
}

// parseSize accepts a byte count with an optional KB/MB/GB suffix (binary
// multiples, matching how operators think about disk).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "GB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GB")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}
