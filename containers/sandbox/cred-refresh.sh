#!/bin/bash
# km-cred-refresh — writes AWS credential files for sandbox and sidecar containers.
# Runs as a sidecar container using the km-sandbox image with this script as the command.
#
# This container mounts the host's ~/.aws directory (read-only) so it can use the
# operator's SSO session to continuously assume scoped roles. No long-lived IAM keys needed.
#
# Env vars (set by docker-compose.yml):
#   AWS_PROFILE             - operator's AWS profile (e.g. klanker-terraform)
#   AWS_DEFAULT_REGION      - region
#   KM_SANDBOX_ROLE_ARN     - role ARN for sandbox container
#   KM_SIDECAR_ROLE_ARN     - role ARN for sidecar containers
#   KM_SANDBOX_ID           - sandbox identifier

set -euo pipefail

CREDS_DIR="/creds"
REFRESH_INTERVAL=2700  # 45 minutes (STS assumed-role creds last 1h)

log() { echo "[km-cred-refresh] $*"; }

write_credentials() {
    local region="${AWS_DEFAULT_REGION:-us-east-1}"
    local profile="${AWS_PROFILE:-}"

    # Verify the host AWS session is valid.
    if ! aws sts get-caller-identity --profile "$profile" >/dev/null 2>&1; then
        log "WARNING: Host AWS session not valid (profile=$profile) — writing empty credential files"
        printf '[sandbox]\nregion = %s\n' "$region" > "${CREDS_DIR}/sandbox"
        printf '[sidecar]\nregion = %s\n' "$region" > "${CREDS_DIR}/sidecar"
        chmod 644 "${CREDS_DIR}/sandbox" "${CREDS_DIR}/sidecar"
        return 0
    fi

    log "Host AWS session valid (profile=$profile)"

    # Assume scoped roles using the host's SSO session.
    local sandbox_creds="" sidecar_creds=""

    if [ -n "${KM_SANDBOX_ROLE_ARN:-}" ]; then
        log "Assuming sandbox role: ${KM_SANDBOX_ROLE_ARN}"
        if sandbox_creds=$(aws sts assume-role \
            --profile "$profile" \
            --role-arn "${KM_SANDBOX_ROLE_ARN}" \
            --role-session-name "km-sandbox-${KM_SANDBOX_ID:-refresh}" \
            --duration-seconds 3600 \
            --output json 2>&1); then
            log "Sandbox role assumed successfully"
        else
            log "WARNING: Failed to assume sandbox role: $sandbox_creds"
            sandbox_creds=""
        fi
    fi

    if [ -n "${KM_SIDECAR_ROLE_ARN:-}" ]; then
        log "Assuming sidecar role: ${KM_SIDECAR_ROLE_ARN}"
        if sidecar_creds=$(aws sts assume-role \
            --profile "$profile" \
            --role-arn "${KM_SIDECAR_ROLE_ARN}" \
            --role-session-name "km-sidecar-${KM_SANDBOX_ID:-refresh}" \
            --duration-seconds 3600 \
            --output json 2>&1); then
            log "Sidecar role assumed successfully"
        else
            log "WARNING: Failed to assume sidecar role: $sidecar_creds"
            sidecar_creds=""
        fi
    fi

    # Write sandbox credentials file.
    if [ -n "$sandbox_creds" ]; then
        local s_key=$(echo "$sandbox_creds" | jq -r '.Credentials.AccessKeyId')
        local s_secret=$(echo "$sandbox_creds" | jq -r '.Credentials.SecretAccessKey')
        local s_token=$(echo "$sandbox_creds" | jq -r '.Credentials.SessionToken')
        cat > "${CREDS_DIR}/sandbox" <<CRED
[sandbox]
aws_access_key_id = ${s_key}
aws_secret_access_key = ${s_secret}
aws_session_token = ${s_token}
region = ${region}
CRED
    else
        # Fallback: export operator credentials from SSO session.
        local op_creds
        if op_creds=$(aws configure export-credentials --profile "$profile" --format process 2>&1); then
            local op_key=$(echo "$op_creds" | jq -r '.AccessKeyId')
            local op_secret=$(echo "$op_creds" | jq -r '.SecretAccessKey')
            local op_token=$(echo "$op_creds" | jq -r '.SessionToken')
            cat > "${CREDS_DIR}/sandbox" <<CRED
[sandbox]
aws_access_key_id = ${op_key}
aws_secret_access_key = ${op_secret}
aws_session_token = ${op_token}
region = ${region}
CRED
            log "Wrote sandbox creds from operator SSO session (no scoped role)"
        else
            log "WARNING: Failed to export operator credentials: $op_creds"
            printf '[sandbox]\nregion = %s\n' "$region" > "${CREDS_DIR}/sandbox"
        fi
    fi

    # Write sidecar credentials file (same pattern).
    if [ -n "$sidecar_creds" ]; then
        local k_key=$(echo "$sidecar_creds" | jq -r '.Credentials.AccessKeyId')
        local k_secret=$(echo "$sidecar_creds" | jq -r '.Credentials.SecretAccessKey')
        local k_token=$(echo "$sidecar_creds" | jq -r '.Credentials.SessionToken')
        cat > "${CREDS_DIR}/sidecar" <<CRED
[sidecar]
aws_access_key_id = ${k_key}
aws_secret_access_key = ${k_secret}
aws_session_token = ${k_token}
region = ${region}
CRED
    else
        # Copy sandbox creds for sidecar too (same operator credentials).
        sed 's/\[sandbox\]/[sidecar]/' "${CREDS_DIR}/sandbox" > "${CREDS_DIR}/sidecar"
        log "Wrote sidecar creds from sandbox fallback"
    fi

    chmod 644 "${CREDS_DIR}/sandbox" "${CREDS_DIR}/sidecar"
    log "Credential files written to ${CREDS_DIR}/"
}

# Initial write
log "Starting credential refresh loop (interval: ${REFRESH_INTERVAL}s)"
write_credentials

# Refresh loop — credentials stay fresh as long as host SSO session is valid
while true; do
    sleep "${REFRESH_INTERVAL}"
    log "Refreshing credentials..."
    write_credentials
done
