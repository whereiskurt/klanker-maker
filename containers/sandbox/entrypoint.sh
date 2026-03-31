#!/bin/bash
# km-sandbox entrypoint — container equivalent of EC2 user-data bootstrap
# Port of pkg/compiler/userdata.go for ECS / Docker / EKS substrates.
# Each section mirrors the section numbering in userdata.go for easy cross-reference.
#
# DO NOT EDIT — this file is managed by the km-sandbox-base-container-image build.
#
# Env vars read (all optional unless noted):
#   KM_SANDBOX_ID          - sandbox ID (required)
#   KM_ARTIFACTS_BUCKET    - S3 bucket for artifacts, CA cert, snapshots
#   KM_STATE_BUCKET        - S3 bucket for metadata
#   KM_PROXY_CA_CERT_S3    - s3://bucket/sidecars/km-proxy-ca.crt
#   KM_SECRET_PATHS        - comma-separated SSM parameter paths
#   KM_OTP_PATHS           - comma-separated SSM OTP paths (fetch+delete)
#   KM_INIT_COMMANDS       - base64-encoded JSON array of shell commands
#   KM_RSYNC_SNAPSHOT      - rsync snapshot name to restore from S3
#   KM_GITHUB_TOKEN_SSM    - SSM path for GitHub token
#   KM_GITHUB_ALLOWED_REFS - comma-separated allowed git refs
#   KM_PROFILE_ENV         - base64-encoded JSON object {KEY: value}
#   KM_EMAIL_ADDRESS       - sandbox email address
#   KM_OPERATOR_EMAIL      - operator notification address
#
# Env vars set by ECS task definition (do NOT overwrite):
#   SANDBOX_ID, HTTP_PROXY, HTTPS_PROXY, NO_PROXY,
#   CLAUDE_CODE_ENABLE_TELEMETRY, OTEL_*

set -euo pipefail

log()      { echo "[km-entrypoint] $*"; }
log_warn() { echo "[km-entrypoint] WARNING: $*" >&2; }
log_fail() { echo "[km-entrypoint] FATAL: $*" >&2; exit 1; }

# ============================================================
# SIGTERM / SIGINT handler — upload artifacts before exit
# (mirrors spot interruption handler section 6.5 of userdata.go)
# ============================================================
upload_artifacts() {
    local bucket="${KM_ARTIFACTS_BUCKET:-}"
    local sandbox_id="${KM_SANDBOX_ID:-}"
    if [ -z "$bucket" ] || [ -z "$sandbox_id" ]; then
        log_warn "upload_artifacts: KM_ARTIFACTS_BUCKET or KM_SANDBOX_ID not set, skipping"
        return 1
    fi
    log "Uploading /workspace to s3://${bucket}/artifacts/${sandbox_id}/ ..."
    aws s3 sync /workspace "s3://${bucket}/artifacts/${sandbox_id}/" --exclude ".git/*" || {
        log_warn "s3 sync encountered errors"
        return 1
    }
    log "Artifact upload complete"
}

_shutdown() {
    log "Shutdown signal received — uploading artifacts..."
    upload_artifacts || log_warn "Artifact upload failed during shutdown"
    exit 0
}
trap '_shutdown' TERM INT

# ============================================================
# Section 7: CA trust — CRITICAL (abort on failure)
# Mirrors userdata.go section 7 (budget/CA section)
# ============================================================
setup_ca_trust() {
    local cert_s3="${KM_PROXY_CA_CERT_S3:-}"
    if [ -z "$cert_s3" ]; then
        log "CA trust: KM_PROXY_CA_CERT_S3 not set — skipping"
        return 0
    fi
    log "Installing proxy CA cert from ${cert_s3} ..."
    aws s3 cp "${cert_s3}" /etc/pki/ca-trust/source/anchors/km-proxy-ca.crt \
        || log_fail "Failed to download proxy CA cert from ${cert_s3}"
    update-ca-trust \
        || log_fail "update-ca-trust failed"
    # Export CA bundle path for tools that bundle their own CA stores
    local bundle="/etc/pki/tls/certs/ca-bundle.crt"
    export SSL_CERT_FILE="${bundle}"
    export REQUESTS_CA_BUNDLE="${bundle}"
    export CURL_CA_BUNDLE="${bundle}"
    export NODE_EXTRA_CA_CERTS="${bundle}"
    log "CA trust configured (bundle: ${bundle})"
}

# ============================================================
# Section 3: Secret injection — CRITICAL (abort on failure)
# Mirrors userdata.go section 3
# ============================================================
inject_secrets() {
    local paths="${KM_SECRET_PATHS:-}"
    if [ -z "$paths" ]; then
        log "Secret injection: KM_SECRET_PATHS not set — skipping"
        return 0
    fi
    log "Injecting secrets from SSM..."
    IFS=',' read -ra PATH_LIST <<< "${paths}"
    for path in "${PATH_LIST[@]}"; do
        path="${path// /}"  # trim whitespace
        [ -z "$path" ] && continue
        local val
        val=$(aws ssm get-parameter \
            --name "${path}" \
            --with-decryption \
            --query "Parameter.Value" \
            --output text) \
            || log_fail "Failed to fetch secret: ${path}"
        # Derive env var name from last path component: uppercase, hyphens → underscores
        local env_name
        env_name=$(basename "${path}" | tr '[:lower:]' '[:upper:]' | tr '-' '_')
        export "${env_name}=${val}"
        log "Injected secret: ${path} -> ${env_name}"
    done
}

# ============================================================
# Section 3.5: OTP secrets — OPTIONAL (warn and continue)
# Mirrors userdata.go section 3.5
# ============================================================
inject_otp_secrets() {
    local paths="${KM_OTP_PATHS:-}"
    if [ -z "$paths" ]; then
        log "OTP secrets: KM_OTP_PATHS not set — skipping"
        return 0
    fi
    log "Injecting OTP secrets (delete-after-read)..."
    IFS=',' read -ra PATH_LIST <<< "${paths}"
    for path in "${PATH_LIST[@]}"; do
        path="${path// /}"
        [ -z "$path" ] && continue
        local val
        val=$(aws ssm get-parameter \
            --name "${path}" \
            --with-decryption \
            --query "Parameter.Value" \
            --output text 2>/dev/null) || {
            log_warn "OTP secret fetch failed (may have been consumed already): ${path}"
            continue
        }
        if [ -n "$val" ]; then
            # Derive env var name: KM_OTP_ prefix, last path segment, uppercased
            local env_name
            env_name="KM_OTP_$(basename "${path}" | tr '[:lower:]' '[:upper:]' | tr '-' '_')"
            export "${env_name}=${val}"
            log "OTP secret injected: ${path} -> ${env_name}"
            # Delete immediately after read (one-time use)
            aws ssm delete-parameter --name "${path}" 2>/dev/null || true
            log "OTP secret deleted from SSM: ${path}"
        fi
    done
}

# ============================================================
# Section 2.8: Profile environment variables — OPTIONAL
# Mirrors userdata.go section 2.8
# ============================================================
setup_profile_env() {
    local profile_env="${KM_PROFILE_ENV:-}"
    if [ -z "$profile_env" ]; then
        log "Profile env: KM_PROFILE_ENV not set — skipping"
        return 0
    fi
    log "Applying profile environment variables..."
    local pairs
    pairs=$(echo "${profile_env}" | base64 -d | jq -r 'to_entries[] | "\(.key)=\(.value)"' 2>/dev/null) || {
        log_warn "Failed to decode/parse KM_PROFILE_ENV (base64 JSON expected)"
        return 0
    }
    while IFS='=' read -r key value; do
        [ -z "$key" ] && continue
        export "${key}=${value}"
        log "Profile env: exported ${key}"
    done <<< "${pairs}"
}

# ============================================================
# Section 4: GitHub credential helper — OPTIONAL
# Mirrors userdata.go section 4
# ============================================================
setup_github_credentials() {
    local token_ssm="${KM_GITHUB_TOKEN_SSM:-}"
    if [ -z "$token_ssm" ]; then
        log "GitHub credentials: KM_GITHUB_TOKEN_SSM not set — skipping"
        return 0
    fi
    log "Installing GIT_ASKPASS credential helper from SSM: ${token_ssm} ..."
    local token
    token=$(aws ssm get-parameter \
        --name "${token_ssm}" \
        --with-decryption \
        --query "Parameter.Value" \
        --output text 2>/dev/null) || {
        log_warn "Failed to fetch GitHub token from SSM: ${token_ssm}"
        return 0
    }
    mkdir -p /opt/km/bin
    cat > /opt/km/bin/git-askpass.sh << ASKPASS
#!/bin/bash
case "\$1" in
  *Username*) echo "x-access-token" ;;
  *Password*) echo "${token}" ;;
  *)          echo "" ;;
esac
ASKPASS
    chmod +x /opt/km/bin/git-askpass.sh
    export GIT_ASKPASS=/opt/km/bin/git-askpass.sh
    log "GIT_ASKPASS credential helper installed"
}

# ============================================================
# Section 4b: Git ref enforcement — OPTIONAL
# Mirrors userdata.go section 4b
# ============================================================
setup_git_ref_enforcement() {
    local allowed_refs="${KM_GITHUB_ALLOWED_REFS:-}"
    if [ -z "$allowed_refs" ]; then
        log "Git ref enforcement: KM_GITHUB_ALLOWED_REFS not set — skipping"
        return 0
    fi
    log "Installing git ref enforcement pre-push hook..."
    mkdir -p /opt/km/bin
    cat > /opt/km/bin/km-pre-push-hook << 'PREPUSH'
#!/bin/bash
# km pre-push hook: block pushes to refs not in KM_GITHUB_ALLOWED_REFS
ALLOWED_REFS="${KM_GITHUB_ALLOWED_REFS:-}"
if [ -z "$ALLOWED_REFS" ]; then exit 0; fi
while read local_ref local_sha remote_ref remote_sha; do
    branch="${remote_ref#refs/heads/}"
    allowed=false
    IFS=',' read -ra PATTERNS <<< "$ALLOWED_REFS"
    for pattern in "${PATTERNS[@]}"; do
        if [[ "$branch" == $pattern ]]; then allowed=true; break; fi
    done
    if [ "$allowed" = false ]; then
        echo "[km] Push to '$branch' denied — not in allowedRefs: $ALLOWED_REFS" >&2
        exit 1
    fi
done
exit 0
PREPUSH
    chmod +x /opt/km/bin/km-pre-push-hook
    # Install into sandbox user's git template so it applies to all new/cloned repos
    local hooks_dir="/home/sandbox/.config/git/hooks"
    mkdir -p "${hooks_dir}"
    cp /opt/km/bin/km-pre-push-hook "${hooks_dir}/pre-push"
    chmod +x "${hooks_dir}/pre-push"
    chown -R sandbox:sandbox /home/sandbox/.config
    # Also set globally via git config --system
    git config --system core.hooksPath /opt/km/bin 2>/dev/null || true
    export KM_GITHUB_ALLOWED_REFS="${allowed_refs}"
    log "Git ref enforcement installed (allowedRefs: ${allowed_refs})"
}

# ============================================================
# Section 7.4: Rsync snapshot restore — OPTIONAL
# Mirrors userdata.go section 7.4
# ============================================================
restore_rsync_snapshot() {
    local snapshot="${KM_RSYNC_SNAPSHOT:-}"
    local bucket="${KM_ARTIFACTS_BUCKET:-}"
    local sandbox_id="${KM_SANDBOX_ID:-}"
    if [ -z "$snapshot" ]; then
        log "Rsync restore: KM_RSYNC_SNAPSHOT not set — skipping"
        return 0
    fi
    if [ -z "$bucket" ] || [ -z "$sandbox_id" ]; then
        log_warn "Rsync restore: KM_ARTIFACTS_BUCKET or KM_SANDBOX_ID not set — skipping"
        return 0
    fi
    log "Restoring rsync snapshot: ${snapshot} ..."
    aws s3 cp "s3://${bucket}/snapshots/${sandbox_id}/${snapshot}.tar.gz" /tmp/snapshot.tar.gz 2>/dev/null || {
        log_warn "Rsync snapshot '${snapshot}' not found in S3 — skipping"
        return 0
    }
    tar xzf /tmp/snapshot.tar.gz -C /workspace || {
        log_warn "Failed to extract rsync snapshot"
        rm -f /tmp/snapshot.tar.gz
        return 0
    }
    rm -f /tmp/snapshot.tar.gz
    chown -R sandbox:sandbox /workspace
    log "Rsync snapshot '${snapshot}' restored to /workspace"
}

# ============================================================
# Section 7.5: Init commands — OPTIONAL
# Mirrors userdata.go section 7.5 (initCommands, run as root before user drop)
# ============================================================
run_init_commands() {
    local init_cmds="${KM_INIT_COMMANDS:-}"
    if [ -z "$init_cmds" ]; then
        log "Init commands: KM_INIT_COMMANDS not set — skipping"
        return 0
    fi
    log "Running init commands..."
    local cmds
    cmds=$(echo "${init_cmds}" | base64 -d | jq -r '.[]' 2>/dev/null) || {
        log_warn "Failed to decode/parse KM_INIT_COMMANDS (base64 JSON array expected)"
        return 0
    }
    local i=0
    while IFS= read -r cmd; do
        [ -z "$cmd" ] && continue
        i=$((i + 1))
        log "initCommand[${i}]: ${cmd}"
        bash -c "${cmd}" || log_warn "initCommand[${i}] failed: ${cmd}"
    done <<< "${cmds}"
    log "Init commands complete (ran ${i})"
}

# ============================================================
# Section 5 (mail): Mail poller — OPTIONAL
# Mirrors km-mail-poller from userdata.go section 5 (sidecar binaries / mail poller)
# In containers: runs as background bash function (no systemd available).
# ============================================================
_mail_poller() {
    local bucket="${KM_ARTIFACTS_BUCKET:-}"
    local sandbox_id="${KM_SANDBOX_ID:-}"
    local email="${KM_EMAIL_ADDRESS:-}"
    local poll_interval="${KM_MAIL_POLL_INTERVAL:-60}"
    local mail_dir="/var/mail/km"

    if [ -z "$bucket" ]; then
        log_warn "mail poller: KM_ARTIFACTS_BUCKET not set, exiting"
        return 0
    fi

    local my_addr
    my_addr=$(echo "${sandbox_id}@" | tr '[:upper:]' '[:lower:]')
    mkdir -p "${mail_dir}/new" "${mail_dir}/processed"
    log "Mail poller started — polling s3://${bucket}/mail/ for ${my_addr} every ${poll_interval}s"

    while true; do
        aws s3 ls "s3://${bucket}/mail/" 2>/dev/null | awk '{print $NF}' | while read -r key; do
            [ -z "$key" ] && continue
            local local_file="${mail_dir}/new/${key}"
            if [ ! -f "${local_file}" ] && \
               [ ! -f "${mail_dir}/processed/${key}" ] && \
               [ ! -f "${mail_dir}/skipped/${key}" ]; then
                local tmp_file
                tmp_file=$(mktemp)
                if aws s3 cp "s3://${bucket}/mail/${key}" "${tmp_file}" 2>/dev/null; then
                    if head -c 8192 "${tmp_file}" | tr '[:upper:]' '[:lower:]' | grep -q "${my_addr}"; then
                        mv "${tmp_file}" "${local_file}"
                        log "New mail: ${key}"
                    else
                        rm -f "${tmp_file}"
                        mkdir -p "${mail_dir}/skipped"
                        touch "${mail_dir}/skipped/${key}"
                    fi
                else
                    rm -f "${tmp_file}"
                fi
            fi
        done
        sleep "${poll_interval}"
    done
}

start_mail_poller() {
    local email="${KM_EMAIL_ADDRESS:-}"
    if [ -z "$email" ]; then
        log "Mail poller: KM_EMAIL_ADDRESS not set — skipping"
        return 0
    fi
    log "Starting mail poller for ${email} ..."
    _mail_poller &
    log "Mail poller started (PID $!)"
}

# ============================================================
# Write km-upload-artifacts helper for sandbox user profile
# (called by /etc/profile.d/km-shutdown.sh inside the user session)
# ============================================================
_write_upload_helper() {
    cat > /opt/km/bin/km-upload-artifacts << 'UPLOAD'
#!/bin/bash
# km-upload-artifacts: sync /workspace to S3 artifacts bucket
# Called on SIGTERM by sandbox user profile shutdown hook.
BUCKET="${KM_ARTIFACTS_BUCKET:-}"
SANDBOX_ID="${KM_SANDBOX_ID:-${SANDBOX_ID:-}}"
if [ -z "$BUCKET" ] || [ -z "$SANDBOX_ID" ]; then
    echo "[km-upload] KM_ARTIFACTS_BUCKET or KM_SANDBOX_ID not set — skipping" >&2
    exit 0
fi
echo "[km-upload] Syncing /workspace to s3://${BUCKET}/artifacts/${SANDBOX_ID}/ ..."
aws s3 sync /workspace "s3://${BUCKET}/artifacts/${SANDBOX_ID}/" --exclude ".git/*"
echo "[km-upload] Done"
UPLOAD
    chmod +x /opt/km/bin/km-upload-artifacts
}

# ============================================================
# MAIN EXECUTION
# ============================================================
log "=== km-sandbox entrypoint starting ==="
log "sandbox_id=${KM_SANDBOX_ID:-<unset>}"

# Critical sections — abort on failure
setup_ca_trust
inject_secrets

# Optional sections — warn and continue
inject_otp_secrets
setup_profile_env
setup_github_credentials
setup_git_ref_enforcement
restore_rsync_snapshot
run_init_commands
start_mail_poller

# Write upload helper for sandbox user profile
_write_upload_helper

# Install shutdown hook for sandbox user's interactive bash session.
# After exec gosu, the entrypoint trap is replaced, so we use profile.d.
cat > /etc/profile.d/km-shutdown.sh << 'SHUTDOWN_HOOK'
_km_shutdown() { /opt/km/bin/km-upload-artifacts 2>/dev/null || true; }
trap '_km_shutdown' TERM INT EXIT
SHUTDOWN_HOOK
chmod 644 /etc/profile.d/km-shutdown.sh

# Write all KM_* and related env vars to profile.d so the sandbox user inherits them
# in interactive shells (gosu replaces the process but /etc/profile.d/ is sourced by bash).
{
    env | grep -E '^(KM_|SSL_|REQUESTS_|CURL_|NODE_EXTRA_|GIT_|AWS_|SANDBOX_|HTTP_PROXY|HTTPS_PROXY|NO_PROXY|OTEL_|CLAUDE_)' \
        | sed 's/^/export /' || true
} > /etc/profile.d/km-env.sh 2>/dev/null
chmod 644 /etc/profile.d/km-env.sh 2>/dev/null || true

log "Dropping to sandbox user..."
exec gosu sandbox "${@:-/bin/bash}"
