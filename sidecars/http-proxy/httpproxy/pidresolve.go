// Package httpproxy — pidresolve.go
//
// Attributes an accepted proxy connection to the PID that opened it, so flow
// records (see recordFlow in proxy.go) can join with the process-execution
// trace `km-netpolicy who <host>` reads.
//
// Why this lives here rather than relying on the eBPF ring-buffer events
// that already carry a PID: the sandbox user's HTTPS_PROXY env var sends
// traffic to this proxy over loopback *explicitly*, so the eBPF connect4
// program only ever observes a loopback connect — never a deny or a
// redirect — and emits no event at all. The PID has to come from the
// component that actually terminates the agent's connection: this proxy.
//
// The mechanism reuses the same pinned BPF maps transparent.go already
// consults for original-destination recovery (see loadMaps there), except it
// walks the OTHER direction: src_port_to_sock (local TCP source port →
// socket cookie, populated by the sockops program) then socket_pid_map
// (socket cookie → PID, populated by connect4 on every invocation, before
// any allow/deny decision — so it covers a proxied connection too).
package httpproxy

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/rs/zerolog/log"
)

// pidLookupMap is the subset of *ebpf.Map's interface pidResolver needs. It
// exists so the lookup logic (lookupPID) can be unit-tested with a fake,
// without a kernel or root.
type pidLookupMap interface {
	Lookup(key, valueOut interface{}) error
}

// pidResolver resolves an accepted TCP connection's local source port to the
// PID that owns the underlying socket, via pinned BPF maps.
//
// Resolution is best-effort and MUST NEVER be load-bearing for the egress
// path: an absent pin directory (pure `proxy` enforcement pins no BPF maps
// at all), a map miss, or any lookup error all resolve to PID 0 ("could not
// attribute"), silently. Map handles are cached after the first successful
// (or failed) load — never reopened per request.
type pidResolver struct {
	mapDir string

	loadOnce sync.Once
	loadErr  error

	portToSock pidLookupMap // src_port_to_sock: u16 port -> u64 cookie
	sockToPid  pidLookupMap // socket_pid_map:   u64 cookie -> u32 pid

	warnOnce sync.Once
}

// newPIDResolver returns a resolver for sandboxID's pinned BPF map directory.
// Construction never touches the filesystem or the kernel — maps are loaded
// lazily on first resolve call.
func newPIDResolver(sandboxID string) *pidResolver {
	return &pidResolver{mapDir: fmt.Sprintf("/sys/fs/bpf/km/%s/", sandboxID)}
}

// loadMaps loads and caches the pinned maps this resolver needs, exactly
// once. A failure (maps not pinned — e.g. proxy-only enforcement, or the
// enforcer hasn't started yet) is cached too, so every subsequent call is a
// cheap no-op rather than a repeated failing syscall.
func (r *pidResolver) loadMaps() error {
	r.loadOnce.Do(func() {
		portToSock, err := ebpf.LoadPinnedMap(r.mapDir+"src_port_to_sock", nil)
		if err != nil {
			r.loadErr = fmt.Errorf("load src_port_to_sock: %w", err)
			return
		}
		sockToPid, err := ebpf.LoadPinnedMap(r.mapDir+"socket_pid_map", nil)
		if err != nil {
			r.loadErr = fmt.Errorf("load socket_pid_map: %w", err)
			return
		}
		r.portToSock = portToSock
		r.sockToPid = sockToPid
	})
	return r.loadErr
}

// resolve returns the PID that owns the local TCP source port, or 0 if it
// cannot be determined for any reason. A load failure is logged at most
// once per process lifetime — never per connection, or a box running pure
// `proxy` enforcement (no BPF maps at all) would log forever.
func (r *pidResolver) resolve(port uint16) int {
	if r == nil {
		return 0
	}
	if err := r.loadMaps(); err != nil {
		r.warnOnce.Do(func() {
			log.Warn().
				Err(err).
				Str("event_type", "http_proxy_pid_resolution_unavailable").
				Msg("flow PID attribution disabled: pinned BPF maps not found (expected under proxy-only enforcement)")
		})
		return 0
	}
	return lookupPID(r.portToSock, r.sockToPid, port)
}

// resolveFromRemoteAddr resolves the PID for the accepted connection whose
// remote address (as seen by net/http, i.e. "ip:port") is remoteAddr. Any
// parse failure resolves to 0.
func (r *pidResolver) resolveFromRemoteAddr(remoteAddr string) int {
	if r == nil || remoteAddr == "" {
		return 0
	}
	_, portStr, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return 0
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0
	}
	return r.resolve(uint16(p))
}

// lookupPID performs the two-hop map lookup: local port -> socket cookie ->
// PID. Factored out of pidResolver.resolve so it is testable against fake
// maps with no kernel involved. A miss or an error at either hop returns 0,
// never propagated.
func lookupPID(portToSock, sockToPid pidLookupMap, port uint16) int {
	if portToSock == nil || sockToPid == nil {
		return 0
	}
	var cookie uint64
	if err := portToSock.Lookup(&port, &cookie); err != nil {
		return 0
	}
	var pid uint32
	if err := sockToPid.Lookup(&cookie, &pid); err != nil {
		return 0
	}
	return int(pid)
}

// commForPID best-effort reads the command name for pid from /proc. Returns
// "" on any failure (process gone, /proc unavailable, pid<=0) — never an
// error, since Comm is a nice-to-have alongside PID, not a requirement.
func commForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
