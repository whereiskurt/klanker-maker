# KM-Sandbox-Session SSM document — v1.0.0.
#
# Standard_Stream sessionType (vs InteractiveCommands) so Ctrl+C in `aws ssm
# start-session` is forwarded as a PTY byte to the remote foreground process
# instead of tearing down the session. Mirrors SSH signal-handling.
#
# runAsDefaultUser = sandbox replaces the `sudo -u sandbox -i` wrapper that the
# CLI used with AWS-StartInteractiveCommand. The SSM agent performs the user
# switch before spawning the shell, so no sudo is needed.
#
# shellProfile.linux delegates to /usr/local/bin/km-session-entry on the
# sandbox (provisioned by userdata). The wrapper handles the empty-vs-non-empty
# command branching: empty → interactive bash login shell (km shell), non-empty
# → bash login -c "<command>" (km agent paths). Keeping the conditional out of
# the SSM doc avoids two cosmetic/functional issues that the inline form had:
#   1. SSM agent "types" shellProfile.linux content into the PTY, so a verbose
#      conditional shows up echoed twice on session start.
#   2. After the inline conditional's command exits, the SSM agent fell back to
#      a residual interactive sh prompt instead of closing the session cleanly.
# Routing through km-session-entry → km-sandbox-shell also keeps the same
# cgroup-placement code path firing for SSM and direct logins.
resource "aws_ssm_document" "km_sandbox_session" {
  name            = var.document_name
  document_type   = "Session"
  document_format = "JSON"

  content = jsonencode({
    schemaVersion = "1.0"
    description   = "KM sandbox session: Standard_Stream PTY as sandbox user"
    sessionType   = "Standard_Stream"
    parameters = {
      command = {
        type        = "String"
        description = "Command to run inside the bash login shell. Empty = interactive shell."
        default     = ""
      }
    }
    inputs = {
      runAsEnabled       = true
      runAsDefaultUser   = "sandbox"
      idleSessionTimeout = "20"
      shellProfile = {
        linux = "exec /usr/local/bin/km-session-entry \"{{ command }}\""
      }
    }
  })

  tags = merge(var.tags, {
    Module  = "ssm-session-doc"
    Version = "v1.0.0"
  })

  # Schema "1.0" Session docs do NOT support in-place UpdateDocument — Terraform
  # destroys and recreates on content change. create_before_destroy keeps the
  # name continuously available.
  lifecycle {
    create_before_destroy = true
  }
}
