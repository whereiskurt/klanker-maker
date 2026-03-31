#!/bin/bash
# km-cred-refresh — writes AWS credential files for sandbox and sidecar containers.
# Runs as a sidecar container using the km-sandbox image with this script as the command.
#
# This container is the ONLY one with operator AWS credentials (AWS_ACCESS_KEY_ID etc).
# It writes credential files to the shared cred-vol volume so other containers can
# use AWS_SHARED_CREDENTIALS_FILE without holding credentials directly.
#
# Env vars (set by docker-compose.yml):
#   AWS_ACCESS_KEY_ID       - operator's STS temporary key
#   AWS_SECRET_ACCESS_KEY   - operator's STS temporary secret
#   AWS_SESSION_TOKEN       - operator's STS session token
#   AWS_DEFAULT_REGION      - region
#   KM_SANDBOX_ROLE_ARN     - role ARN for sandbox container
#   KM_SIDECAR_ROLE_ARN     - role ARN for sidecar containers
#   KM_SANDBOX_ID           - sandbox identifier

set -euo pipefail

CREDS_DIR="/creds"
REFRESH_INTERVAL=2700  # 45 minutes (STS creds last 1h)

log() { echo "[km-cred-refresh] $*"; }

write_credentials() {
    local key="${AWS_ACCESS_KEY_ID:-}"
    local secret="${AWS_SECRET_ACCESS_KEY:-}"
    local token="${AWS_SESSION_TOKEN:-}"
    local region="${AWS_DEFAULT_REGION:-us-east-1}"

    if [ -z "$key" ] || [ -z "$secret" ]; then
        log "WARNING: No AWS credentials available — writing empty credential files"
        # Write minimal files so containers don't crash on missing file
        printf '[sandbox]\nregion = %s\n' "$region" > "${CREDS_DIR}/sandbox"
        printf '[sidecar]\nregion = %s\n' "$region" > "${CREDS_DIR}/sidecar"
        chmod 644 "${CREDS_DIR}/sandbox" "${CREDS_DIR}/sidecar"
        return 0
    fi

    # Try to assume scoped roles for isolation
    local sandbox_key="$key" sandbox_secret="$secret" sandbox_token="$token"
    local sidecar_key="$key" sidecar_secret="$secret" sidecar_token="$token"

    if [ -n "${KM_SANDBOX_ROLE_ARN:-}" ]; then
        log "Assuming sandbox role: ${KM_SANDBOX_ROLE_ARN}"
        local assume_out
        if assume_out=$(aws sts assume-role \
            --role-arn "${KM_SANDBOX_ROLE_ARN}" \
            --role-session-name "km-sandbox-${KM_SANDBOX_ID:-refresh}" \
            --duration-seconds 3600 \
            --output json 2>&1); then
            sandbox_key=$(echo "$assume_out" | jq -r '.Credentials.AccessKeyId')
            sandbox_secret=$(echo "$assume_out" | jq -r '.Credentials.SecretAccessKey')
            sandbox_token=$(echo "$assume_out" | jq -r '.Credentials.SessionToken')
            log "Sandbox role assumed successfully"
        else
            log "WARNING: Failed to assume sandbox role, using operator creds: $assume_out"
        fi
    fi

    if [ -n "${KM_SIDECAR_ROLE_ARN:-}" ]; then
        log "Assuming sidecar role: ${KM_SIDECAR_ROLE_ARN}"
        local assume_out
        if assume_out=$(aws sts assume-role \
            --role-arn "${KM_SIDECAR_ROLE_ARN}" \
            --role-session-name "km-sidecar-${KM_SANDBOX_ID:-refresh}" \
            --duration-seconds 3600 \
            --output json 2>&1); then
            sidecar_key=$(echo "$assume_out" | jq -r '.Credentials.AccessKeyId')
            sidecar_secret=$(echo "$assume_out" | jq -r '.Credentials.SecretAccessKey')
            sidecar_token=$(echo "$assume_out" | jq -r '.Credentials.SessionToken')
            log "Sidecar role assumed successfully"
        else
            log "WARNING: Failed to assume sidecar role, using operator creds: $assume_out"
        fi
    fi

    # Write sandbox credentials file
    cat > "${CREDS_DIR}/sandbox" <<CRED
[sandbox]
aws_access_key_id = ${sandbox_key}
aws_secret_access_key = ${sandbox_secret}
aws_session_token = ${sandbox_token}
region = ${region}
CRED

    # Write sidecar credentials file
    cat > "${CREDS_DIR}/sidecar" <<CRED
[sidecar]
aws_access_key_id = ${sidecar_key}
aws_secret_access_key = ${sidecar_secret}
aws_session_token = ${sidecar_token}
region = ${region}
CRED

    chmod 644 "${CREDS_DIR}/sandbox" "${CREDS_DIR}/sidecar"
    log "Credential files written to ${CREDS_DIR}/"
}

# Initial write
log "Starting credential refresh loop (interval: ${REFRESH_INTERVAL}s)"
write_credentials

# Refresh loop
while true; do
    sleep "${REFRESH_INTERVAL}"
    log "Refreshing credentials..."
    write_credentials
done
