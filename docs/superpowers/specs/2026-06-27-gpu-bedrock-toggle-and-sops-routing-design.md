# Design: `spec.iam.allowBedrock` + profile-driven GPU cloud routing

**Date:** 2026-06-27
**Branch:** `phase-122-gpu-vllm-serving`
**Status:** Design — approved in brainstorming, pending spec review

## Problem

On a GPU serving box the on-box Bifrost gateway (`:8001`) routes to cloud models
in addition to the local vLLM model. Two things are broken/inconsistent today:

1. **No clean way to grant Bedrock IAM.** Bedrock IAM is gated on
   `spec.execution.useBedrock`, but `useBedrock: true` *also* injects agent env
   vars (`CLAUDE_CODE_USE_BEDROCK=1`, `ANTHROPIC_BASE_URL=…bedrock…`) that
   **repoint the on-box `claude`/`goose` agents at Bedrock** — directly
   conflicting with the GPU design ("claude stays cloud-pointed, codex → local").
   So GPU profiles set `useBedrock: false`, which strips *all* Bedrock IAM. The
   Bifrost `bedrock/*` routes therefore 401 (no `InvokeModel`/`bedrock-mantle`).

2. **The Bifrost config advertises routes that don't work.** `base/gpu/serve`'s
   `config.json` is a static heredoc listing `bedrock`, `anthropic`, `openai`,
   `vllm-local` providers — but:
   - `bedrock` 401s (no IAM, per above).
   - `anthropic`/`openai` need SOPS-injected keys, and **no GPU leaf wires them**
     (`secrets/gpu.enc.yaml` is orphaned; `gpu-llama-*` reference a
     `secrets/llama-hf.enc.yaml` that does not exist; qwen/glm/kimi reference no
     secrets). Net: only `vllm-local/local` works on a GPU box today.

## Goals

- A profile decides whether it wants Bedrock; that single choice drives **both**
  the IAM grant **and** whether Bifrost includes the `bedrock` provider.
- Granting Bedrock IAM must **not** hijack the on-box agents (no `bedrock.go`
  env injection).
- Two coherent cloud-routing modes on GPU, with the Bifrost config reflecting
  **only providers that are actually usable**:
  - **Default (keys):** SOPS-injected `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` →
    `anthropic`/`openai` providers.
  - **Keyless:** `iam.allowBedrock: true` → `bedrock` provider (instance-role
    SigV4), no keys to manage.
- The toggle is a **generic** IAM primitive (any profile), with GPU as the first
  consumer.

## Non-goals

- Changing `useBedrock` semantics (it keeps its agent-repointing behavior for
  non-GPU profiles).
- A `!replace` inheritance directive or multi-file `spec.secrets` (out of scope).
- 8×-L40S (48xlarge / TP=8) and the non-Qwen models' live serving (separate UAT).

## Design

### 1. New field: `spec.iam.allowBedrock` (`*bool`, default off)

Generic, lives in the existing `spec.iam` block beside `roleSessionDuration` /
`allowedRegions` / `allowedSecretPaths`. Added to the JSON schema as an optional
boolean (`additionalProperties:false` block). Absent ≡ `false`.

Helper in the compiler:

```go
// enableBedrock reports whether the sandbox role should receive Bedrock IAM and
// whether Bifrost may use the bedrock provider. True when the agent is pointed
// at Bedrock (useBedrock) OR the gateway is explicitly allowed keyless Bedrock.
func enableBedrock(p *profile.SandboxProfile) bool {
    if p.Spec.Execution.UseBedrock { return true }
    return p.Spec.IAM != nil && p.Spec.IAM.AllowBedrock != nil && *p.Spec.IAM.AllowBedrock
}
```

### 2. IAM grant without agent hijack

- `service_hcl.go:787`: `EnableBedrock: p.Spec.Execution.UseBedrock` →
  `EnableBedrock: enableBedrock(p)`. This turns on the `ec2spot_bedrock` policy
  (`bedrock:InvokeModel`, `…WithResponseStream`, `ListInferenceProfiles`,
  `ListFoundationModels`, the marketplace-subscribe statement, **and** the
  already-committed `bedrock-mantle:CreateInference` statement) whenever Bedrock
  is enabled by either path.
- **`bedrock.go` (`mergeBedrockEnv`) is unchanged** — it stays keyed on
  `p.Spec.Execution.UseBedrock` only. So `allowBedrock` grants the role Bedrock
  permissions for the *gateway* while the on-box agents keep their env untouched.

### 3. L7 metering

`buildL7ProxyHosts`: change `if p.Spec.Execution.UseBedrock` →
`if enableBedrock(p)` for the `.amazonaws.com` + `api.anthropic.com` pair, so
Bifrost→Bedrock calls flow through the http-proxy MITM meter (matches "cloud
routes metered automatically"). Host order preserved for the existing test
contract (`TestL7ProxyHostsWithBedrock`); a new test covers the `allowBedrock`
(useBedrock=false) case.

### 4. Bifrost-enable signal to the box

The compiler writes a marker file early in userdata, **before** km-init.sh /
`initCommandsAppend` run:

```
{{- if .EnableBedrock }}
mkdir -p /etc/km && touch /etc/km/bedrock.enabled
{{- end }}
```

`EnableBedrock` is added to the userdata template params (set from
`enableBedrock(p)`). The marker is a presence flag — no value parsing, no env
sourcing fragility.

### 5. `base/gpu/serve`: conditional Bifrost `config.json` (jq)

The static config.json heredoc step in `initCommandsAppend` is replaced by a
step that **builds the JSON with `jq`**, starting from the always-present
`vllm-local` provider and conditionally adding providers:

| Provider | Added when |
|---|---|
| `vllm-local` | always |
| `anthropic` | `grep -q '^ANTHROPIC_API_KEY=.\+' /etc/km/bifrost-env` |
| `openai` | `grep -q '^OPENAI_API_KEY=.\+' /etc/km/bifrost-env` |
| `bedrock` | `[ -f /etc/km/bedrock.enabled ]` |

`jq` is present (installed by `base/os/debian` + `base/userinit`). The provider
JSON blocks are the same shapes already validated live. `base/gpu/serve` is
**key-agnostic** — it does not declare `spec.secrets` (per "each leaf references
its own SOPS"). It does **not** set `allowBedrock` (default off).

Ordering note: the existing
`grep -hE '^(ANTHROPIC_API_KEY|OPENAI_API_KEY)=' /etc/sandbox-secrets.env >> /etc/km/bifrost-env`
step must run **before** the jq config build, so the key-presence checks see the
injected keys. `/etc/sandbox-secrets.env` is produced by the SOPS decrypt at
boot (userdata, before `initCommandsAppend`).

### 6. Per-leaf SOPS wiring (default-keys mode)

`spec.secrets.sopsFile` is one file per profile, so each leaf's SOPS file carries
**all** secrets that leaf needs:

- **Ungated leaves** (qwen, glm, kimi): `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`.
- **Gated leaves** (llama): `HF_TOKEN`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`.

Implementation tasks:
- Populate each leaf's SOPS file. Source the API-key *values* from SSM
  (`/km/secrets/anthropic-api-key`, `/km/secrets/openai-api-key`) where they
  exist; the operator supplies any missing value (OpenAI key was a handoff TODO).
  Encrypt with the shared SOPS KMS key (`km bootstrap --shared-secrets-key`;
  verify provisioned).
- Create the missing `secrets/llama-hf.enc.yaml` (HF_TOKEN + API keys).
- Repurpose/retire the orphaned `secrets/gpu.enc.yaml` (fold its Anthropic key
  into the per-leaf files, then remove or keep as a documented template).
- Each leaf already (or now) declares `spec.secrets.sopsFile: ./secrets/<leaf>.enc.yaml`.

**Keyless mode** for any leaf: remove its `spec.secrets.sopsFile` (or omit the
API keys) and set `spec.iam.allowBedrock: true`. Bifrost then drops
`anthropic`/`openai` (no keys) and adds `bedrock`.

### 7. IAM module

Already committed (`912eb730`): `bedrock-mantle:CreateInference` statement in
`ec2spot/v1.2.0`. No further module change.

## Testing

- `pkg/profile`: schema accepts `iam.allowBedrock`; round-trips; merges
  (pointer-bool, nil-safe) through inheritance.
- `pkg/compiler`: `enableBedrock` truth table (useBedrock / allowBedrock /
  neither / both); `buildL7ProxyHosts` includes `.amazonaws.com` for
  allowBedrock-only; userdata emits `/etc/km/bedrock.enabled` marker iff
  `EnableBedrock`; `service_hcl` `enable_bedrock` reflects the helper.
- `scripts/validate-all-profiles.sh`: 20/20 still valid.
- The jq-conditional config + provider-presence logic is shell, invisible to Go
  goldens → **live UAT** (a fresh g6.xlarge run via the mgmt-account rig):
  confirm each mode renders the expected providers and the routes return.
- Full `go test ./...` green.

## Deploy surface

- Schema field → create-handler's bundled `toolchain/km` must refresh:
  `make build-lambdas` + `km init --dry-run=false`. (Remote create flattens
  `extends` operator-side, so the field is resolved before upload.)
- IAM module (mantle) → already needs `make build-lambdas` + `km init` (bundled
  `infra/modules`).
- `base/gpu/serve` + leaf YAML + SOPS files → on disk (no rebuild for `--local`).
- Existing GPU sandboxes: `km destroy && km create` to pick up the new userdata
  marker + conditional config.

## Open dependency

Populating SOPS files needs the API-key values (SSM presence to verify) and the
shared SOPS KMS key provisioned. If a value is missing, the operator supplies it;
the spec does not assume the keys exist.
