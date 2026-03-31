package compiler

import "github.com/whereiskurt/klankrmkr/pkg/profile"

// mergeBedrockEnv returns the profile's Env map with Bedrock-specific variables
// injected when UseBedrock is true. Explicit env entries take precedence over
// the auto-injected defaults (user can override model IDs etc).
func mergeBedrockEnv(p *profile.SandboxProfile) map[string]string {
	env := make(map[string]string)
	// Copy existing env first.
	for k, v := range p.Spec.Execution.Env {
		env[k] = v
	}

	if !p.Spec.Execution.UseBedrock {
		return env
	}

	// Bedrock defaults — only set if not already present in profile env.
	region := p.Spec.Runtime.Region
	if region == "" {
		region = "us-east-1"
	}

	defaults := map[string]string{
		"CLAUDE_CODE_USE_BEDROCK":                "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"ANTHROPIC_BASE_URL":                     "https://bedrock-runtime." + region + ".amazonaws.com",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":          "us.anthropic.claude-sonnet-4-6",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":            "us.anthropic.claude-opus-4-6-v1",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":           "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"AWS_DEFAULT_REGION":                      region,
	}

	for k, v := range defaults {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}

	return env
}
