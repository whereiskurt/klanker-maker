package compiler

import (
	"strings"
	"testing"
)

// The enforcer must no longer pin itself to the per-sandbox scope. Since
// Phase 135 it attaches at the cgroup2 mount root and selects callers by uid
// or scope membership. Passing the scope path here would restore exactly the
// gap the phase closes — and would do it silently, because the box still
// boots, the unit still runs, and every other check still passes.
func TestUserData_EnforcerAttachesAtRootCgroup(t *testing.T) {
	for _, mode := range []string{"ebpf", "both"} {
		t.Run(mode, func(t *testing.T) {
			p := baseProfile()
			p.Spec.Network.Enforcement = mode
			out, err := generateUserData(p, "sb-enf-"+mode, nil, "my-bucket", false, nil)
			if err != nil {
				t.Fatalf("generateUserData failed: %v", err)
			}
			if strings.Contains(out, "--cgroup /sys/fs/cgroup/km.slice/") {
				t.Error("enforcer is still pinned to the per-sandbox scope, which no " +
					"interactive session ever enters")
			}
			if !strings.Contains(out, "--cgroup /sys/fs/cgroup\n") {
				t.Error("enforcer must attach at the cgroup2 mount root")
			}
		})
	}
}

// The scope itself must still be created: it is the second clause of the
// enforcement predicate, and Phase 132's dispatch_as_sandbox sites still join
// it. Moving the attach point must not be mistaken for retiring the scope.
func TestUserData_SandboxScopeStillCreated(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "both"
	out, err := generateUserData(p, "sb-scope", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "mkdir -p /sys/fs/cgroup/km.slice/km-sb-scope.scope") {
		t.Error("the per-sandbox scope must still be created; it is the cgroup clause " +
			"of the predicate and Phase 132 dispatch still joins it")
	}
}
