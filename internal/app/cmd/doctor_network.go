package cmd

import (
	"fmt"
	"strings"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// runningPrivateSandboxIDs filters metas down to sandbox ids that are both
// running AND placed in a private subnet. A pre-125 row (NetworkPlacement ==
// "") is implicitly public — it must never count as private (matches the
// natDisableGuard filter in internal/app/cmd/init_nat_guard.go).
func runningPrivateSandboxIDs(metas []kmaws.SandboxMetadata) []string {
	var ids []string
	for _, m := range metas {
		if m.NetworkPlacement != "private" {
			continue
		}
		// Phase 125 live-UAT finding: `km create` does NOT write a `status`
		// attribute at create time — `km list` derives status from live EC2 — so a
		// freshly created, actively running private sandbox has Status == "".
		// Requiring Status == "running" here matched nothing, so checkNATIdle
		// reported "safe to disable" while a private sandbox was live; an operator
		// following that remediation would have torn down the NAT its egress
		// depends on.
		//
		// Fail safe in the direction that cannot break a live sandbox: count a
		// private row as NAT-dependent unless its status is definitively terminal.
		// Rows are deleted on destroy (Phase 109), so a present row means the
		// sandbox still exists. "stopped"/"starting"/"" all count — a stopped
		// private sandbox needs NAT again the moment it resumes.
		if m.Status == "failed" {
			continue
		}
		ids = append(ids, m.SandboxID)
	}
	return ids
}

// checkNATIdle warns when the install-level NAT gateway toggle is on but no
// running private sandbox depends on it — the operator is paying for NAT/EIP
// infrastructure across every AZ for nothing.
//
// Returns:
//   - SKIPPED: natEnabled is false — NAT was never touched (Phase 124 install).
//   - WARN:    natEnabled is true and zero running private sandboxes exist.
//   - OK:      natEnabled is true and at least one running private sandbox exists.
func checkNATIdle(natEnabled bool, metas []kmaws.SandboxMetadata) CheckResult {
	name := "NAT gateway idle"
	if !natEnabled {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "network.nat_gateway not set — NAT off",
		}
	}

	privateIDs := runningPrivateSandboxIDs(metas)
	if len(privateIDs) == 0 {
		return CheckResult{
			Name:   name,
			Status: CheckWarn,
			Message: "network.nat_gateway is enabled but no private sandbox depends on it — " +
				"you are paying about $132/month (4 AZs) for NAT/EIP infrastructure nothing is using; safe to disable",
			Remediation: "Set network.nat_gateway: false in km-config.yaml and run `km init --dry-run=false` to tear down the NAT/EIP infrastructure.",
		}
	}

	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("network.nat_gateway enabled; %d private sandbox(es) depend on it", len(privateIDs)),
	}
}

// checkPrivateWithoutNAT warns when a running private sandbox exists but the
// install-level NAT gateway toggle is off — that sandbox has no egress path
// at all (its private subnet has no route to the internet).
//
// Returns:
//   - SKIPPED: natEnabled is true — the dangerous combination cannot occur.
//   - SKIPPED: natEnabled is false and zero running private sandboxes exist.
//   - WARN:    natEnabled is false and one or more running private sandboxes exist.
func checkPrivateWithoutNAT(natEnabled bool, metas []kmaws.SandboxMetadata) CheckResult {
	name := "Private sandboxes without NAT"
	if natEnabled {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "network.nat_gateway is enabled — private sandboxes have an egress path",
		}
	}

	privateIDs := runningPrivateSandboxIDs(metas)
	if len(privateIDs) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "no running private sandboxes — nothing to check",
		}
	}

	return CheckResult{
		Name:   name,
		Status: CheckWarn,
		Message: fmt.Sprintf("%d running private sandbox(es) have no NAT egress path: %s",
			len(privateIDs), strings.Join(privateIDs, ", ")),
		Remediation: "Set network.nat_gateway: true in km-config.yaml and run `km init --dry-run=false` to provision NAT for these sandboxes, or destroy them if they no longer need network access (km destroy <id> --remote --yes).",
	}
}
