# Shared per-install inbound FIFO DLQs — v1.0.0.
#
# Phase 99.1 (GH-DLQ-SHARED): two shared dead-letter queues, one per inbound
# source, created ONCE per install at `km init` (idempotent via Terraform state —
# RESEARCH Finding 4 Option A). Per-sandbox inbound FIFO queues
# (km-{github,slack}-inbound-<sandbox-id>.fifo) attach a redrivePolicy that
# targets these DLQs so a poison message that exhausts maxReceiveCount is moved
# off the source queue instead of head-of-line-blocking its message group forever
# (the FIFO poison-message wedge found in Phase 99 UAT).
#
# FIFO: both DLQs are FIFO (`fifo_queue = true`) — a FIFO source queue's
# redrivePolicy MUST target a FIFO DLQ (AWS constraint). content_based_dedup is
# false: redrive-moved messages carry their original MessageGroupId /
# MessageDeduplicationId from the source queue; the DLQ does not synthesize them.
#
# message_retention_seconds = 1209600 (14 days, the SQS max) so an operator has a
# full two weeks to inspect / redrive poison messages before they age out.
#
# Naming: {label}-github-inbound-dlq.fifo / {label}-slack-inbound-dlq.fifo. These
# match the existing km-operator-policy `{prefix}-{github,slack}-inbound-*.fifo`
# IAM wildcards (RESEARCH Pitfall 6) — no new IAM grant required.
#
# NOTE: this module declares NO provider requirements block of its own — root.hcl's
# generate "provider" stanza is the single source (memory project_terragrunt_providers_in_root).

resource "aws_sqs_queue" "github_inbound_dlq" {
  name                        = var.github_dlq_name
  fifo_queue                  = true
  content_based_deduplication = false
  message_retention_seconds   = 1209600

  tags = merge(var.tags, {
    Name      = var.github_dlq_name
    Component = "km-github-inbound"
  })
}

resource "aws_sqs_queue" "slack_inbound_dlq" {
  name                        = var.slack_dlq_name
  fifo_queue                  = true
  content_based_deduplication = false
  message_retention_seconds   = 1209600

  tags = merge(var.tags, {
    Name      = var.slack_dlq_name
    Component = "km-slack-inbound"
  })
}
