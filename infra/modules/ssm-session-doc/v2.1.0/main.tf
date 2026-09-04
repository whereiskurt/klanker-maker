# {prefix}-Sandbox-Session SSM document — v2.1.0.
# Per-install document naming via ${var.resource_prefix}-Sandbox-Session,
# preserving the v1.0.0 "Sandbox-Session" suffix and Standard_Stream sessionType.
#
# v2.0.0 -> v2.1.0: idleSessionTimeout raised 20 -> 60 minutes. Only `name` and
# `document_type` are ForceNew on aws_ssm_document, so a content-only change is
# an in-place UpdateDocument that publishes a new document version. There is no
# destroy/create gap here (unlike the v1.0.0 -> v2.0.0 rename below) and active
# sessions are unaffected — a session carries the preferences it started with,
# so the longer timeout applies from the next `km shell` onward.
#
# Migration from v1.0.0: a moved {} block declares the rename, but AWS SSM
# does not support document rename — terraform's AWS provider falls back to
# destroy + create. Active SSM sessions (started before the rename) keep
# working; new sessions started during the ~2-second destroy/create gap fail
# with InvalidDocument and the operator retries. Retained here so an install
# still on v1.0.0 can adopt v2.1.0 directly.

resource "aws_ssm_document" "sandbox_session" {
  name            = "${var.resource_prefix}-Sandbox-Session"
  document_type   = "Session"
  document_format = "JSON"

  content = jsonencode({
    schemaVersion = "1.0"
    description   = "${var.resource_prefix} sandbox session: Standard_Stream PTY as sandbox user"
    sessionType   = "Standard_Stream"
    parameters = {
      command = {
        type        = "String"
        description = "Command to run inside the bash login shell. Empty = interactive shell."
        default     = ""
      }
    }
    inputs = {
      runAsEnabled     = true
      runAsDefaultUser = "sandbox"
      # Minutes of no terminal I/O before SSM tears the session down
      # server-side. 60 is the maximum AWS accepts (valid range 1-60).
      # "Idle" counts terminal bytes, not wall clock: a silently-thinking agent
      # turn that prints nothing still ages toward this limit. There is
      # deliberately no maxSessionDuration, so an active session never expires.
      idleSessionTimeout = "60"
      shellProfile = {
        linux = "exec /usr/local/bin/km-session-entry \"{{ command }}\""
      }
    }
  })

  tags = merge(var.tags, {
    Module               = "ssm-session-doc"
    Version              = "v2.1.0"
    "km:resource-prefix" = var.resource_prefix
  })

  lifecycle {
    create_before_destroy = true
  }
}

# Phase 84.4.1: declares state-rename from v1.0.0's resource address.
# Terraform's AWS provider falls back to destroy/create because the AWS SSM
# API does not support document rename; the moved {} block is preserved for
# documentation + future provider-level rename support.
moved {
  from = aws_ssm_document.km_sandbox_session
  to   = aws_ssm_document.sandbox_session
}
