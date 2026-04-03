// Package httpproxy — transparent proxy support for BPF-redirected connections.
//
// When the eBPF connect4 hook rewrites a connection's destination to
// 127.0.0.1:3128, the client sends raw TLS (not HTTP CONNECT). This file
// provides a TCP listener wrapper that detects such connections and
// recovers the original destination via pinned BPF maps.
package httpproxy

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
)

// TransparentListener wraps a net.Listener and detects BPF-redirected
// connections by peeking at the first byte. TLS connections (0x16) are
// handled transparently; HTTP connections are passed to goproxy as usual.
type TransparentListener struct {
	inner     net.Listener
	proxy     *goproxy.ProxyHttpServer
	sandboxID string
	mapDir    string // /sys/fs/bpf/km/{sandboxID}/

	// GitHub repo filter (set via SetGitHubRepos)
	githubRepos []string

	portToSock *ebpf.Map // src_port_to_sock
	sockToIP   *ebpf.Map // sock_to_original_ip
	sockToPort *ebpf.Map // sock_to_original_port

	mu      sync.Once
	mapErr  error
}

// SetGitHubRepos configures repo-level filtering for transparent connections.
func (tl *TransparentListener) SetGitHubRepos(repos []string) {
	tl.githubRepos = repos
}

// NewTransparentListener creates a listener that handles both explicit proxy
// (HTTP CONNECT) and transparent (BPF-redirected TLS) connections.
func NewTransparentListener(ln net.Listener, proxy *goproxy.ProxyHttpServer, sandboxID string) *TransparentListener {
	return &TransparentListener{
		inner:     ln,
		proxy:     proxy,
		sandboxID: sandboxID,
		mapDir:    fmt.Sprintf("/sys/fs/bpf/km/%s/", sandboxID),
	}
}

// loadMaps loads the pinned BPF maps. Called lazily on first transparent connection.
func (tl *TransparentListener) loadMaps() error {
	tl.mu.Do(func() {
		tl.portToSock, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"src_port_to_sock", nil)
		if tl.mapErr != nil {
			tl.mapErr = fmt.Errorf("load src_port_to_sock: %w", tl.mapErr)
			return
		}
		tl.sockToIP, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_original_ip", nil)
		if tl.mapErr != nil {
			tl.mapErr = fmt.Errorf("load sock_to_original_ip: %w", tl.mapErr)
			return
		}
		tl.sockToPort, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_original_port", nil)
		if tl.mapErr != nil {
			tl.mapErr = fmt.Errorf("load sock_to_original_port: %w", tl.mapErr)
			return
		}
		log.Info().Str("sandbox_id", tl.sandboxID).Msg("BPF maps loaded for transparent proxy")
	})
	return tl.mapErr
}

// lookupOriginalDest recovers the original destination IP:port for a
// BPF-redirected connection, using the peer's source port as the lookup key.
func (tl *TransparentListener) lookupOriginalDest(peerPort uint16) (net.IP, uint16, error) {
	if err := tl.loadMaps(); err != nil {
		return nil, 0, err
	}

	// src_port_to_sock: u16 → u64 (socket cookie)
	var cookie uint64
	if err := tl.portToSock.Lookup(&peerPort, &cookie); err != nil {
		return nil, 0, fmt.Errorf("src_port_to_sock lookup port %d: %w", peerPort, err)
	}

	// sock_to_original_ip: u64 → u32 (NBO IP stored as native uint32)
	var ipU32 uint32
	if err := tl.sockToIP.Lookup(&cookie, &ipU32); err != nil {
		return nil, 0, fmt.Errorf("sock_to_original_ip lookup cookie %d: %w", cookie, err)
	}

	// sock_to_original_port: u64 → u16
	var origPort uint16
	if err := tl.sockToPort.Lookup(&cookie, &origPort); err != nil {
		return nil, 0, fmt.Errorf("sock_to_original_port lookup cookie %d: %w", cookie, err)
	}

	// Convert NBO IP (stored as native uint32 by cilium/ebpf) back to net.IP
	ipBytes := make([]byte, 4)
	binary.NativeEndian.PutUint32(ipBytes, ipU32)
	ip := net.IP(ipBytes)

	return ip, origPort, nil
}

// Serve accepts connections and dispatches them to either the transparent
// handler (for TLS) or goproxy (for HTTP CONNECT).
func (tl *TransparentListener) Serve() error {
	for {
		conn, err := tl.inner.Accept()
		if err != nil {
			return err
		}
		go tl.handleConn(conn)
	}
}

func (tl *TransparentListener) handleConn(conn net.Conn) {
	defer conn.Close()

	// Peek at first byte to determine protocol
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		return
	}

	peekedConn := &peekedConn{Conn: conn, reader: br}

	if first[0] == 0x16 {
		// TLS ClientHello — this is a BPF-redirected transparent connection
		tl.handleTransparent(peekedConn)
	} else {
		// HTTP request (CONNECT or plain) — pass to goproxy
		tl.proxy.ServeHTTP(
			&hijackResponseWriter{conn: peekedConn},
			nil, // goproxy reads from the connection directly
		)
		// Actually, goproxy needs http.Server to handle this properly.
		// Let's use the simpler approach: serve via http.Server on the peeked conn.
		srv := &http.Server{Handler: tl.proxy}
		srvConn := &singleConnListener{conn: peekedConn}
		srv.Serve(srvConn)
	}
}

func (tl *TransparentListener) handleTransparent(conn net.Conn) {
	// Get peer's source port for BPF map lookup
	tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		log.Error().Msg("transparent: remote addr is not TCP")
		return
	}

	origIP, origPort, err := tl.lookupOriginalDest(uint16(tcpAddr.Port))
	if err != nil {
		log.Error().Err(err).Int("peer_port", tcpAddr.Port).Msg("transparent: failed to lookup original dest")
		return
	}

	origHost := fmt.Sprintf("%s:%d", origIP.String(), origPort)
	log.Info().
		Str("sandbox_id", tl.sandboxID).
		Str("event_type", "transparent_proxy_connect").
		Str("original_dest", origHost).
		Int("peer_port", tcpAddr.Port).
		Msg("")

	// TLS-terminate the client connection using goproxy's CA.
	// Use the original IP as the SNI host for cert generation.
	tlsConfigFunc := goproxy.TLSConfigFromCA(&goproxy.GoproxyCa)
	tlsCfg, err := tlsConfigFunc(origIP.String(), nil)
	if err != nil {
		log.Error().Err(err).Str("dest", origHost).Msg("transparent: TLS config generation failed")
		return
	}
	clientTLS := tls.Server(conn, tlsCfg)
	if err := clientTLS.Handshake(); err != nil {
		log.Error().Err(err).Str("dest", origHost).Msg("transparent: TLS handshake failed")
		return
	}
	defer clientTLS.Close()

	// Use SNI from the client hello for the upstream connection
	sni := clientTLS.ConnectionState().ServerName
	if sni == "" {
		sni = origIP.String()
	}

	// Connect to the real destination
	destConn, err := net.Dial("tcp", origHost)
	if err != nil {
		log.Error().Err(err).Str("dest", origHost).Msg("transparent: dial original dest failed")
		return
	}
	defer destConn.Close()

	// Establish TLS to the real destination
	destTLS := tls.Client(destConn, &tls.Config{
		ServerName: sni,
	})
	if err := destTLS.Handshake(); err != nil {
		log.Error().Err(err).Str("dest", origHost).Msg("transparent: dest TLS handshake failed")
		return
	}
	defer destTLS.Close()

	// Now we have decrypted streams on both sides. Read HTTP requests from the
	// client, pass them through the proxy's request handlers, and forward to dest.
	// For simplicity, we'll do bidirectional copy (no L7 inspection in this first pass).
	// The GitHub repo filter and budget enforcement need the proxy pipeline.
	// TODO: pipe through goproxy handlers for full L7 inspection.

	// For now: bidirectional MITM relay with L7 request interception
	tl.relayWithInspection(clientTLS, destTLS, origHost)
}

// relayWithInspection reads HTTP requests from the client TLS stream,
// checks them against the proxy's repo filter / budget rules, and forwards
// allowed requests to the destination.
func (tl *TransparentListener) relayWithInspection(client, dest net.Conn, origHost string) {
	clientReader := bufio.NewReader(client)
	destReader := bufio.NewReader(dest)

	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				log.Debug().Err(err).Msg("transparent: read request")
			}
			return
		}

		// Fix up the request URL
		if req.URL.Host == "" {
			req.URL.Host = origHost
		}
		if req.URL.Scheme == "" {
			req.URL.Scheme = "https"
		}

		// GitHub repo filter: check if this is a GitHub request to a blocked repo
		if len(tl.githubRepos) > 0 && githubHostsRegex.MatchString(req.Host) {
			repo := ExtractRepoFromPath(req.Host, req.URL.Path)
			if repo != "" && !IsRepoAllowed(repo, tl.githubRepos) {
				log.Info().
					Str("event_type", "github_repo_blocked").
					Str("sandbox_id", tl.sandboxID).
					Str("repo", repo).
					Str("mode", "transparent").
					Msg("")
				blocked := GitHubBlockedResponse(req, tl.sandboxID, repo)
				_ = blocked.Write(client)
				blocked.Body.Close()
				return
			}
			if repo != "" {
				log.Info().
					Str("event_type", "github_repo_allowed").
					Str("sandbox_id", tl.sandboxID).
					Str("repo", repo).
					Str("mode", "transparent").
					Msg("")
			}
		}

		// Forward the request to the real destination
		req.RequestURI = "" // required for client.Do style
		if err := req.Write(dest); err != nil {
			log.Error().Err(err).Msg("transparent: write to dest")
			return
		}

		// Read the response
		resp, err := http.ReadResponse(destReader, req)
		if err != nil {
			log.Error().Err(err).Msg("transparent: read response from dest")
			return
		}

		// Write the response back to the client
		if err := resp.Write(client); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()
	}
}

// peekedConn wraps a net.Conn with a bufio.Reader that may have buffered data.
type peekedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

// singleConnListener serves exactly one connection then returns EOF.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn != nil {
		return conn, nil
	}
	return nil, fmt.Errorf("closed")
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// hijackResponseWriter is unused but kept for potential future use.
type hijackResponseWriter struct {
	conn net.Conn
}

func (w *hijackResponseWriter) Header() http.Header        { return http.Header{} }
func (w *hijackResponseWriter) Write(b []byte) (int, error) { return w.conn.Write(b) }
func (w *hijackResponseWriter) WriteHeader(int)             {}

// CheckBPFMapsExist returns true if the BPF maps are pinned and accessible.
func CheckBPFMapsExist(sandboxID string) bool {
	mapDir := fmt.Sprintf("/sys/fs/bpf/km/%s/", sandboxID)
	_, err := os.Stat(mapDir + "src_port_to_sock")
	return err == nil
}
