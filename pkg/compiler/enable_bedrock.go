package compiler

import "github.com/whereiskurt/klanker-maker/pkg/profile"

// enableBedrock reports whether the sandbox role should receive Bedrock IAM and
// whether the on-box Bifrost gateway may use the bedrock provider.
//
// True when EITHER the on-box agents are pointed at Bedrock
// (spec.execution.useBedrock) OR the gateway is explicitly allowed keyless
// Bedrock (spec.iam.allowBedrock). The agent-env injection in bedrock.go stays
// keyed on UseBedrock ONLY, so allowBedrock grants IAM without repointing agents.
func enableBedrock(p *profile.SandboxProfile) bool {
	if p.Spec.Execution.UseBedrock {
		return true
	}
	return p.Spec.IAM.AllowBedrock != nil && *p.Spec.IAM.AllowBedrock
}
