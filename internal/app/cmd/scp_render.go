package cmd

import (
	"fmt"
	"os"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/scp"
)

// scpPolicyEnv carries the rendered SCP into infra/live/management/scp, whose
// get_env has no default so an unset value fails the run loudly.
const scpPolicyEnv = "KM_SCP_POLICY_JSON"

// scpInputFromConfig maps km-config.yaml onto the pkg/scp renderer. The
// renderer is the single definition of the policy: the --scp preview, the
// deployed document and the km doctor content check all come from here.
func scpInputFromConfig(cfg *config.Config) scp.Input {
	return scp.Input{
		ResourcePrefix:       cfg.GetResourcePrefix(),
		ApplicationAccountID: cfg.ApplicationAccountID,
		Region:               cfg.PrimaryRegion,
		RegionLabel:          cfg.GetRegionLabel(),
		ExtraRegions:         cfg.SCP.ExtraRegions,
		TrustedRoles:         cfg.SCP.TrustedRoles,
		TrustedIAMRoles:      cfg.SCP.TrustedIAMRoles,
		TrustedSSMRoles:      cfg.SCP.TrustedSSMRoles,
	}
}

// renderSCP renders the install's sandbox-containment SCP.
func renderSCP(cfg *config.Config) (scp.Policy, error) {
	p, err := scp.Render(scpInputFromConfig(cfg))
	if err != nil {
		return scp.Policy{}, fmt.Errorf("render SCP from km-config.yaml: %w", err)
	}
	return p, nil
}

// exportSCPPolicyEnv renders the SCP and sets KM_SCP_POLICY_JSON for the
// terragrunt subprocess. Unlike warnAndSetEnv it always overwrites: a stale
// value inherited from an earlier `eval $(km env)` must never win over the
// config the operator is applying now.
func exportSCPPolicyEnv(cfg *config.Config) (scp.Policy, error) {
	p, err := renderSCP(cfg)
	if err != nil {
		return scp.Policy{}, err
	}
	os.Setenv(scpPolicyEnv, string(p.JSON())) //nolint:errcheck
	return p, nil
}
