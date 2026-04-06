//go:build linux && amd64

// Package cmd provides the Cobra command tree for the km CLI.
package cmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/audit"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/resolver"
	ebpftls "github.com/whereiskurt/klankrmkr/pkg/ebpf/tls"
)

// NewEBPFAttachCmd creates the "km ebpf-attach" subcommand.
// This is an internal command invoked by EC2 user-data bootstrap scripts.
// It orchestrates the full eBPF enforcement setup:
//  1. Create enforcer (load BPF programs, attach to sandbox cgroup, pin to bpffs)
//  2. Pre-populate CIDR allowlist (IMDS, VPC)
//  3. Start DNS resolver daemon
//  4. Start ring buffer audit consumer
//  5. Wait for SIGTERM/SIGINT
//  6. Graceful shutdown: close consumer, stop resolver, close enforcer
func NewEBPFAttachCmd(cfg *config.Config) *cobra.Command {
	var (
		sandboxID     string
		dnsPort       uint32
		httpPort      uint32
		firewallMode  string
		allowedDNS    string
		allowedHosts  string
		proxyHosts    string
		cgroupPath    string
		enableTLS     bool
		allowedRepos  string
		httpProxyPID  uint32
		observe       bool
		observeOutput string
	)

	cmd := &cobra.Command{
		Use:    "ebpf-attach",
		Short:  "Attach eBPF network enforcement to sandbox cgroup",
		Long:   "Loads BPF programs, attaches to sandbox cgroup, starts DNS resolver, and runs audit consumer. Invoked by EC2 user-data bootstrap.",
		Hidden: true, // internal command, not user-facing
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEbpfAttach(sandboxID, dnsPort, httpPort, firewallMode,
				allowedDNS, allowedHosts, proxyHosts, cgroupPath,
				enableTLS, allowedRepos, httpProxyPID, observe, observeOutput)
		},
	}

	cmd.Flags().StringVar(&sandboxID, "sandbox-id", "", "Sandbox ID to enforce (required)")
	_ = cmd.MarkFlagRequired("sandbox-id")

	cmd.Flags().Uint32Var(&dnsPort, "dns-port", 5353, "UDP port for DNS resolver proxy")
	cmd.Flags().Uint32Var(&httpPort, "http-proxy-port", 3128, "TCP port for HTTP/HTTPS proxy")
	cmd.Flags().StringVar(&firewallMode, "firewall-mode", "block",
		"Firewall enforcement mode: log (emit events only), allow (permissive), block (enforce allowlist)")
	cmd.Flags().StringVar(&allowedDNS, "allowed-dns", "",
		"Comma-separated DNS domain suffixes to allow (e.g. github.com,amazonaws.com)")
	cmd.Flags().StringVar(&allowedHosts, "allowed-hosts", "",
		"Comma-separated hosts for L7 proxy allowlist")
	cmd.Flags().StringVar(&proxyHosts, "proxy-hosts", "",
		"Comma-separated hosts whose resolved IPs are redirected to L7 proxy")
	cmd.Flags().StringVar(&cgroupPath, "cgroup", "",
		"Override cgroup path (default: auto-detected from sandbox ID)")
	cmd.Flags().BoolVar(&enableTLS, "tls", false,
		"Enable TLS uprobe observability (attaches to libssl.so.3)")
	cmd.Flags().StringVar(&allowedRepos, "allowed-repos", "",
		"Comma-separated list of allowed GitHub repos (owner/repo)")
	cmd.Flags().Uint32Var(&httpProxyPID, "proxy-pid", 0,
		"PID of the HTTP proxy process to exempt from BPF interception (gatekeeper mode); 0 = disabled")
	cmd.Flags().BoolVar(&observe, "observe", false,
		"Enable learning mode: record observed DNS/TLS traffic and write profile JSON on shutdown")
	cmd.Flags().StringVar(&observeOutput, "observe-output", "/tmp/km-observed.json",
		"Local path to write observed JSON on shutdown (used with --observe)")

	return cmd
}

// parsefirewallMode converts a mode string to the BPF uint16 constant.
func parseFirewallMode(mode string) (uint16, error) {
	switch strings.ToLower(mode) {
	case "log":
		return ebpf.ModeLog, nil
	case "allow":
		return ebpf.ModeAllow, nil
	case "block", "":
		return ebpf.ModeBlock, nil
	default:
		return 0, fmt.Errorf("unknown firewall-mode %q: use log, allow, or block", mode)
	}
}

// ipToUint32 converts an IPv4 address to a uint32 matching how the kernel
// stores network-byte-order IP bytes as a native __u32. On x86 (LE),
// 127.0.0.1 in NBO bytes {0x7f,0x00,0x00,0x01} becomes __u32 = 0x0100007f.
// This is needed because BPF ctx->user_ip4 uses this representation.
func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.NativeEndian.Uint32(ip4)
}

// observedState is the JSON serialisation format for a completed learn session.
// It is written locally by ebpf-attach --observe and uploaded to S3, then
// consumed by km shell --learn on the operator's host to generate a profile.
type observedState struct {
	DNS   []string `json:"dns"`
	Hosts []string `json:"hosts"`
	Repos []string `json:"repos"`
}

func runEbpfAttach(
	sandboxID string,
	dnsPort, httpPort uint32,
	firewallMode string,
	allowedDNS, allowedHosts, proxyHosts string,
	cgroupOverride string,
	enableTLS bool,
	allowedRepos string,
	httpProxyPID uint32,
	observe bool,
	observeOutput string,
) error {
	logger := log.With().Str("sandbox_id", sandboxID).Logger()

	fwMode, err := parseFirewallMode(firewallMode)
	if err != nil {
		return err
	}

	// Learning mode: create a Recorder to accumulate all observed traffic.
	var recorder *allowlistgen.Recorder
	if observe {
		recorder = allowlistgen.NewRecorder()
		logger.Info().Str("output", observeOutput).Msg("observe mode: learning mode enabled")
	}

	// 127.0.0.1 in network byte order.
	mitmAddr := ipToUint32(net.ParseIP("127.0.0.1"))

	cfg := ebpf.Config{
		SandboxID:      sandboxID,
		DNSProxyPort:   dnsPort,
		HTTPProxyPort:  httpPort,
		HTTPSProxyPort: httpPort,
		ProxyPID:       uint32(os.Getpid()),
		HTTPProxyPID:   httpProxyPID,
		FirewallMode:   fwMode,
		MITMProxyAddr:  mitmAddr,
	}

	// In block mode, the HTTP proxy must be exempt or its outbound connections
	// to allowed hosts get redirected back to itself (infinite redirect loop).
	// Warn if --proxy-pid is not set in block mode.
	if httpProxyPID == 0 && fwMode == ebpf.ModeBlock {
		logger.Warn().Msg("no --proxy-pid set in block mode; HTTP proxy may experience redirect loops")
	}

	logger.Info().
		Str("firewall_mode", firewallMode).
		Uint32("dns_port", dnsPort).
		Uint32("http_port", httpPort).
		Msg("loading eBPF enforcer")

	enforcer, err := ebpf.NewEnforcer(cfg)
	if err != nil {
		return fmt.Errorf("create enforcer: %w", err)
	}
	defer enforcer.Close()

	// Pre-populate CIDR allowlist with infrastructure addresses.
	// IMDS endpoint is always required for AWS metadata access.
	imdsIP := net.ParseIP("169.254.169.254")
	if err := enforcer.AllowIP(imdsIP); err != nil {
		logger.Warn().Err(err).Msg("failed to allow IMDS IP (non-fatal)")
	}

	// VPC DNS resolver — glibc's stub resolver connects to this for DNS queries.
	// Without this, DNS resolution itself is blocked by connect4 before sendmsg4
	// can intercept the query. 169.254.169.253 is the standard AWS VPC DNS.
	vpcDNS := net.ParseIP("169.254.169.253")
	if err := enforcer.AllowIP(vpcDNS); err != nil {
		logger.Warn().Err(err).Msg("failed to allow VPC DNS IP (non-fatal)")
	}

	// Allow VPC CIDR — 10.0.0.0/8 covers standard AWS VPC ranges.
	if err := enforcer.AllowCIDR("10.0.0.0/8"); err != nil {
		logger.Warn().Err(err).Msg("failed to allow VPC CIDR (non-fatal)")
	}

	// Allow link-local — 169.254.0.0/16 covers IMDS, VPC DNS, and other AWS services.
	if err := enforcer.AllowCIDR("169.254.0.0/16"); err != nil {
		logger.Warn().Err(err).Msg("failed to allow link-local CIDR (non-fatal)")
	}

	// Parse allowed DNS suffixes.
	var dnsSuffixes []string
	if allowedDNS != "" {
		for _, s := range strings.Split(allowedDNS, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				dnsSuffixes = append(dnsSuffixes, s)
			}
		}
	}

	// Parse proxy host suffixes.
	var proxyHostList []string
	if proxyHosts != "" {
		for _, s := range strings.Split(proxyHosts, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				proxyHostList = append(proxyHostList, s)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start DNS resolver daemon (skip if dnsPort == 0, i.e. "both" mode
	// where the existing km-dns-proxy sidecar handles DNS).
	resolverErrCh := make(chan error, 1)
	if dnsPort > 0 {
		listenAddr := fmt.Sprintf("127.0.0.1:%d", dnsPort)
		resolverCfg := resolver.ResolverConfig{
			ListenAddr:      listenAddr,
			UpstreamAddr:    "169.254.169.253:53",
			SandboxID:       sandboxID,
			AllowedSuffixes: dnsSuffixes,
			MapUpdater:      enforcer,
			ProxyHosts:      proxyHostList,
		}
		// Wire the recorder as a DNS observer when in learning mode.
		if recorder != nil {
			resolverCfg.DomainObserver = func(domain string, _ bool) {
				recorder.RecordDNSQuery(domain)
			}
		}
		res := resolver.NewResolver(resolverCfg)
		go func() {
			if err := res.Start(ctx); err != nil {
				resolverErrCh <- err
			}
		}()
		logger.Info().Str("listen", listenAddr).Msg("DNS resolver started")
	} else {
		logger.Info().Msg("DNS resolver skipped (both mode — km-dns-proxy handles DNS)")
	}

	// Pre-resolve all allowed hosts and DNS suffixes to seed the BPF allowlist.
	// Without this, the first connection attempt to any allowed host would fail
	// because connect4 blocks before DNS resolution can populate the trie.
	var hostsToResolve []string
	if allowedHosts != "" {
		for _, h := range strings.Split(allowedHosts, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			// Strip leading dot from DNS suffix entries (e.g. ".amazonaws.com")
			h = strings.TrimPrefix(h, ".")
			hostsToResolve = append(hostsToResolve, h)
		}
	}

	// Build proxy host set for seeding — IPs of these hosts also get MarkForProxy.
	proxyHostSet := make(map[string]bool, len(proxyHostList))
	for _, ph := range proxyHostList {
		proxyHostSet[strings.ToLower(strings.TrimPrefix(ph, "."))] = true
	}

	seeded := 0
	proxyMarked := 0
	for _, host := range hostsToResolve {
		ips, err := net.LookupHost(host)
		if err != nil {
			// Suffix entries like "amazonaws.com" won't resolve directly — that's fine
			continue
		}
		// Check if host matches any proxy host (exact or suffix).
		needsProxy := false
		hostLower := strings.ToLower(host)
		for ph := range proxyHostSet {
			if hostLower == ph || strings.HasSuffix(hostLower, "."+ph) {
				needsProxy = true
				break
			}
		}
		for _, ipStr := range ips {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			if err := enforcer.AllowIP(ip); err != nil {
				logger.Warn().Err(err).Str("host", host).Str("ip", ipStr).Msg("failed to seed IP")
			} else {
				seeded++
			}
			if needsProxy {
				if err := enforcer.MarkForProxy(ip); err != nil {
					logger.Warn().Err(err).Str("host", host).Str("ip", ipStr).Msg("failed to mark IP for proxy")
				} else {
					proxyMarked++
				}
			}
		}
	}
	logger.Info().Int("seeded_ips", seeded).Int("proxy_marked", proxyMarked).Int("hosts_resolved", len(hostsToResolve)).Msg("pre-seeded BPF allowlist from allowed hosts")

	// Start ring buffer audit consumer.
	consumer, err := audit.NewConsumer(enforcer.Events(), sandboxID, logger)
	if err != nil {
		return fmt.Errorf("create audit consumer: %w", err)
	}

	auditErrCh := make(chan error, 1)
	go func() {
		if err := consumer.Run(ctx); err != nil {
			auditErrCh <- err
		}
	}()
	logger.Info().Msg("audit consumer started")

	// TLS uprobe observability — attach to OpenSSL when --tls is enabled.
	var tlsProbe *ebpftls.OpenSSLProbe
	var tlsConsumer *ebpftls.Consumer
	var tlsCancel context.CancelFunc

	if enableTLS {
		libsslPath, err := ebpftls.FindSystemLibssl()
		if err != nil {
			logger.Warn().Err(err).Msg("libssl.so.3 not found, skipping TLS uprobe")
		} else {
			tlsProbe, err = ebpftls.AttachOpenSSL(libsslPath)
			if err != nil {
				logger.Error().Err(err).Msg("failed to attach OpenSSL uprobes")
			} else {
				logger.Info().Str("libssl", libsslPath).Msg("OpenSSL uprobes attached")

				tlsConsumer, err = ebpftls.NewConsumer(tlsProbe.EventsMap(), logger)
				if err != nil {
					logger.Error().Err(err).Msg("failed to create TLS consumer")
					tlsProbe.Close()
					tlsProbe = nil
				} else {
					// Register GitHub audit handler.
					var repos []string
					if allowedRepos != "" {
						for _, r := range strings.Split(allowedRepos, ",") {
							r = strings.TrimSpace(r)
							if r != "" {
								repos = append(repos, r)
							}
						}
					}
					ghHandler := ebpftls.NewGitHubAuditHandler(repos, logger)
					tlsConsumer.AddHandler(ghHandler.Handle)

					// Register Bedrock/Anthropic API audit handler.
					brHandler := ebpftls.NewBedrockAuditHandler(logger)
					tlsConsumer.AddHandler(brHandler.Handle)

					// Register allowlistgen recorder as TLS handler when in learning mode.
					if recorder != nil {
						tlsConsumer.AddHandler(recorder.HandleTLSEvent)
					}

					// Run TLS consumer in background.
					var tlsCtx context.Context
					tlsCtx, tlsCancel = context.WithCancel(ctx)
					tlsErrCh := make(chan error, 1)
					go func() {
						if err := tlsConsumer.Run(tlsCtx); err != nil {
							tlsErrCh <- err
						}
					}()

					logger.Info().Int("handlers", 2).Msg("TLS consumer started with handlers")
				}
			}
		}
	}

	logger.Info().Bool("tls_enabled", enableTLS).Msg("eBPF enforcement active — waiting for shutdown signal")

	// Wait for SIGTERM/SIGINT (shutdown) or SIGUSR1 (snapshot flush).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGUSR1 {
				// Snapshot flush: write current observations without shutting down.
				if recorder != nil {
					flushObservedState(recorder, sandboxID, observeOutput, logger)
				} else {
					logger.Warn().Msg("SIGUSR1 received but observe mode not enabled")
				}
				continue
			}
			logger.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		case err := <-resolverErrCh:
			logger.Error().Err(err).Msg("DNS resolver error")
		case err := <-auditErrCh:
			logger.Error().Err(err).Msg("audit consumer error")
		}
		break
	}

	logger.Info().Msg("shutting down eBPF enforcement")

	// Graceful shutdown: cancel context (stops resolver and audit consumer).
	cancel()

	// Shut down TLS uprobe resources if active.
	if tlsCancel != nil {
		tlsCancel()
	}
	if tlsConsumer != nil {
		if err := tlsConsumer.Close(); err != nil {
			logger.Debug().Err(err).Msg("TLS consumer close")
		}
	}
	if tlsProbe != nil {
		if err := tlsProbe.Close(); err != nil {
			logger.Debug().Err(err).Msg("TLS probe close")
		}
	}

	// Close audit consumer explicitly to unblock pending Read().
	if err := consumer.Close(); err != nil {
		logger.Debug().Err(err).Msg("audit consumer close")
	}

	// Observe mode: final flush on shutdown.
	if recorder != nil {
		flushObservedState(recorder, sandboxID, observeOutput, logger)
	}

	// Enforcer.Close() is deferred — it closes BPF objects.
	// Pinned programs remain on bpffs until km destroy calls ebpf.Cleanup().
	logger.Info().Msg("eBPF enforcement shutdown complete")
	return nil
}

// flushObservedState writes the recorder's current state to a local JSON file
// and uploads it to S3. Called on SIGUSR1 (snapshot) and on shutdown.
func flushObservedState(recorder *allowlistgen.Recorder, sandboxID, outputPath string, logger zerolog.Logger) {
	state := observedState{
		DNS:   recorder.DNSDomains(),
		Hosts: recorder.Hosts(),
		Repos: recorder.Repos(),
	}
	data, marshalErr := json.MarshalIndent(state, "", "  ")
	if marshalErr != nil {
		logger.Error().Err(marshalErr).Msg("observe: failed to marshal state")
		return
	}

	// Atomic write: write to .tmp then rename.
	tmpPath := outputPath + ".tmp"
	if writeErr := os.WriteFile(tmpPath, data, 0o644); writeErr != nil {
		logger.Error().Err(writeErr).Str("path", tmpPath).Msg("observe: failed to write tmp file")
		return
	}
	if renameErr := os.Rename(tmpPath, outputPath); renameErr != nil {
		logger.Error().Err(renameErr).Msg("observe: failed to rename tmp to output")
		return
	}

	// Upload to S3 at learn/{sandboxID}/{timestamp}.json.
	s3Key := "skipped"
	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if bucket != "" {
		timestamp := time.Now().UTC().Format("20060102T150405Z")
		s3Key = fmt.Sprintf("learn/%s/%s.json", sandboxID, timestamp)
		uploadCtx := context.Background()
		awsCfg, cfgErr := kmaws.LoadAWSConfig(uploadCtx, "")
		if cfgErr != nil {
			logger.Warn().Err(cfgErr).Msg("observe: failed to load AWS config for S3 upload")
			s3Key = "skipped"
		} else {
			s3Client := s3.NewFromConfig(awsCfg)
			_, putErr := s3Client.PutObject(uploadCtx, &s3.PutObjectInput{
				Bucket:      awssdk.String(bucket),
				Key:         awssdk.String(s3Key),
				Body:        bytes.NewReader(data),
				ContentType: awssdk.String("application/json"),
			})
			if putErr != nil {
				logger.Warn().Err(putErr).Str("key", s3Key).Msg("observe: S3 upload failed (non-fatal)")
				s3Key = "skipped"
			}
		}
	} else {
		logger.Warn().Msg("observe: KM_ARTIFACTS_BUCKET not set, skipping S3 upload")
	}

	logger.Info().
		Int("dns_domains", len(state.DNS)).
		Int("hosts", len(state.Hosts)).
		Int("repos", len(state.Repos)).
		Str("path", outputPath).
		Str("s3_key", s3Key).
		Msgf("observe: flushed %d DNS, %d hosts, %d repos (S3: %s)",
			len(state.DNS), len(state.Hosts), len(state.Repos), s3Key)
}
