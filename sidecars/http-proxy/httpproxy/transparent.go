// Package httpproxy — transparent proxy support for BPF-redirected connections.
//
// When the eBPF connect4 hook rewrites a connection's destination to
// 127.0.0.1:3128, the client sends raw TLS (not HTTP CONNECT). This file
// provides a TCP listener wrapper that detects such connections and
// recovers the original destination via pinned BPF maps.
package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klankrmkr/pkg/aws"
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

	// Budget enforcement for AI traffic metering (set via SetBudgetEnforcement)
	budget *budgetEnforcementOptions

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

// SetBudgetEnforcement enables AI token metering on transparent connections.
// Bedrock and Anthropic responses are captured and metered using the same
// extraction logic as the goproxy path.
func (tl *TransparentListener) SetBudgetEnforcement(client aws.BudgetAPI, tableName string, modelRates map[string]aws.BedrockModelRate, onBudgetUpdate BudgetUpdater) {
	tl.budget = &budgetEnforcementOptions{
		client:         client,
		tableName:      tableName,
		modelRates:     modelRates,
		cache:          NewBudgetCache(),
		onBudgetUpdate: onBudgetUpdate,
	}
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

	// sock_to_original_port: u64 → u16 (network byte order)
	var origPortRaw uint16
	if err := tl.sockToPort.Lookup(&cookie, &origPortRaw); err != nil {
		return nil, 0, fmt.Errorf("sock_to_original_port lookup cookie %d: %w", cookie, err)
	}
	// Convert from NBO to host byte order
	origPort := (origPortRaw >> 8) | (origPortRaw << 8)

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
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("transparent: recovered from panic")
		}
	}()

	// Peek at first byte to determine protocol
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		conn.Close()
		return
	}

	peekedConn := &peekedConn{Conn: conn, reader: br}

	if first[0] == 0x16 {
		// TLS ClientHello — this is a BPF-redirected transparent connection.
		// handleTransparent owns the connection lifecycle (TLS Close cascades).
		defer conn.Close()
		tl.handleTransparent(peekedConn)
	} else {
		// HTTP request (CONNECT or plain) — pass to goproxy via http.Server.
		// Do NOT defer conn.Close() here: goproxy hijacks the connection for
		// CONNECT tunnels (MITM) and closes it when the tunnel completes.
		// Closing prematurely kills the MITM TLS handshake mid-flight.
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
	// Generate a leaf cert for the destination host signed by the platform CA.
	// We use tls.Config with GetCertificate to lazily generate based on SNI.
	tlsCfg := &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := hello.ServerName
			if host == "" {
				host = origIP.String()
			}
			return signHost(goproxy.GoproxyCa, host)
		},
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

	// Decrypted streams on both sides — relay with L7 inspection for
	// GitHub repo filtering and AI budget metering.
	tl.relayWithInspection(clientTLS, destTLS, origHost)
}

// relayWithInspection reads HTTP requests from the client TLS stream,
// checks them against the proxy's repo filter / budget rules, and forwards
// allowed requests to the destination. Bedrock and Anthropic responses are
// captured for token metering when budget enforcement is enabled.
func (tl *TransparentListener) relayWithInspection(client, dest net.Conn, origHost string) {
	clientReader := bufio.NewReader(client)
	destReader := bufio.NewReader(dest)

	// Determine host type once per connection for metering decisions.
	host, _, _ := net.SplitHostPort(origHost)
	if host == "" {
		host = origHost
	}
	isBedrock := bedrockHostRegex.MatchString(host)
	isAnthropic := anthropicHostRegex.MatchString(host)

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

		// Budget pre-flight: reject if AI budget is exhausted.
		if tl.budget != nil && (isBedrock || isAnthropic) {
			entry := tl.budget.cache.Get(tl.sandboxID)
			if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
				log.Info().
					Str("sandbox_id", tl.sandboxID).
					Str("event_type", "ai_budget_exhausted_preflight").
					Str("host", req.Host).
					Str("mode", "transparent").
					Float64("spent", entry.AISpent).
					Float64("limit", entry.AILimit).
					Msg("")
				var blocked *http.Response
				if isBedrock {
					modelID := ExtractModelID(req.URL.Path)
					blocked = BedrockBlockedResponse(req, tl.sandboxID, modelID, entry.AISpent, entry.AILimit)
				} else {
					blocked = AnthropicBlockedResponse(req, tl.sandboxID, "", entry.AISpent, entry.AILimit)
				}
				_ = blocked.Write(client)
				blocked.Body.Close()
				return
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

		// Meter AI traffic: capture response body for token extraction.
		if tl.budget != nil && isBedrock {
			tl.meterBedrockResponse(resp, req)
		} else if tl.budget != nil && isAnthropic {
			tl.meterAnthropicResponse(resp, req)
		}

		// Write the response back to the client
		if err := resp.Write(client); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()
	}
}

// meterBedrockResponse wraps a Bedrock response body with a metering reader
// that extracts tokens and writes spend to DynamoDB on EOF.
func (tl *TransparentListener) meterBedrockResponse(resp *http.Response, req *http.Request) {
	be := tl.budget
	modelID := ExtractModelID(req.URL.Path)
	resp.Body = newMeteringReader(resp.Body, func(captured []byte) {
		inputTokens, outputTokens, parseErr := ExtractBedrockTokens(bytes.NewReader(captured))
		if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
			return
		}

		var costUSD float64
		if rate, ok := be.modelRates[modelID]; ok {
			costUSD = CalculateCost(inputTokens, outputTokens, rate.InputPricePer1KTokens, rate.OutputPricePer1KTokens)
		}

		log.Info().
			Str("sandbox_id", tl.sandboxID).
			Str("event_type", "bedrock_tokens_metered").
			Str("model", modelID).
			Str("mode", "transparent").
			Int("input_tokens", inputTokens).
			Int("output_tokens", outputTokens).
			Float64("cost_usd", costUSD).
			Msg("")

		tl.updateBudgetSpend(be, modelID, inputTokens, outputTokens, costUSD)
	})
}

// meterAnthropicResponse wraps an Anthropic response body with a metering reader
// that extracts tokens and writes spend to DynamoDB on EOF.
func (tl *TransparentListener) meterAnthropicResponse(resp *http.Response, req *http.Request) {
	be := tl.budget
	resp.Body = newMeteringReader(resp.Body, func(captured []byte) {
		modelID, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, parseErr := ExtractAnthropicTokens(bytes.NewReader(captured))
		if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
			return
		}

		var costUSD float64
		if rate, ok := staticAnthropicRates[modelID]; ok {
			costUSD = CalculateAnthropicCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, rate)
		}

		log.Info().
			Str("sandbox_id", tl.sandboxID).
			Str("event_type", "anthropic_tokens_metered").
			Str("model", modelID).
			Str("mode", "transparent").
			Int("input_tokens", inputTokens).
			Int("output_tokens", outputTokens).
			Int("cache_read_tokens", cacheReadTokens).
			Int("cache_write_tokens", cacheWriteTokens).
			Float64("cost_usd", costUSD).
			Msg("")

		tl.updateBudgetSpend(be, modelID, inputTokens, outputTokens, costUSD)
	})
}

// updateBudgetSpend writes metered AI spend to DynamoDB and updates the local cache.
func (tl *TransparentListener) updateBudgetSpend(be *budgetEnforcementOptions, modelID string, inputTokens, outputTokens int, costUSD float64) {
	be.cache.UpdateLocalSpend(tl.sandboxID, costUSD)

	updatedSpend, err := aws.IncrementAISpend(
		context.Background(),
		be.client,
		be.tableName,
		tl.sandboxID,
		modelID,
		inputTokens,
		outputTokens,
		costUSD,
	)
	if err != nil {
		log.Error().
			Str("sandbox_id", tl.sandboxID).
			Str("event_type", "ai_spend_increment_error").
			Str("mode", "transparent").
			Err(err).
			Msg("")
		return
	}

	cachedEntry := be.cache.Get(tl.sandboxID)
	if cachedEntry != nil {
		cachedEntry.AISpent = updatedSpend
		be.cache.Set(tl.sandboxID, cachedEntry)
	}

	if be.onBudgetUpdate != nil {
		limit := float64(0)
		if cachedEntry != nil {
			limit = cachedEntry.AILimit
		}
		remaining := limit - updatedSpend
		be.onBudgetUpdate(remaining)
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


// CheckBPFMapsExist returns true if the BPF maps are pinned and accessible.
func CheckBPFMapsExist(sandboxID string) bool {
	mapDir := fmt.Sprintf("/sys/fs/bpf/km/%s/", sandboxID)
	_, err := os.Stat(mapDir + "src_port_to_sock")
	return err == nil
}

// signHost generates a leaf TLS certificate for host, signed by the CA.
func signHost(ca tls.Certificate, host string) (*tls.Certificate, error) {
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	parent, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return nil, err
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, err
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{certDER, ca.Certificate[0]},
		PrivateKey:  key,
	}
	return cert, nil
}
