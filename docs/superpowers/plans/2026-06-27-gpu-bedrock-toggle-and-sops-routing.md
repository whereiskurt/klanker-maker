# GPU Bedrock Toggle + Profile-Driven Cloud Routing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic `spec.iam.allowBedrock` toggle that grants Bedrock IAM without `useBedrock`'s agent-env hijack, and make the GPU Bifrost gateway include each cloud provider only when it's actually usable (SOPS keys present, or `allowBedrock` for keyless Bedrock).

**Architecture:** A compiler helper `enableBedrock(p)` (= `useBedrock || iam.allowBedrock`) drives three things: the `ec2spot_bedrock` IAM grant, the `.amazonaws.com` L7 metering host, and a `/etc/km/bedrock.enabled` marker written to the box. `base/gpu/serve`'s `initCommandsAppend` builds `config.json` with `jq`, adding `anthropic`/`openai` providers when their key is in `/etc/km/bifrost-env` and `bedrock` when the marker exists. Each GPU leaf carries its own SOPS file with the keys it needs.

**Tech Stack:** Go (`pkg/profile`, `pkg/compiler`), JSON Schema, Terraform/HCL (`ec2spot` module — already done), SOPS + AWS KMS, jq + bash (in-profile bring-up), SSM-driven live UAT.

## Global Constraints

- apiVersion stays `klankermaker.ai/v1alpha2`; no bump.
- `spec.iam.allowBedrock` is `*bool`, default absent ≡ false. `bedrock.go` (`mergeBedrockEnv`) stays keyed on `useBedrock` ONLY — never on `allowBedrock`.
- App account: `052251888500`, region `us-east-1`. SOPS KMS ARN: `arn:aws:kms:us-east-1:052251888500:alias/km-sandbox-secrets`.
- SSM key paths (SecureString, confirmed present): `/km/secrets/anthropic-api-key`, `/km/secrets/openai-api-key`.
- The `bedrock-mantle:CreateInference` IAM statement is already committed (`912eb730`) — do NOT re-add.
- Use `make build` after Go edits (ldflags), `go test ./... -timeout 600s` and check the real exit code (not a piped tail).
- `find -delete` for removing files (direct `rm` is intercepted); use `git rm` for tracked files.

---

### Task 1: `spec.iam.allowBedrock` schema + struct field

**Files:**
- Modify: `pkg/profile/types.go` (IAMSpec, ~line 594)
- Modify: `pkg/profile/schemas/sandbox_profile.schema.json` (`/properties/spec/properties/iam`)
- Test: `pkg/profile/inherit_test.go` (append)

**Interfaces:**
- Produces: `IAMSpec.AllowBedrock *bool` (yaml `allowBedrock,omitempty`).

- [ ] **Step 1: Write the failing test** — append to `pkg/profile/inherit_test.go`:

```go
// Phase 122: spec.iam.allowBedrock is an optional *bool that survives parse +
// merge and defaults to nil (absent).
func TestIAMAllowBedrock_ParseAndMerge(t *testing.T) {
	raw := []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  iam:
    roleSessionDuration: 1h
    allowedRegions: [us-east-1]
    allowBedrock: true
`)
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.IAM.AllowBedrock == nil || *p.Spec.IAM.AllowBedrock != true {
		t.Fatalf("allowBedrock: want *true, got %v", p.Spec.IAM.AllowBedrock)
	}
	// Absent → nil.
	raw2 := []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  iam: {roleSessionDuration: 1h, allowedRegions: [us-east-1]}
`)
	p2, _ := Parse(raw2)
	if p2.Spec.IAM.AllowBedrock != nil {
		t.Fatalf("absent allowBedrock should be nil, got %v", *p2.Spec.IAM.AllowBedrock)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/profile/ -run TestIAMAllowBedrock_ParseAndMerge -v`
Expected: compile error `p.Spec.IAM.AllowBedrock undefined`.

- [ ] **Step 3: Add the struct field** — in `pkg/profile/types.go`, inside `type IAMSpec struct` after `AllowedSecretPaths`:

```go
	// AllowBedrock grants the sandbox role Bedrock IAM (InvokeModel +
	// bedrock-mantle) for the on-box Bifrost gateway WITHOUT useBedrock's agent
	// env injection. Decoupled from spec.execution.useBedrock. Default nil/false.
	AllowBedrock *bool `yaml:"allowBedrock,omitempty"`
```

- [ ] **Step 4: Add the schema property** — in `pkg/profile/schemas/sandbox_profile.schema.json`, under `/properties/spec/properties/iam/properties`, add a sibling to `allowedSecretPaths`:

```json
"allowBedrock": {
  "type": "boolean",
  "description": "Grant the sandbox role Bedrock IAM (InvokeModel + bedrock-mantle) for the Bifrost gateway without useBedrock's agent-env injection. Default false."
}
```

(The `iam` block is `additionalProperties: false`; this makes `allowBedrock` an accepted key. Do not add it to `required`.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/profile/ -run TestIAMAllowBedrock_ParseAndMerge -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/profile/types.go pkg/profile/schemas/sandbox_profile.schema.json pkg/profile/inherit_test.go
git commit -m "feat(122): add spec.iam.allowBedrock (*bool) field + JSON schema"
```

---

### Task 2: `enableBedrock` helper + wire IAM, L7, and the box marker

**Files:**
- Create: `pkg/compiler/enable_bedrock.go`
- Modify: `pkg/compiler/service_hcl.go:787`
- Modify: `pkg/compiler/userdata.go` (`buildL7ProxyHosts` ~5478; `userDataParams` ~5010; params set ~5606; template before line 4833)
- Test: `pkg/compiler/enable_bedrock_test.go`, `pkg/compiler/userdata_test.go` (append)

**Interfaces:**
- Consumes: `IAMSpec.AllowBedrock` (Task 1).
- Produces: `func enableBedrock(p *profile.SandboxProfile) bool`; `userDataParams.EnableBedrock bool`; userdata emits `/etc/km/bedrock.enabled` iff true.

- [ ] **Step 1: Write the failing helper test** — create `pkg/compiler/enable_bedrock_test.go`:

```go
package compiler

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func boolp(b bool) *bool { return &b }

func TestEnableBedrock(t *testing.T) {
	cases := []struct {
		name   string
		use    bool
		allow  *bool
		expect bool
	}{
		{"neither", false, nil, false},
		{"useBedrock only", true, nil, true},
		{"allowBedrock only", false, boolp(true), true},
		{"allow false", false, boolp(false), false},
		{"both", true, boolp(true), true},
	}
	for _, c := range cases {
		p := &profile.SandboxProfile{}
		p.Spec.Execution.UseBedrock = c.use
		p.Spec.IAM.AllowBedrock = c.allow
		if got := enableBedrock(p); got != c.expect {
			t.Errorf("%s: enableBedrock=%v want %v", c.name, got, c.expect)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run TestEnableBedrock -v`
Expected: compile error `undefined: enableBedrock`.

- [ ] **Step 3: Implement the helper** — create `pkg/compiler/enable_bedrock.go`:

```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/compiler/ -run TestEnableBedrock -v`
Expected: PASS.

- [ ] **Step 5: Wire `service_hcl.go`** — change line 787 from `EnableBedrock: p.Spec.Execution.UseBedrock,` to:

```go
		EnableBedrock:     enableBedrock(p),
```

- [ ] **Step 6: Write the failing L7 test** — append to `pkg/compiler/userdata_test.go`:

```go
// Phase 122: allowBedrock (useBedrock=false) still adds the Bedrock L7 host so
// Bifrost->Bedrock calls are MITM-metered.
func TestL7ProxyHostsAllowBedrock(t *testing.T) {
	p := baseProfile() // same constructor TestL7ProxyHostsWithBedrock uses
	p.Spec.Execution.UseBedrock = false
	p.Spec.IAM.AllowBedrock = boolp(true)
	got := buildL7ProxyHosts(p)
	if !strings.Contains(got, ".amazonaws.com") {
		t.Errorf("allowBedrock must add .amazonaws.com; got %q", got)
	}
}
```

- [ ] **Step 7: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run TestL7ProxyHostsAllowBedrock -v`
Expected: FAIL (`.amazonaws.com` absent — gate is still `UseBedrock`).

- [ ] **Step 8: Wire `buildL7ProxyHosts`** — in `pkg/compiler/userdata.go` (~5478) change `if p.Spec.Execution.UseBedrock {` to:

```go
	if enableBedrock(p) {
```

- [ ] **Step 9: Run to verify L7 test passes + existing L7 test still passes**

Run: `go test ./pkg/compiler/ -run 'TestL7ProxyHosts' -v`
Expected: both PASS (`TestL7ProxyHostsWithBedrock` unchanged — useBedrock=true still hits the branch).

- [ ] **Step 10: Write the failing marker test** — append to `pkg/compiler/userdata_test.go`:

```go
// Phase 122: userdata writes the /etc/km/bedrock.enabled marker iff bedrock is
// enabled (so base/gpu/serve's jq config includes the bedrock provider).
func TestUserdataBedrockMarker(t *testing.T) {
	p := baseProfile()
	p.Spec.IAM.AllowBedrock = boolp(true)
	ud, err := generateUserData(p, "sb-bedrock-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !strings.Contains(ud, "touch /etc/km/bedrock.enabled") {
		t.Errorf("expected bedrock marker emission when allowBedrock")
	}
	p.Spec.IAM.AllowBedrock = boolp(false)
	ud2, err := generateUserData(p, "sb-bedrock-2", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if strings.Contains(ud2, "touch /etc/km/bedrock.enabled") {
		t.Errorf("marker must be absent when bedrock disabled")
	}
}
```

(`generateUserData(p, id, nil, "my-bucket", false, nil)` is the exact signature the other userdata tests use, e.g. line 36.)

- [ ] **Step 11: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserdataBedrockMarker -v`
Expected: FAIL (marker never emitted).

- [ ] **Step 12: Add the param field** — in `pkg/compiler/userdata.go`, inside `type userDataParams struct` (near `InitCommands`, ~5010):

```go
	// EnableBedrock writes the /etc/km/bedrock.enabled marker so the GPU
	// Bifrost config (base/gpu/serve initCommandsAppend) includes the bedrock
	// provider. Set from enableBedrock(p).
	EnableBedrock bool
```

- [ ] **Step 13: Set the param** — near line 5606 (`params.InitCommands = p.Spec.Execution.InitCommands`) add:

```go
	params.EnableBedrock = enableBedrock(p)
```

- [ ] **Step 14: Emit the marker in the template** — in `pkg/compiler/userdata.go`, immediately BEFORE the InitCommands block at line 4833 (`{{- if or .InitCommands .InitScripts }}`), insert:

```
{{- if .EnableBedrock }}
# Phase 122: marker read by base/gpu/serve's jq config build to include the
# bedrock provider. Written before km-init.sh (initCommandsAppend) runs.
mkdir -p /etc/km && touch /etc/km/bedrock.enabled
{{- end }}
```

- [ ] **Step 15: Run the marker + full compiler suite**

Run: `go test ./pkg/compiler/ -count=1 -timeout 300s`
Expected: ok (marker test passes; no golden regressions — `EnableBedrock=false` for learn.v2 etc. emits nothing).

- [ ] **Step 16: Commit**

```bash
git add pkg/compiler/enable_bedrock.go pkg/compiler/enable_bedrock_test.go pkg/compiler/service_hcl.go pkg/compiler/userdata.go pkg/compiler/userdata_test.go
git commit -m "feat(122): enableBedrock helper drives IAM + L7 host + /etc/km/bedrock.enabled marker"
```

---

### Task 3: `base/gpu/serve` — conditional Bifrost config via jq

**Files:**
- Modify: `profiles/base/gpu/serve.yaml` (initCommandsAppend, lines 109–128)
- Test: live UAT in Task 5 (shell logic; no Go golden). Local gate: `km validate` of a GPU leaf.

**Interfaces:**
- Consumes: `/etc/km/bedrock.enabled` marker (Task 2); `/etc/km/bifrost-env` key presence.
- Produces: `/etc/km/bifrost-data/config.json` with only-usable providers.

- [ ] **Step 1: Reorder + replace the config step.** In `profiles/base/gpu/serve.yaml`, the bring-up currently writes a static `config.json` (lines 109–121) and only later (line 124) populates `/etc/km/bifrost-env`. Replace BOTH so bifrost-env is populated first, then `config.json` is built conditionally. Replace the block from line 108 (the `- |` that begins `mkdir -p /etc/km/bifrost-data`) through line 124 (the `touch /etc/km/bifrost-env && grep ...` item) with:

```yaml
      # Populate /etc/km/bifrost-env from SOPS-injected keys FIRST so the config
      # build below can detect which providers are usable.
      - "mkdir -p /etc/km/bifrost-data && touch /etc/km/bifrost-env && grep -hE '^(ANTHROPIC_API_KEY|OPENAI_API_KEY)=' /etc/sandbox-secrets.env >> /etc/km/bifrost-env 2>/dev/null || true"
      # Build Bifrost config.json with jq — include a provider ONLY when usable:
      # vllm-local always; anthropic/openai if their key is in bifrost-env;
      # bedrock if /etc/km/bedrock.enabled exists (spec.iam.allowBedrock:true).
      - |
        set -e
        VLLM='{"keys":[{"name":"vllm-key","value":"dummy","models":["*"],"weight":1.0}],"network_config":{"base_url":"http://127.0.0.1:8000","default_request_timeout_in_seconds":120},"custom_provider_config":{"base_provider_type":"openai","allow_private_network":true,"allowed_requests":{"chat_completion":true,"chat_completion_stream":true,"responses":true,"responses_stream":true}}}'
        PROVIDERS=$(jq -nc --argjson v "$VLLM" '{"vllm-local":$v}')
        if grep -qE '^ANTHROPIC_API_KEY=.+' /etc/km/bifrost-env 2>/dev/null; then
          PROVIDERS=$(echo "$PROVIDERS" | jq -c '. + {"anthropic":{"keys":[{"name":"anthropic-direct","value":"env.ANTHROPIC_API_KEY","models":["*"],"weight":1.0}]}}')
        fi
        if grep -qE '^OPENAI_API_KEY=.+' /etc/km/bifrost-env 2>/dev/null; then
          PROVIDERS=$(echo "$PROVIDERS" | jq -c '. + {"openai":{"keys":[{"name":"openai-frontier","value":"env.OPENAI_API_KEY","models":["*"],"weight":1.0}]}}')
        fi
        if [ -f /etc/km/bedrock.enabled ]; then
          PROVIDERS=$(echo "$PROVIDERS" | jq -c '. + {"bedrock":{"keys":[{"name":"bedrock-iam","models":["*"],"weight":1.0,"bedrock_key_config":{"region":"us-east-1"}}]}}')
        fi
        jq -nc --argjson p "$PROVIDERS" '{"config_store":{"enabled":false},"providers":$p}' > /etc/km/bifrost-data/config.json
        chown -R 1000:1000 /etc/km/bifrost-data
```

(Leave the `vllm.service` + `bifrost.service` unit heredocs (lines 69–107) and the `docker pull` / `daemon-reload` / `enable --now` items (lines 125–128) unchanged. `base/gpu/serve` does NOT set `iam.allowBedrock` and does NOT declare `spec.secrets` — it stays key-agnostic.)

- [ ] **Step 2: Validate a GPU leaf still resolves + validates**

Run: `./km validate profiles/gpu-qwen-12x.yaml`
Expected: `ok` (the merged profile validates; the jq block is opaque shell text).

- [ ] **Step 3: Confirm the merged km-init payload carries the jq logic** — quick Go check:

```bash
cat > pkg/profile/zz_jq_test.go <<'EOF'
package profile
import ("strings";"testing")
func TestBifrostJqInPayload(t *testing.T){
  rp,_:=Resolve("gpu-qwen-12x",[]string{"../../profiles"})
  j:=strings.Join(rp.Spec.Execution.InitCommands,"\n")
  for _,m:=range []string{"bedrock.enabled","jq -nc","vllm-local","ANTHROPIC_API_KEY=.+"}{
    if !strings.Contains(j,m){t.Errorf("missing %q in merged payload",m)}
  }
}
EOF
go test ./pkg/profile/ -run TestBifrostJqInPayload -v
find pkg/profile -name zz_jq_test.go -delete
```

Expected: PASS, then file removed.

- [ ] **Step 4: Commit**

```bash
git add profiles/base/gpu/serve.yaml
git commit -m "feat(122): base/gpu/serve builds Bifrost config with jq (providers only when usable)"
```

---

### Task 4: Per-leaf SOPS files + retire `gpu.enc.yaml`

**Files:**
- Create: `secrets/qwen.enc.yaml`, `secrets/glm.enc.yaml`, `secrets/kimi.enc.yaml`, `secrets/llama-hf.enc.yaml`
- Delete: `secrets/gpu.enc.yaml`
- Modify: `profiles/gpu-qwen-12x.yaml`, `gpu-qwen-48x.yaml`, `gpu-glmair-12x.yaml`, `gpu-glm46-48x.yaml`, `gpu-kimidev-12x.yaml` (add `spec.secrets.sopsFile`); `gpu-llama-12x.yaml`, `gpu-llama-48x.yaml` already reference `./secrets/llama-hf.enc.yaml`.

**Interfaces:**
- Consumes: SSM keys + KMS (Global Constraints).
- Produces: each leaf's `spec.secrets.sopsFile` decrypts to `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` (+ `HF_TOKEN` for llama) in `/etc/sandbox-secrets.env`.

> NOTE: this task touches real secret values; run the SOPS steps in your own
> terminal with `AWS_PROFILE=klanker-application`. The plaintext temp files MUST
> be deleted (`find -delete`) and never committed — only the `.enc.yaml` outputs.

- [ ] **Step 1: Pull the API key values from SSM into shell vars** (terminal):

```bash
export AWS_PROFILE=klanker-application AWS_REGION=us-east-1
ANTH=$(aws ssm get-parameter --name /km/secrets/anthropic-api-key --with-decryption --query Parameter.Value --output text)
OAI=$(aws ssm get-parameter --name /km/secrets/openai-api-key --with-decryption --query Parameter.Value --output text)
KMS='arn:aws:kms:us-east-1:052251888500:alias/km-sandbox-secrets'
```

- [ ] **Step 2: Build + encrypt the three API-key-only leaf bundles** (qwen/glm/kimi):

```bash
for f in qwen glm kimi; do
  printf 'ANTHROPIC_API_KEY: %s\nOPENAI_API_KEY: %s\n' "$ANTH" "$OAI" > "/tmp/$f.plain.yaml"
  sops --encrypt --kms "$KMS" "/tmp/$f.plain.yaml" > "secrets/$f.enc.yaml"
  find /tmp -maxdepth 1 -name "$f.plain.yaml" -delete
done
ls -la secrets/qwen.enc.yaml secrets/glm.enc.yaml secrets/kimi.enc.yaml
```

- [ ] **Step 3: Build + encrypt the llama bundle (HF_TOKEN + API keys).** Supply your HF token:

```bash
HF='hf_REPLACE_WITH_YOUR_TOKEN'
printf 'HF_TOKEN: %s\nANTHROPIC_API_KEY: %s\nOPENAI_API_KEY: %s\n' "$HF" "$ANTH" "$OAI" > /tmp/llama.plain.yaml
sops --encrypt --kms "$KMS" /tmp/llama.plain.yaml > secrets/llama-hf.enc.yaml
find /tmp -maxdepth 1 -name llama.plain.yaml -delete
```

- [ ] **Step 4: Verify each bundle decrypts to the expected keys** (no values printed):

```bash
for f in qwen glm kimi llama-hf; do
  echo "$f: $(sops decrypt --output-type dotenv secrets/$f.enc.yaml | cut -d= -f1 | paste -sd, -)"
done
```

Expected: `qwen/glm/kimi: ANTHROPIC_API_KEY,OPENAI_API_KEY` and `llama-hf: HF_TOKEN,ANTHROPIC_API_KEY,OPENAI_API_KEY`.

- [ ] **Step 5: Reference the SOPS file in the qwen/glm/kimi leaves.** In each of `profiles/gpu-qwen-12x.yaml`, `gpu-qwen-48x.yaml`, `gpu-glmair-12x.yaml`, `gpu-glm46-48x.yaml`, `gpu-kimidev-12x.yaml`, add under `spec:` (matching the llama leaves' existing two-line block, pointing at the leaf's own file — qwen leaves → `qwen.enc.yaml`, glm leaves → `glm.enc.yaml`, kimi → `kimi.enc.yaml`):

```yaml
  secrets:
    sopsFile: ./secrets/qwen.enc.yaml
```

- [ ] **Step 6: Retire the orphaned bundle:**

```bash
git rm secrets/gpu.enc.yaml
```

- [ ] **Step 7: Validate the full inventory**

Run: `bash scripts/validate-all-profiles.sh`
Expected: `all 20 profiles valid`.

- [ ] **Step 8: Commit** (only `.enc.yaml` + profile YAML — confirm no plaintext staged):

```bash
git status --short
git add secrets/qwen.enc.yaml secrets/glm.enc.yaml secrets/kimi.enc.yaml secrets/llama-hf.enc.yaml \
        profiles/gpu-qwen-12x.yaml profiles/gpu-qwen-48x.yaml profiles/gpu-glmair-12x.yaml \
        profiles/gpu-glm46-48x.yaml profiles/gpu-kimidev-12x.yaml
git rm --cached secrets/gpu.enc.yaml 2>/dev/null; git add -u secrets/
git commit -m "feat(122): per-leaf SOPS bundles (anthropic+openai, +HF for llama); retire gpu.enc.yaml"
```

---

### Task 5: Full suite + live two-mode UAT

**Files:** none (verification only).

- [ ] **Step 1: Build + full test suite**

```bash
make build
make build-lambdas
go test ./... -count=1 -timeout 600s; echo "EXIT=$?"
```

Expected: `EXIT=0`, zero FAIL.

- [ ] **Step 2: Deploy to refresh the create-handler toolchain + IAM**

```bash
AWS_PROFILE=klanker-application ./km init --dry-run=false
```

Expected: completes; create-handler `toolchain/km` + `ec2spot` module (mantle grant) refreshed.

- [ ] **Step 3: Live UAT — default-keys mode.** Recreate a GPU box from a leaf that has a SOPS file and NO `allowBedrock` (e.g. a temporary CPU-rehearsal variant extending `base/gpu/serve` + `secrets/qwen.enc.yaml`, or `gpu-qwen-12x` if G-quota is available). After boot, via SSM:

```bash
jq -r '.providers | keys | join(",")' /etc/km/bifrost-data/config.json
test ! -f /etc/km/bedrock.enabled && echo "marker absent (expected)"
```

Expected providers: `anthropic,openai,vllm-local` (no `bedrock`); marker absent.

- [ ] **Step 4: Live UAT — keyless mode.** Recreate the same leaf with `spec.iam.allowBedrock: true` and its `spec.secrets` removed. After boot, via SSM:

```bash
jq -r '.providers | keys | join(",")' /etc/km/bifrost-data/config.json
test -f /etc/km/bedrock.enabled && echo "marker present (expected)"
curl -s --max-time 40 http://localhost:8001/openai/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"bedrock/us.anthropic.claude-sonnet-4-6","messages":[{"role":"user","content":"Reply: BEDROCKMODEOK"}],"max_tokens":15}'
```

Expected providers include `bedrock` (not `anthropic`/`openai`); the Bedrock route returns (instance-role SigV4, now incl. the mantle grant for gpt-oss).

- [ ] **Step 5: Tear down the UAT box(es)** and confirm clean.

- [ ] **Step 6: Final commit (docs)** — update `docs/gpu-model-serving.md` + the CLAUDE.md Phase 122 block to document `spec.iam.allowBedrock`, the two modes, and the per-leaf SOPS wiring.

```bash
git add docs/gpu-model-serving.md CLAUDE.md
git commit -m "docs(122): document allowBedrock + two-mode GPU cloud routing"
```

---

## Self-Review

**Spec coverage:** field (T1) ✓; IAM-without-hijack via `enableBedrock` + `bedrock.go` untouched (T2) ✓; L7 metering (T2) ✓; marker (T2) ✓; conditional jq config incl. anthropic/openai/bedrock (T3) ✓; per-leaf SOPS + retire gpu.enc.yaml + llama-hf (T4) ✓; mantle grant already committed (noted) ✓; tests + deploy + two-mode UAT (T5) ✓.

**Placeholder scan:** the only intentional operator-supplied values are the HF token (T4 S3) and the SSM-sourced key values — both are real runtime secrets, not plan gaps. Test helpers use the codebase's real `baseProfile()` constructor and `generateUserData(p, id, nil, "my-bucket", false, nil)` signature (verified against `pkg/compiler/userdata_test.go`).

**Type consistency:** `enableBedrock(p *profile.SandboxProfile) bool`, `IAMSpec.AllowBedrock *bool`, `userDataParams.EnableBedrock bool` used consistently across T1–T3 and the `EnableBedrock` HCL field (pre-existing) set from the same helper.
