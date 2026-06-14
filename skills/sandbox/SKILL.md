---
name: sandbox
description: "Self-census: identity, capabilities, network position, privilege/restrictions, Slack readiness, posture summary"
---

# klanker:sandbox — Sandbox Self-Census

Run this skill **first** whenever you need to understand who you are, what you can do, where you
sit on the network, and what restrictions are in place — and why. A single invocation gives you
full self-knowledge grounded in both the declarative profile on disk and live runtime probes.

**Invoke this skill first** before using `klanker:email`, `klanker:operator`, or `klanker:slack`.

## Cross-references

- `klanker:email` — send/receive email between sandboxes and to the operator
- `klanker:slack` — post from inside a sandbox to its per-sandbox Slack channel (see Section E for readiness check)
- `klanker:operator` — natural-language requests to the operator inbox
- `klanker:init` — operator-side companion (one-time platform setup that provisions the env this skill detects)

---

## Preamble: Read the on-box profile (graceful fallback)

```bash
KM_PROFILE=$(cat /opt/km/.km-profile.yaml 2>/dev/null)
if [ -z "$KM_PROFILE" ]; then
  echo "[INFO] /opt/km/.km-profile.yaml absent — pre-Phase-113 sandbox"
  echo "[INFO] Falling back to env-var census + live probes only"
  PROFILE_AVAILABLE=0
else
  PROFILE_AVAILABLE=1
  echo "[INFO] Profile loaded from /opt/km/.km-profile.yaml"
fi
```

Every profile-derived check below is guarded on `PROFILE_AVAILABLE`. When `PROFILE_AVAILABLE=0`
the skill continues via env vars and live probes — it never errors on the missing file.

---

## Section A: Identity & Agent

Determine who you are. If `KM_SANDBOX_ID` is empty you are **not in a KM sandbox** — stop and
inform the user. All other klanker skills require a sandbox environment.

```bash
echo "=== A: Identity ==="
echo "KM_SANDBOX_ID=$KM_SANDBOX_ID"
echo "KM_SANDBOX_ALIAS=$KM_SANDBOX_ALIAS"
echo "KM_SANDBOX_EMAIL=$KM_SANDBOX_EMAIL"
echo "KM_EMAIL_ADDRESS=$KM_EMAIL_ADDRESS"
echo "KM_OPERATOR_EMAIL=$KM_OPERATOR_EMAIL"
echo "KM_SANDBOX_DOMAIN=$KM_SANDBOX_DOMAIN"
echo "KM_ARTIFACTS_BUCKET=$KM_ARTIFACTS_BUCKET"
echo "KM_AGENT=$KM_AGENT"          # claude | codex — default agent for this box

if [ -z "$KM_SANDBOX_ID" ]; then
  echo "[STOP] Not in a KM sandbox. klanker skills are unavailable."
  exit 0
fi

# Agent default from profile (cross-check KM_AGENT)
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  PROFILE_AGENT=$(echo "$KM_PROFILE" | grep -A2 '^  agent:' | grep 'default:' | awk '{print $2}')
  echo "Profile agent.default: ${PROFILE_AGENT:-claude (implicit)}"
fi
```

---

## Section B: Capability Census

Enumerate what this box can do — which bridges it serves, which channels are wired, and which
runtime features are available. All bash; no new binary.

```bash
echo "=== B: Capability Census ==="

# B1. Sidecar helper binaries
# km-github present → this box serves the GitHub inbound bridge
# km-h1 present     → this box serves the HackerOne inbound bridge
for bin in km-send km-recv km-slack km-presence km-github km-h1; do
  if test -x /opt/km/bin/$bin; then
    echo "$bin: present"
  else
    echo "$bin: absent"
  fi
done

# B2. Channels wired
echo "--- Channels ---"
# Email
echo "KM_SANDBOX_EMAIL=$KM_SANDBOX_EMAIL"
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  echo "Email signing policy:    $(echo "$KM_PROFILE" | grep -A10 '^    email:' | grep 'signing:' | awk '{print $2}' | head -1)"
  echo "Email verifyInbound:     $(echo "$KM_PROFILE" | grep -A10 '^    email:' | grep 'verifyInbound:' | awk '{print $2}' | head -1)"
  echo "Email encryption policy: $(echo "$KM_PROFILE" | grep -A10 '^    email:' | grep 'encryption:' | awk '{print $2}' | head -1)"
fi

# Slack post-back
echo "KM_NOTIFY_SLACK_ENABLED=$KM_NOTIFY_SLACK_ENABLED"
echo "KM_SLACK_CHANNEL_ID=$KM_SLACK_CHANNEL_ID"
echo "KM_SLACK_BRIDGE_URL=$KM_SLACK_BRIDGE_URL"
echo "KM_SLACK_THREAD_TS=$KM_SLACK_THREAD_TS"

# Inbound pollers (live probe via systemctl)
for svc in km-slack-inbound-poller km-github-inbound-poller km-h1-inbound-poller; do
  STATUS=$(systemctl is-active ${svc}.service 2>/dev/null || echo "not-found")
  echo "${svc}: $STATUS"
done

# Inbound queue URLs (populated when inbound is enabled)
echo "KM_GITHUB_INBOUND_QUEUE_URL=$KM_GITHUB_INBOUND_QUEUE_URL"
echo "KM_H1_INBOUND_QUEUE_URL=$KM_H1_INBOUND_QUEUE_URL"

# B3. Runtime features (profile-guarded)
echo "--- Runtime features ---"
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  VS_CODE=$(echo "$KM_PROFILE" | grep -A3 'vscode:' | grep 'enabled:' | awk '{print $2}' | head -1)
  DESKTOP=$(echo "$KM_PROFILE" | grep -A3 'desktop:' | grep 'enabled:' | awk '{print $2}' | head -1)
  echo "VS Code Remote-SSH: ${VS_CODE:-false}"
  echo "Desktop (KasmVNC):  ${DESKTOP:-false}"

  # Budget / lifecycle
  echo "Budget/TTL from profile:"
  echo "$KM_PROFILE" | grep -E '(ttl|idle|maxSpendUSD|computeBudget):' | head -6 | sed 's/^/  /'

  # Additional storage
  ADD_VOL=$(echo "$KM_PROFILE" | grep 'additionalVolume:' | head -1)
  ADD_SNAP=$(echo "$KM_PROFILE" | grep 'additionalSnapshots:' | head -1)
  [ -n "$ADD_VOL"  ] && echo "additionalVolume:    $ADD_VOL"
  [ -n "$ADD_SNAP" ] && echo "additionalSnapshots: $ADD_SNAP"
else
  echo "(profile absent — VS Code, desktop, budget, storage: check profile when available)"
fi

# B4. Always-present systemd sidecars
echo "--- Core sidecars ---"
for svc in km-http-proxy km-dns-proxy km-audit-log km-tracing km-presence km-mail-poller; do
  STATUS=$(systemctl is-active ${svc}.service 2>/dev/null || echo "not-found")
  echo "${svc}: $STATUS"
done

# Signing key access (needed by km-send and km-slack)
aws ssm get-parameter --name "/sandbox/$KM_SANDBOX_ID/signing-key" \
  --with-decryption --query 'Parameter.Value' --output text > /dev/null 2>&1 \
  && echo "signing-key: OK" || echo "signing-key: MISSING (km-send --no-sign required for external recipients)"
```

**Key interpretation:**
- `km-github` present → this box dispatches GitHub PR comment turns; the GitHub inbound poller
  should be `active`.
- `km-h1` present → this box dispatches HackerOne report comment turns.
- `km-send`/`km-recv` always present on Phase-63+ sandboxes.
- Signing key is shared by `km-slack` (Slack envelope signing) and `km-send` (sandbox-to-sandbox
  email). If missing and `signing: required`, both will fail. External email: use `km-send --no-sign`.

**External email exception:** For non-sandbox recipients (Gmail, corporate email), always pass
`--no-sign` — it skips the signing key fetch entirely. Inbound replies from non-sandbox senders
must include `KM-AUTH: <safe-phrase>` in the body; otherwise `km-mail-poller` drops them silently
to `/var/mail/km/skipped/`.

---

## Section C: Network Position

Understand what egress is permitted and how it is enforced. Network section is passive plus
**exactly two safe-active curls** — the only traffic-generating step (expect these in `km otel`).

```bash
echo "=== C: Network Position ==="

# C1. Infer enforcement mode — there is NO KM_ENFORCEMENT env var on-box; infer from runtime signals
EBPF_ACTIVE=$(systemctl is-active km-ebpf-enforcer.service 2>/dev/null || echo inactive)
IPTABLES_DNAT=$(iptables -t nat -L OUTPUT -n 2>/dev/null | grep -c DNAT || echo 0)

if [ "$EBPF_ACTIVE" = "active" ] && [ "$IPTABLES_DNAT" -gt 0 ]; then
  INFERRED_MODE="both"
elif [ "$EBPF_ACTIVE" = "active" ]; then
  INFERRED_MODE="ebpf"
elif [ "$IPTABLES_DNAT" -gt 0 ]; then
  INFERRED_MODE="proxy"
else
  INFERRED_MODE="unknown"
fi
echo "Inferred enforcement mode: $INFERRED_MODE (ebpf-enforcer=$EBPF_ACTIVE, DNAT rules=$IPTABLES_DNAT)"

if [ "$PROFILE_AVAILABLE" = "1" ]; then
  PROFILE_MODE=$(echo "$KM_PROFILE" | grep -A2 '^  network:' | grep 'enforcement:' | awk '{print $2}' | head -1)
  echo "Profile spec.network.enforcement: ${PROFILE_MODE:-(not set, default=proxy)}"
  if [ -n "$PROFILE_MODE" ] && [ "$PROFILE_MODE" != "$INFERRED_MODE" ]; then
    echo "[WARN] enforcement mode mismatch: profile=$PROFILE_MODE inferred=$INFERRED_MODE"
  fi
fi

# C2. Proxy / CA / eBPF signals
echo "HTTP_PROXY=$HTTP_PROXY"
echo "HTTPS_PROXY=$HTTPS_PROXY"
[ -f /usr/local/share/ca-certificates/km-proxy-ca.crt ] \
  && echo "km-proxy CA: present (proxy/both mode)" \
  || echo "km-proxy CA: absent"
KM_EBPF_CGROUP="/sys/fs/cgroup/km.slice/km-${KM_SANDBOX_ID}.scope"
[ -d "$KM_EBPF_CGROUP" ] \
  && echo "eBPF cgroup: present ($KM_EBPF_CGROUP)" \
  || echo "eBPF cgroup: absent"
echo "DNS resolver: $(cat /etc/resolv.conf | grep ^nameserver | head -2)"

# C3. Egress allowlist from profile
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  echo "--- Egress allowlist (from profile) ---"
  echo "$KM_PROFILE" | grep -A50 'network:' | grep -E '^\s+-\s+' | head -20 | sed 's/^/  /'
fi

# C4. Safe-active confirmation: ONE allowed + ONE known-blocked host
# NOTE: These are the ONLY two outbound requests this section makes.
# They will appear in `km otel` output — this is expected operator behavior.
echo "--- Active egress probe (2 requests only) ---"

# Pick an allowed host from the allowlist (profile) or fall back to a commonly-permitted domain
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  ALLOWED_HOST=$(echo "$KM_PROFILE" | grep -A50 'network:' | grep -E '^\s+-\s+[a-z]' | head -1 | awk '{print $2}' | tr -d '"')
fi
ALLOWED_HOST=${ALLOWED_HOST:-api.anthropic.com}  # always in the allowlist for Claude sandboxes

echo "Testing ALLOWED host: $ALLOWED_HOST"
ALLOWED_CODE=$(curl --max-time 5 -sS -o /dev/null -w '%{http_code}' "https://$ALLOWED_HOST/" 2>&1 || echo "blocked/timeout")
echo "  Result: $ALLOWED_CODE (expect 2xx/3xx/4xx = reachable; timeout/000 = blocked)"

BLOCKED_HOST="evil.example.com"  # never in any allowlist
echo "Testing BLOCKED host: $BLOCKED_HOST"
BLOCKED_CODE=$(curl --max-time 5 -sS -o /dev/null -w '%{http_code}' "https://$BLOCKED_HOST/" 2>&1 || echo "blocked/timeout")
echo "  Result: $BLOCKED_CODE (expect timeout/000 = correctly blocked)"
```

---

## Section D: Privilege & Restrictions

Each restriction is explained so you stop fighting locked-down behavior.

```bash
echo "=== D: Privilege & Restrictions ==="

# D1. Passwordless sudo probe
if sudo -n true 2>/dev/null; then
  SUDO_STATUS="yes — passwordless sudo available"
else
  SUDO_STATUS="no — sudo requires a password or is disabled"
fi
echo "Passwordless sudo: $SUDO_STATUS"

# Cross-check against profile
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  PROFILE_PRIV=$(echo "$KM_PROFILE" | grep 'privileged:' | awk '{print $2}' | head -1)
  echo "Profile spec.execution.privileged: ${PROFILE_PRIV:-(not set, default=false)}"
  if [ "$PROFILE_PRIV" = "true" ] && ! sudo -n true 2>/dev/null; then
    echo "[WARN] Profile says privileged=true but sudo probe failed — box may need recreate"
  fi
  if [ "${PROFILE_PRIV:-false}" = "false" ] && sudo -n true 2>/dev/null; then
    echo "[WARN] Unexpected sudo access — profile says privileged=false"
  fi
fi

# Why: spec.execution.privileged: false (the default) means no wheel/sudo access.
# This is intentional — do NOT attempt to escalate privileges.
# Privileged sandboxes are opt-in and must be declared in the profile.
echo ""
echo "Why each restriction exists:"
echo "  - No sudo:        spec.execution.privileged: false (default). Intentional sandbox isolation."
echo "                    Do not attempt privilege escalation — it will fail and log to the audit stream."

# D2. Git-ref enforcement
echo "  - Git refs:       KM_ALLOWED_REFS=$KM_ALLOWED_REFS"
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  ALLOWED_REFS=$(echo "$KM_PROFILE" | grep -A5 'sourceAccess:' | grep 'allowedRefs:' | head -1)
  echo "                    Profile: $ALLOWED_REFS"
fi
echo "                    The poller enforces this list — pushes to non-allowed refs are rejected."

# D3. Secret paths
if [ "$PROFILE_AVAILABLE" = "1" ]; then
  SECRET_PATHS=$(echo "$KM_PROFILE" | grep -A10 'allowedSecretPaths:' | grep -E '^\s+-' | head -5 | sed 's/^/                    /')
  if [ -n "$SECRET_PATHS" ]; then
    echo "  - Secret paths:   only these SSM paths are IAM-accessible from this role:"
    echo "$SECRET_PATHS"
  else
    echo "  - Secret paths:   spec.iam.allowedSecretPaths: (none declared — SSM access limited to sandbox key)"
  fi
fi

# D4. Network egress (summarize; details in Section C)
echo "  - Network egress: enforcement=$INFERRED_MODE — outbound connections outside the allowlist"
echo "                    are blocked at the kernel/proxy layer. This is not a transient error;"
echo "                    it is intentional policy. Check Section C allowlist for permitted hosts."
```

---

## Section E: Slack Publish-Back Readiness

Confirm the Slack post-back environment is wired before using `klanker:slack`.

```bash
echo "=== E: Slack Publish-Back Readiness ==="

ALL_SLACK_OK=1
for var in KM_NOTIFY_SLACK_ENABLED KM_SLACK_CHANNEL_ID KM_SLACK_BRIDGE_URL; do
  VAL=$(eval echo "\$$var")
  if [ -z "$VAL" ] || [ "$VAL" = "0" ]; then
    echo "$var: NOT SET — Slack posting unavailable"
    ALL_SLACK_OK=0
  else
    echo "$var: OK ($VAL)"
  fi
done

# KM_SLACK_THREAD_TS is populated only when the agent is inside a Slack-initiated thread
echo "KM_SLACK_THREAD_TS=${KM_SLACK_THREAD_TS:-(not set; replies will create a new thread)}"

# km-slack binary check
test -x /opt/km/bin/km-slack \
  && echo "km-slack binary: present" \
  || echo "km-slack binary: MISSING — recreate sandbox after km init --sidecars"

if [ "$ALL_SLACK_OK" = "1" ]; then
  echo "[OK] Slack post-back is ready. See klanker:slack for usage."
else
  echo "[WARN] Slack is not fully provisioned. Check notification.slack.enabled in the profile"
  echo "       and verify km init --dry-run=false has been run with the Slack bridge deployed."
fi
```

For the full Slack posting API (streaming transcripts, attachments, thread management), see
**`klanker:slack`**. Do NOT duplicate that skill's content here.

---

## Section F: Self-Diagnosis Summary

After running sections A–E, write a one-paragraph posture statement:

```
Who I am:       Sandbox $KM_SANDBOX_ID (alias $KM_SANDBOX_ALIAS), agent $KM_AGENT.
What I can do:  [list of present km-* helpers and active pollers]
Network:        Enforcement mode $INFERRED_MODE. Egress allowed to [list]. BLOCKED: everything else.
Restrictions:   [sudo yes/no; privileged reason; allowedRefs; secret paths]
Slack:          [ready / not provisioned]
```

Example posture statement (fill in your actual values):

> I am sandbox `sb-abc123` (alias `review-bot`), running as the `claude` agent. I have
> `km-send`, `km-recv`, `km-slack`, and `km-github` present, so I serve the GitHub inbound
> bridge. Email is signed (`signing: required`). Network enforcement is `both` (eBPF + proxy);
> egress is limited to `api.anthropic.com`, `github.com`, and `api.github.com` — all other
> outbound connections are blocked by policy, not by transient error. I have no sudo access
> (`privileged: false`) — this is intentional, so I will not attempt privilege escalation.
> Slack post-back is ready on channel `C0ABC123`.

Use this context when invoking `klanker:email`, `klanker:operator`, or `klanker:slack`.
