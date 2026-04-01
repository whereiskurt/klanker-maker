//go:build linux && amd64

package tls

import (
	"errors"
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/rs/zerolog/log"
)

// OpenSSLProbe manages the lifecycle of uprobes attached to OpenSSL's
// SSL_write/SSL_read functions and the kprobes for connection correlation.
type OpenSSLProbe struct {
	objs        *opensslBpfObjects
	connectObjs *connectBpfObjects
	links       []link.Link
	closed      bool
}

// AttachOpenSSL loads the BPF programs and attaches uprobes to the OpenSSL
// library at libsslPath. It also attaches kprobes for connection correlation.
//
// The caller must call Close() when done to detach probes and free resources.
func AttachOpenSSL(libsslPath string) (*OpenSSLProbe, error) {
	// Remove memlock rlimit for kernels < 5.11.
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w", err)
	}

	// Load OpenSSL BPF objects (programs + maps).
	var objs opensslBpfObjects
	if err := loadOpensslBpfObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load openssl bpf objects: %w", err)
	}

	// Load connection correlation BPF objects.
	// Share maps with the openssl objects by reusing the same map FDs.
	var connObjs connectBpfObjects
	connOpts := &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{},
	}
	// Load connect BPF with map replacement so they share the same maps.
	connSpec, err := loadConnectBpf()
	if err != nil {
		objs.Close()
		return nil, fmt.Errorf("load connect bpf spec: %w", err)
	}
	// Replace maps in connectBpf with the ones already loaded by opensslBpf,
	// so both programs share the same conn_map and lib_enabled.
	connSpec.Maps["conn_map"].Contents = nil
	connSpec.Maps["lib_enabled"].Contents = nil
	connSpec.Maps["ssl_read_args_map"].Contents = nil
	connSpec.Maps["tls_events"].Contents = nil
	connOpts.MapReplacements = map[string]*ebpf.Map{
		"conn_map":         objs.ConnMap,
		"lib_enabled":      objs.LibEnabled,
		"ssl_read_args_map": objs.SslReadArgsMap,
		"tls_events":        objs.TlsEvents,
	}
	if err := connSpec.LoadAndAssign(&connObjs, connOpts); err != nil {
		objs.Close()
		return nil, fmt.Errorf("load connect bpf objects: %w", err)
	}

	probe := &OpenSSLProbe{
		objs:        &objs,
		connectObjs: &connObjs,
	}

	// Open the libssl executable for uprobe attachment.
	ex, err := link.OpenExecutable(libsslPath)
	if err != nil {
		probe.Close()
		return nil, fmt.Errorf("open executable %s: %w", libsslPath, err)
	}

	// Required probes: SSL_write and SSL_read entry + SSL_read return.
	requiredUprobes := []struct {
		symbol string
		prog   *ebpf.Program
		ret    bool
	}{
		{"SSL_write", objs.UprobeSslWriteEntry, false},
		{"SSL_read", objs.UprobeSslReadEntry, false},
		{"SSL_read", objs.UretprobeSslReadReturn, true},
	}

	for _, up := range requiredUprobes {
		var l link.Link
		var err error
		if up.ret {
			l, err = ex.Uretprobe(up.symbol, up.prog, nil)
		} else {
			l, err = ex.Uprobe(up.symbol, up.prog, nil)
		}
		if err != nil {
			probe.Close()
			retStr := ""
			if up.ret {
				retStr = "uret"
			}
			return nil, fmt.Errorf("attach %sprobe %s: %w", retStr, up.symbol, err)
		}
		probe.links = append(probe.links, l)
	}

	// Optional probes: SSL_write_ex and SSL_read_ex (OpenSSL 3.x only).
	optionalUprobes := []struct {
		symbol string
		prog   *ebpf.Program
		ret    bool
	}{
		{"SSL_write_ex", objs.UprobeSslWriteExEntry, false},
		{"SSL_read_ex", objs.UprobeSslReadExEntry, false},
		{"SSL_read_ex", objs.UretprobeSslReadExReturn, true},
	}

	for _, up := range optionalUprobes {
		var l link.Link
		var err error
		if up.ret {
			l, err = ex.Uretprobe(up.symbol, up.prog, nil)
		} else {
			l, err = ex.Uprobe(up.symbol, up.prog, nil)
		}
		if err != nil {
			// Optional — some OpenSSL versions (1.1.x) don't have _ex variants.
			log.Debug().Err(err).Str("symbol", up.symbol).Msg("optional uprobe not available, skipping")
			continue
		}
		probe.links = append(probe.links, l)
	}

	// Attach kprobes for connection correlation.
	kprobes := []struct {
		symbol string
		prog   *ebpf.Program
	}{
		{"__sys_connect", connObjs.KprobeConnect},
		{"__sys_accept4", connObjs.KprobeAccept4},
	}

	for _, kp := range kprobes {
		l, err := link.Kprobe(kp.symbol, kp.prog, nil)
		if err != nil {
			// Try without __ prefix (kernel version variation).
			altSymbol := kp.symbol[2:] // strip "__"
			l, err = link.Kprobe("sys_"+altSymbol[4:], kp.prog, nil)
			if err != nil {
				log.Warn().Err(err).Str("symbol", kp.symbol).Msg("kprobe attach failed, connection correlation may be limited")
				continue
			}
		}
		probe.links = append(probe.links, l)
	}

	// Enable OpenSSL library capture by default.
	// Map types are __u8 key and __u8 value in BPF.
	key := uint8(LibOpenSSL)
	val := uint8(1)
	if err := objs.LibEnabled.Put(key, val); err != nil {
		log.Warn().Err(err).Msg("failed to set lib_enabled for openssl")
	}

	log.Info().
		Str("libssl", libsslPath).
		Int("uprobe_count", len(probe.links)).
		Msg("openssl uprobes attached")

	return probe, nil
}

// EventsMap returns the BPF ring buffer map for TLS events.
// The consumer reads from this map.
func (p *OpenSSLProbe) EventsMap() *ebpf.Map {
	return p.objs.TlsEvents
}

// SetLibraryEnabled toggles capture for a specific TLS library type
// via the lib_enabled BPF map. When disabled, the BPF programs will
// skip event emission for that library, reducing overhead without
// detaching probes.
func (p *OpenSSLProbe) SetLibraryEnabled(libType uint8, enabled bool) error {
	key := libType
	val := uint8(0)
	if enabled {
		val = 1
	}
	return p.objs.LibEnabled.Put(key, val)
}

// Close detaches all uprobes and kprobes, and frees BPF objects.
// Safe to call multiple times (idempotent).
func (p *OpenSSLProbe) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true

	var errs []error
	for _, l := range p.links {
		if err := l.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.objs != nil {
		if err := p.objs.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.connectObjs != nil {
		// Only close programs — maps are shared with opensslBpf.
		if err := p.connectObjs.connectBpfPrograms.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
