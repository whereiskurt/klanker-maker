# Shared per-install inbound FIFO DLQs — v1.1.0.
#
# Phase 99.1 (GH-DLQ-SHARED): two shared dead-letter queues, one per inbound
# source, created ONCE per install at `km init` (idempotent via Terraform state —
# RESEARCH Finding 4 Option A). Per-sandbox inbound FIFO queues
# (km-{github,slack}-inbound-<sandbox-id>.fifo) attach a redrivePolicy that
# targets these DLQs so a poison message that exhausts maxReceiveCount is moved
# off the source queue instead of head-of-line-blocking its message group forever
# (the FIFO poison-message wedge found in Phase 99 UAT).
#
# v1.1.0 (Phase 127) adds a THIRD shared DLQ for the generic webhook ingress
# bridge (km-webhook-bridge / cmd/km-webhook-bridge). This module is the correct
# place for it: it already provisions the shared github/slack DLQs, and
# pkg/aws.WebhookInboundDLQName / internal/app/cmd/create_webhook_inbound.go
# already compute a deterministic ARN for a queue named
# {prefix}-webhook-inbound-dlq.fifo — nothing previously created that queue.
# NOTE: the H1 bridge has this exact gap (H1InboundDLQName / DLQArn are
# computed, but no resource here or anywhere else creates the queue they name)
# — a genuine pre-existing bug in this module, left AS-IS. Do not "fix" it by
# adding an h1_dlq_name here; that is out of scope for Phase 127 and would want
# its own investigation of why H1 never got one.
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

resource "aws_sqs_queue" "webhook_inbound_dlq" {
  name                        = var.webhook_dlq_name
  fifo_queue                  = true
  content_based_deduplication = false
  message_retention_seconds   = 1209600

  tags = merge(var.tags, {
    Name      = var.webhook_dlq_name
    Component = "km-webhook-inbound"
  })
}
