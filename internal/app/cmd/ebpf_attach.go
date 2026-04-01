//go:build linux && amd64

// Package cmd provides the Cobra command tree for the km CLI.
package cmd

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/audit"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/resolver"
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
		sandboxID    string
		dnsPort      uint32
		httpPort     uint32
		firewallMode string
		allowedDNS   string
		allowedHosts string
		proxyHosts   string
		cgroupPath   string
	)

	cmd := &cobra.Command{
		Use:    "ebpf-attach",
		Short:  "Attach eBPF network enforcement to sandbox cgroup",
		Long:   "Loads BPF programs, attaches to sandbox cgroup, starts DNS resolver, and runs audit consumer. Invoked by EC2 user-data bootstrap.",
		Hidden: true, // internal command, not user-facing
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEbpfAttach(sandboxID, dnsPort, httpPort, firewallMode,
				allowedDNS, allowedHosts, proxyHosts, cgroupPath)
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

// ipToUint32 converts an IPv4 address to a uint32 in network byte order.
func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}

func runEbpfAttach(
	sandboxID string,
	dnsPort, httpPort uint32,
	firewallMode string,
	allowedDNS, allowedHosts, proxyHosts string,
	cgroupOverride string,
) error {
	logger := log.With().Str("sandbox_id", sandboxID).Logger()

	fwMode, err := parseFirewallMode(firewallMode)
	if err != nil {
		return err
	}

	// 127.0.0.1 in network byte order.
	mitmAddr := ipToUint32(net.ParseIP("127.0.0.1"))

	cfg := ebpf.Config{
		SandboxID:      sandboxID,
		DNSProxyPort:   dnsPort,
		HTTPProxyPort:  httpPort,
		HTTPSProxyPort: httpPort,
		ProxyPID:       uint32(os.Getpid()),
		FirewallMode:   fwMode,
		MITMProxyAddr:  mitmAddr,
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

	// Start DNS resolver daemon.
	listenAddr := fmt.Sprintf("127.0.0.1:%d", dnsPort)
	resolverCfg := resolver.ResolverConfig{
		ListenAddr:      listenAddr,
		UpstreamAddr:    "169.254.169.253:53", // VPC resolver
		SandboxID:       sandboxID,
		AllowedSuffixes: dnsSuffixes,
		MapUpdater:      enforcer,
		ProxyHosts:      proxyHostList,
	}
	res := resolver.NewResolver(resolverCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolverErrCh := make(chan error, 1)
	go func() {
		if err := res.Start(ctx); err != nil {
			resolverErrCh <- err
		}
	}()
	logger.Info().Str("listen", listenAddr).Msg("DNS resolver started")

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

	seeded := 0
	for _, host := range hostsToResolve {
		ips, err := net.LookupHost(host)
		if err != nil {
			// Suffix entries like "amazonaws.com" won't resolve directly — that's fine
			continue
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
		}
	}
	logger.Info().Int("seeded_ips", seeded).Int("hosts_resolved", len(hostsToResolve)).Msg("pre-seeded BPF allowlist from allowed hosts")

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

	logger.Info().Msg("eBPF enforcement active — waiting for shutdown signal")

	// Wait for SIGTERM or SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		logger.Info().Str("signal", sig.String()).Msg("received shutdown signal")
	case err := <-resolverErrCh:
		logger.Error().Err(err).Msg("DNS resolver error")
	case err := <-auditErrCh:
		logger.Error().Err(err).Msg("audit consumer error")
	}

	logger.Info().Msg("shutting down eBPF enforcement")

	// Graceful shutdown: cancel context (stops resolver and audit consumer).
	cancel()

	// Close audit consumer explicitly to unblock pending Read().
	if err := consumer.Close(); err != nil {
		logger.Debug().Err(err).Msg("audit consumer close")
	}

	// Enforcer.Close() is deferred — it closes BPF objects.
	// Pinned programs remain on bpffs until km destroy calls ebpf.Cleanup().
	logger.Info().Msg("eBPF enforcement shutdown complete")
	return nil
}
