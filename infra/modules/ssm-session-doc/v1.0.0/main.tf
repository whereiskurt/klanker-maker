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
# shellProfile.linux is parameterized — the conditional one-liner runs `exec
# bash -l` for the empty-command case (km shell non-root) and `bash -lc
# "{{ command }}"` for non-empty commands (km agent --claude / attach / run
# --interactive). Both paths source /etc/profile.d/ via login shell semantics.
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
        linux = "[ -z \"{{ command }}\" ] && exec bash -l || bash -lc \"{{ command }}\""
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
