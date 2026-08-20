package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// natDisableGuard is the pure Phase 125 NAT-disable refuse guard (T-125-17).
// No AWS clients in the signature — table-testable in isolation.
//
// Enabling NAT (desiredEnabled == true) never blocks: turning NAT on is
// pure-additive and orphans nothing. Disabling NAT (desiredEnabled == false)
// is refused when one or more sandbox rows are BOTH running AND privately
// placed — destroying NAT out from under a running private sandbox removes
// its only egress path with no diagnostic (its default route just
// disappears). Placement is read from SandboxMetadata.NetworkPlacement; a
// pre-125 row with an empty value is treated as public and never blocks. No
// override flag is offered — silently orphaning a private sandbox's egress
// is worse than a blocked apply.
func natDisableGuard(desiredEnabled bool, sandboxes []awspkg.SandboxMetadata) error {
	if desiredEnabled {
		return nil
	}

	var offending []string
	for _, sb := range sandboxes {
		if sb.NetworkPlacement == "private" && sb.Status == "running" {
			offending = append(offending, sb.SandboxID)
		}
	}
	if len(offending) == 0 {
		return nil
	}

	return fmt.Errorf(
		"refusing to disable NAT: %d running private sandbox(es) depend on it for egress: %s — destroy them first (km destroy <id> --remote --yes), then re-run km init to disable network.nat_gateway",
		len(offending), strings.Join(offending, ", "),
	)
}

// natGuardSandboxLister lists all sandbox metadata for the NAT-disable guard.
// Matches the shape of awspkg.ListAllSandboxMetadataDynamo — NOT the lossy
// SandboxLister/SandboxRecord interface used elsewhere in this package;
// NetworkPlacement lives only on SandboxMetadata (Plan 04). A nil value means
// no DynamoDB access is wired for the check; natDisableGuardPreApply WARNs
// and proceeds rather than hard-failing (T-125-20, accepted: the operator
// running km init already holds the credentials to destroy this
// infrastructure directly).
type natGuardSandboxLister func(ctx context.Context) ([]awspkg.SandboxMetadata, error)

// natDisableGuardPreApply is the dependency-injected core of the pre-apply
// gate: it resolves whether this is a genuine disable (NAT currently exists
// AND the desired state is off) and — only in that case — lists live sandbox
// metadata and delegates to the pure natDisableGuard.
//
// Any AWS/list failure (including a nil lister) degrades to a WARN and a nil
// return: an operator with no DynamoDB access should not be locked out of
// km init entirely.
func natDisableGuardPreApply(ctx context.Context, desiredEnabled, currentlyEnabled bool, lister natGuardSandboxLister) error {
	if desiredEnabled || !currentlyEnabled {
		// Enabling never blocks (pure-additive). Disabling when NAT was never
		// on / already off has nothing to protect — not a genuine disable.
		return nil
	}

	if lister == nil {
		fmt.Fprintln(os.Stderr, "WARN: NAT-disable guard: no DynamoDB access wired — proceeding without the check")
		return nil
	}

	sandboxes, err := lister(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: NAT-disable guard: could not list sandbox metadata (%v) — proceeding without the check\n", err)
		return nil
	}

	return natDisableGuard(desiredEnabled, sandboxes)
}

// checkNATDisableGuardBeforeApply is the cfg/awsCfg-bound production entry
// point, invoked via InitNATDisableGuardHook (bound in runInit, see init.go)
// immediately before the network module's Apply.
func checkNATDisableGuardBeforeApply(ctx context.Context, cfg *config.Config, awsCfg aws.Config, repoRoot, region string) error {
	desiredEnabled := cfg.GetNATGatewayEnabled()

	networkDir := filepath.Join(repoRoot, "infra", "live", compiler.RegionLabel(region), "network")
	currentlyEnabled := natCurrentlyEnabledFromOutputs(networkDir)

	var lister natGuardSandboxLister
	if tableName := cfg.GetSandboxTableName(); tableName != "" {
		client := dynamodb.NewFromConfig(awsCfg)
		lister = func(lctx context.Context) ([]awspkg.SandboxMetadata, error) {
			return awspkg.ListAllSandboxMetadataDynamo(lctx, client, tableName)
		}
	}

	return natDisableGuardPreApply(ctx, desiredEnabled, currentlyEnabled, lister)
}

// natCurrentlyEnabledFromOutputs reads the network module's existing
// outputs.json (if any) and reports whether nat_gateway_ids is non-empty —
// i.e. NAT gateway infrastructure currently exists. Any read/parse/missing-key
// error is treated as "not currently enabled" (fresh install or a pre-125
// outputs.json), matching the non-fatal extraction convention in
// LoadNetworkOutputs.
func natCurrentlyEnabledFromOutputs(networkDir string) bool {
	data, err := os.ReadFile(filepath.Join(networkDir, "outputs.json"))
	if err != nil {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	var ids []string
	if err := extractTFOutput(raw, "nat_gateway_ids", &ids); err != nil {
		return false
	}
	return len(ids) > 0
}
