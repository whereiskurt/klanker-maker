package cmd

// Phase 125 (125-08): tests for the pure placement helpers introduced in create.go
// (checkPrivateSubnetGuard, resolveSandboxSubnets) and their SandboxMetadata wiring.
//
// These live in an internal (package cmd) test file — not create_test.go's external
// cmd_test package — because the helpers are deliberately unexported (pure, no AWS
// session) and cmd_test cannot call them. This mirrors the existing split in this
// directory (e.g. init_nat_guard_test.go is package cmd while init_test.go is
// package cmd_test) rather than settling for source-text pattern matching.

import (
	"os"
	"strings"
	"testing"
)

// TestCreatePrivateSubnetGuard exercises checkPrivateSubnetGuard's four behaviours
// from the 125-08-PLAN.md <behavior> block.
func TestCreatePrivateSubnetGuard(t *testing.T) {
	t.Run("private profile against NAT-less install errors with the fix command", func(t *testing.T) {
		err := checkPrivateSubnetGuard(true, nil)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "network.nat_gateway") {
			t.Errorf("error %q does not name network.nat_gateway", err.Error())
		}
		if !strings.Contains(err.Error(), "km init") {
			t.Errorf("error %q does not name km init", err.Error())
		}
	})

	t.Run("private profile against install WITH private subnets does not error", func(t *testing.T) {
		err := checkPrivateSubnetGuard(true, []string{"subnet-priv-1a", "subnet-priv-1b"})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("public profile against install with no private subnets does not error", func(t *testing.T) {
		err := checkPrivateSubnetGuard(false, nil)
		if err != nil {
			t.Errorf("public sandboxes must never be gated on NAT, got %v", err)
		}
	})

	t.Run("guard call precedes the first artifact write, per site", func(t *testing.T) {
		// Test 4: the guard must fire before any terragrunt/S3 artifact is written.
		// runCreate (local) writes to disk via terragrunt.CreateSandboxDir; runCreateRemote
		// has no local sandbox dir at all — its first artifact write is the S3 PutObject
		// upload. Each function is checked independently: its own guard call site must
		// precede its own first artifact-write call site.
		src, err := os.ReadFile("create.go")
		if err != nil {
			t.Fatalf("read create.go: %v", err)
		}
		s := string(src)

		localStart := strings.Index(s, "\nfunc runCreate(")
		remoteStart := strings.Index(s, "\nfunc runCreateRemote(")
		remoteEnd := strings.Index(s, "\nfunc extractRepoOwner(")
		if localStart == -1 || remoteStart == -1 || remoteEnd == -1 {
			t.Fatal("create.go: could not locate runCreate/runCreateRemote/extractRepoOwner boundaries")
		}
		localSrc := s[localStart:remoteStart]
		remoteSrc := s[remoteStart:remoteEnd]

		checkGuardBeforeWrite := func(t *testing.T, fnSrc, fnName, writeMarker string) {
			t.Helper()
			guardIdx := strings.Index(fnSrc, "checkPrivateSubnetGuard(")
			if guardIdx == -1 {
				t.Errorf("%s: no checkPrivateSubnetGuard( call found", fnName)
				return
			}
			writeIdx := strings.Index(fnSrc, writeMarker)
			if writeIdx == -1 {
				t.Errorf("%s: no %q call found", fnName, writeMarker)
				return
			}
			if guardIdx >= writeIdx {
				t.Errorf("%s: checkPrivateSubnetGuard( at offset %d is not before %q at offset %d", fnName, guardIdx, writeMarker, writeIdx)
			}
		}

		checkGuardBeforeWrite(t, localSrc, "runCreate", "terragrunt.CreateSandboxDir(")
		checkGuardBeforeWrite(t, remoteSrc, "runCreateRemote", "s3Client.PutObject(")
	})
}
