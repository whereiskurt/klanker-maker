//go:build linux && amd64

// ebpf-attach only exists on linux/amd64, so these tests carry the same build
// tag as the command they cover and are a no-op on a macOS workstation.
package cmd

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// The compiler emits `--denied-dns` / `--denied-hosts` into the km-ebpf-enforcer
// unit whenever a profile declares denies. If this command does not accept
// them, the enforcer fails to start and the sandbox boots with no egress
// enforcement at all — so the flag contract is worth pinning down here rather
// than discovering it on a live box.
func TestEBPFAttachCmd_AcceptsDenyFlags(t *testing.T) {
	c := NewEBPFAttachCmd(&config.Config{})

	for _, name := range []string{"denied-dns", "denied-hosts"} {
		if f := c.Flags().Lookup(name); f == nil {
			t.Errorf("ebpf-attach must register a --%s flag", name)
		}
	}
}

// Parsing must succeed with the exact argument shape the user-data template
// renders, including the allow flags alongside the deny flags.
func TestEBPFAttachCmd_ParsesDenyFlagValues(t *testing.T) {
	c := NewEBPFAttachCmd(&config.Config{})

	// The exact argument shape the user-data template renders.
	if err := c.ParseFlags([]string{
		"--sandbox-id", "sb-test",
		"--allowed-dns", "github.com",
		"--allowed-hosts", "github.com",
		"--denied-dns", "evil.example.com,.tracker.net",
		"--denied-hosts", "evil.example.com",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got, err := c.Flags().GetString("denied-dns")
	if err != nil {
		t.Fatalf("GetString(denied-dns): %v", err)
	}
	if got != "evil.example.com,.tracker.net" {
		t.Errorf("denied-dns = %q, want %q", got, "evil.example.com,.tracker.net")
	}

	gotHosts, err := c.Flags().GetString("denied-hosts")
	if err != nil {
		t.Fatalf("GetString(denied-hosts): %v", err)
	}
	if gotHosts != "evil.example.com" {
		t.Errorf("denied-hosts = %q, want %q", gotHosts, "evil.example.com")
	}
}
