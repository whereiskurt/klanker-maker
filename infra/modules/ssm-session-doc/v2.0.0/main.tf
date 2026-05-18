# {prefix}-Sandbox-Session SSM document — v2.0.0 (Phase 84.4.1).
# Per-install document naming via ${var.resource_prefix}-Sandbox-Session,
# preserving the v1.0.0 "Sandbox-Session" suffix and Standard_Stream sessionType.
#
# Migration from v1.0.0: a moved {} block declares the rename, but AWS SSM
# does not support document rename — terraform's AWS provider falls back to
# destroy + create. Active SSM sessions (started before the rename) keep
# working; new sessions started during the ~2-second destroy/create gap fail
# with InvalidDocument and the operator retries.

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
      runAsEnabled       = true
      runAsDefaultUser   = "sandbox"
      idleSessionTimeout = "20"
      shellProfile = {
        linux = "exec /usr/local/bin/km-session-entry \"{{ command }}\""
      }
    }
  })

  tags = merge(var.tags, {
    Module               = "ssm-session-doc"
    Version              = "v2.0.0"
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
