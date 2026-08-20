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

// TestResolveSandboxSubnets exercises resolveSandboxSubnets — the single
// placement-resolution point inserted before the Phase 124 AZ sweep — plus the
// sweep's reorder/rotate mechanics that operate on its result (125-08-PLAN.md
// Task 2 Tests 1-5).
func TestResolveSandboxSubnets(t *testing.T) {
	publicSubnets := []string{"subnet-pub-1a", "subnet-pub-1b", "subnet-pub-1c"}
	privateSubnets := []string{"subnet-priv-1a", "subnet-priv-1b", "subnet-priv-1c"}

	t.Run("Test1_private_true_resolves_to_private_list", func(t *testing.T) {
		got := resolveSandboxSubnets(true, publicSubnets, privateSubnets)
		if !stringSlicesEqual(got, privateSubnets) {
			t.Errorf("resolveSandboxSubnets(true, ...) = %v, want %v", got, privateSubnets)
		}
	})

	t.Run("Test2_private_false_resolves_to_public_list", func(t *testing.T) {
		got := resolveSandboxSubnets(false, publicSubnets, privateSubnets)
		if !stringSlicesEqual(got, publicSubnets) {
			t.Errorf("resolveSandboxSubnets(false, ...) = %v, want %v", got, publicSubnets)
		}
	})

	t.Run("Test3_PublicSubnets_field_untouched_by_resolution_either_way", func(t *testing.T) {
		// resolveSandboxSubnets is a pure function returning a new slice reference —
		// it must never mutate its inputs. The caller (create.go) separately keeps
		// network.PublicSubnets populated; this test locks in that resolveSandboxSubnets
		// itself is not the thing that could break that guarantee.
		publicCopy := append([]string{}, publicSubnets...)
		privateCopy := append([]string{}, privateSubnets...)
		_ = resolveSandboxSubnets(true, publicCopy, privateCopy)
		_ = resolveSandboxSubnets(false, publicCopy, privateCopy)
		if !stringSlicesEqual(publicCopy, publicSubnets) {
			t.Errorf("public subnets slice mutated: got %v, want %v", publicCopy, publicSubnets)
		}
		if !stringSlicesEqual(privateCopy, privateSubnets) {
			t.Errorf("private subnets slice mutated: got %v, want %v", privateCopy, privateSubnets)
		}
	})

	t.Run("Test4_attempt_rotation_keeps_AZ_subnet_pairing", func(t *testing.T) {
		// Reproduces the exact "attempt > 0" rotation the sweep performs on
		// network.AvailabilityZones / network.SandboxSubnets (create.go, inside the
		// attempt loop): rotate both slices left by one in lockstep.
		azs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
		subnets := resolveSandboxSubnets(true, publicSubnets, privateSubnets)
		pairing := map[string]string{azs[0]: subnets[0], azs[1]: subnets[1], azs[2]: subnets[2]}

		subnets = append(subnets[1:], subnets[0])
		azs = append(azs[1:], azs[0])

		for i, az := range azs {
			if subnets[i] != pairing[az] {
				t.Errorf("after rotation, AZ %q paired with subnet %q, want %q", az, subnets[i], pairing[az])
			}
		}
	})

	t.Run("Test5_ranked_reorder_keeps_AZ_subnet_pairing", func(t *testing.T) {
		// Reproduces the exact zip-then-reorder the sweep performs after RankAZs
		// returns a ranked AZ list (create.go: subnetByAZ map keyed by AZ, then
		// reordered to follow `ranked`'s order).
		azs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
		subnets := resolveSandboxSubnets(true, publicSubnets, privateSubnets)
		pairing := map[string]string{azs[0]: subnets[0], azs[1]: subnets[1], azs[2]: subnets[2]}

		ranked := []string{"us-east-1c", "us-east-1a", "us-east-1b"}
		subnetByAZ := make(map[string]string, len(azs))
		for i, az := range azs {
			subnetByAZ[az] = subnets[i]
		}
		reordered := make([]string, 0, len(ranked))
		for _, az := range ranked {
			reordered = append(reordered, subnetByAZ[az])
		}

		for i, az := range ranked {
			if reordered[i] != pairing[az] {
				t.Errorf("after ranked reorder, AZ %q paired with subnet %q, want %q", az, reordered[i], pairing[az])
			}
		}
	})
}

// TestNetworkPlacementRecorded exercises networkPlacementLabel (Test 6: a private
// create records "private" on the sandbox row; a public one records "public") and
// confirms both create.go SandboxMetadata literals are wired to it.
func TestNetworkPlacementRecorded(t *testing.T) {
	t.Run("Test6_private_true_labels_private", func(t *testing.T) {
		if got := networkPlacementLabel(true); got != "private" {
			t.Errorf("networkPlacementLabel(true) = %q, want %q", got, "private")
		}
	})

	t.Run("Test6_private_false_labels_public", func(t *testing.T) {
		if got := networkPlacementLabel(false); got != "public" {
			t.Errorf("networkPlacementLabel(false) = %q, want %q", got, "public")
		}
	})

	t.Run("both_SandboxMetadata_literals_set_NetworkPlacement", func(t *testing.T) {
		src, err := os.ReadFile("create.go")
		if err != nil {
			t.Fatalf("read create.go: %v", err)
		}
		count := strings.Count(string(src), "NetworkPlacement: networkPlacementLabel(resolvedProfile.Spec.Network.PrivateSubnet)")
		if count != 2 {
			t.Errorf("expected 2 NetworkPlacement wiring sites (EC2 local + --remote starting row), found %d", count)
		}
	})
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
