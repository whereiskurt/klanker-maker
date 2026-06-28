package compiler

import (
	"fmt"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// codexConfigBaseBlock is the canonical ~/.codex/config.toml content written for
// every sandbox. It is byte-identical to the heredoc the Phase 70 userdata
// template previously emitted inline (pkg/compiler/userdata.go), so wiring the
// synthesizer into the userdata pipeline does NOT change the bytes shipped to a
// sandbox — the Phase 92 byte-identity contract (VC-3) depends on this.
//
// HONEST SCOPE (per .planning/research/codex-config-toml.md §1a + Phase 70 spike
// at docs/codex-parity.md): Codex 0.121/0.133 does NOT fire these hooks from
// config.toml, and there is NO native tool allow/deny schema. The hook blocks are
// forward-compat scaffolding; actual gating is enforcement-layer only (eBPF /
// proxy network restrictions + sandbox_mode filesystem scoping).
const codexConfigBaseBlock = `# Phase 70: 'codex_hooks' was the Codex 0.121 feature flag name. In 0.133+ it
# was renamed to 'hooks' and the old name emits a deprecation warning event
# on every codex exec (filtered out by our JSONL parser, but noisy). The
# flag remains harmless under Path B (JSONL parsing) — hooks may or may not
# actually fire depending on Codex version; the platform does not depend on
# them. See docs/codex-parity.md and the Path B section of SPEC.md.
[features]
hooks = true

[[hooks.PermissionRequest]]
matcher = ".*"

[[hooks.PermissionRequest.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook PermissionRequest"
timeout = 30
statusMessage = "km: notifying operator"

[[hooks.Stop]]

[[hooks.Stop.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook Stop"
timeout = 30
`

// synthesizeCodexConfig builds the ~/.codex/config.toml content from the typed
// AgentCodexSpec (Phase 92, Wave 5).
//
// HONEST SCOPE (per .planning/research/codex-config-toml.md §1a): Codex 0.133 has
// NO native tool allow/deny in config.toml. The base block's [[hooks.*]] entries
// are forward-compat — Codex 0.133 ignores them (Phase 70 spike confirmed at
// docs/codex-parity.md). This synthesizer therefore:
//
//  1. Emits the existing inert hook blocks (codexConfigBaseBlock) so a future
//     Codex release that activates them picks them up without a migration.
//  2. Echoes agent.codex.args via comments — operators pass these through the km
//     wrapper, not via config.toml.
//  3. Emits a "# NOTE:" block when agent.codex.tools.{autoApprove,deny} are
//     non-empty, documenting the asymmetry vs Claude Code. The fields are
//     preserved in the YAML for forward-compat but are NOT enforced today.
//  4. Emits a [model_providers.local] block (Phase 122) when LocalBaseURL is set,
//     routing codex at the local model via the Bifrost gateway (:8001, wire_api=
//     "responses"). Codex requires the OpenAI Responses API (since Feb 2026); the
//     profile sets base_url = "http://localhost:8001/v1" pointing at Bifrost, NOT
//     vLLM :8000 directly — Bifrost provides uniform multi-provider access.
//     NOTE: current vLLM also serves /v1/responses natively, so :8000 is a valid
//     documented fallback if Bifrost is unavailable, but the profile routes codex
//     through Bifrost :8001 for uniform multi-provider access.
//
// Nil-safe: a nil agent or nil agent.Codex returns just the base hook block (the
// common case — most profiles do not configure agent.codex). Returns a string
// (toml) so it can be written via the configFiles userdata path without further
// marshalling.
func synthesizeCodexConfig(agent *profile.AgentSpec) (string, error) {
	var b strings.Builder
	b.WriteString(codexConfigBaseBlock)

	if agent == nil || agent.Codex == nil {
		return b.String(), nil
	}
	c := agent.Codex

	if len(c.Args) > 0 {
		b.WriteString("\n# agent.codex.args (echoed for reference; passed via the km wrapper,\n")
		b.WriteString("# not honored from config.toml):\n")
		for _, a := range c.Args {
			b.WriteString(fmt.Sprintf("# args: %s\n", a))
		}
	}

	if len(c.Tools.AutoApprove) > 0 || len(c.Tools.Deny) > 0 {
		b.WriteString("\n# NOTE: agent.codex.tools.{autoApprove,deny} are declared but NOT enforced.\n")
		b.WriteString("# Codex 0.133 has no settings.json/config.toml tool-gating schema. These\n")
		b.WriteString("# fields are preserved for a future Codex release; for actual gating today\n")
		b.WriteString("# use sandbox-level network policy (eBPF allowlist) + sandbox_mode scoping.\n")
		b.WriteString("# See docs/agent-tool-gating.md and docs/codex-parity.md.\n")
		if len(c.Tools.AutoApprove) > 0 {
			b.WriteString(fmt.Sprintf("# autoApprove: %s\n", strings.Join(c.Tools.AutoApprove, ", ")))
		}
		if len(c.Tools.Deny) > 0 {
			b.WriteString(fmt.Sprintf("# deny:        %s\n", strings.Join(c.Tools.Deny, ", ")))
		}
	}

	// Phase 122: emit [model_providers.local] when the local-provider knob is set.
	// LocalBaseURL points at the Bifrost gateway (http://localhost:8001/v1) which
	// serves the Responses API to codex and routes to the on-box vLLM instance.
	if c.LocalBaseURL != "" {
		model := c.LocalModel
		if model == "" {
			model = "local"
		}
		fmt.Fprintf(&b, "\nmodel_provider = %q\n", "local")
		fmt.Fprintf(&b, "model = %q\n", model)
		fmt.Fprintf(&b, "\n[model_providers.local]\n")
		fmt.Fprintf(&b, "name = %q\n", "Local vLLM (via Bifrost)")
		fmt.Fprintf(&b, "base_url = %q\n", c.LocalBaseURL)
		fmt.Fprintf(&b, "wire_api = %q\n", "responses")
		fmt.Fprintf(&b, "env_key = %q\n", "OPENAI_API_KEY")
	}

	return b.String(), nil
}
