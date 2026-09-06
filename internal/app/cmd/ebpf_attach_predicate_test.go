//go:build linux && amd64

// ebpf-attach only exists on linux/amd64, so this test carries the same build
// tag. The wiring guard that must run on a dev machine lives in
// pkg/ebpf/predicate/guard_test.go instead.

package cmd

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// --cgroup was accepted and silently ignored before Phase 135: it was threaded
// into runEbpfAttach as cgroupOverride and never referenced in the body, so the
// userdata had been passing a value that did nothing.
func TestEbpfAttachFlags_CgroupAndSandboxUIDExist(t *testing.T) {
	cmd := NewEBPFAttachCmd(&config.Config{})
	for _, name := range []string{"cgroup", "sandbox-uid"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s flag is missing", name)
		}
	}
}
