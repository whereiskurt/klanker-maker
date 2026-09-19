# Shared per-install inbound FIFO DLQs — v1.2.0.
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
#
# v1.2.0 (2026-09-19) adds the FOURTH: the HackerOne inbound DLQ. v1.1.0's
# comment recorded that pkg/aws.H1InboundDLQName / DLQArn named a queue nothing
# created and scoped the fix out of Phase 127, predicting a latent gap. It was
# not latent. SQS validates a RedrivePolicy's deadLetterTargetArn at CreateQueue
# time, not at redrive time, so the first `km create` of any profile with
# notification.h1.inbound.enabled: true failed hard:
#
#   create h1 queue sec-h1-inbound-always-c3a035ea.fifo: ... InvalidParameterValue:
#   Value {"deadLetterTargetArn":"arn:aws:sqs:...:sec-h1-inbound-dlq.fifo",
#   "maxReceiveCount":3} for parameter RedrivePolicy is invalid.
#   Reason: Dead letter target does not exist.
#
# — and it fails AFTER the EC2 apply, leaving a running instance, three volumes,
# an SG and an instance profile on a row marked `failed`, billing until someone
# runs `km destroy`. H1 inbound had been completely non-functional since Phase
# 103 (whose UAT was never run); no shipped profile set the flag, so nobody had
# reached CreateH1InboundQueue. pkg/terragrunt's
# TestSqsInboundDLQModule_EveryDLQNameHelperHasAQueueResource now pairs every
# *InboundDLQName helper with a resource in the pinned module.
#
# FIFO: both DLQs are FIFO (`fifo_queue = true`) — a FIFO source queue's
# redrivePolicy MUST target a FIFO DLQ (AWS constraint). content_based_dedup is
# false: redrive-moved messages carry their original MessageGroupId /
# MessageDeduplicationId from the source queue; the DLQ does not synthesize them.
#
# message_retention_seconds = 1209600 (14 days, the SQS max) so an operator has a
# full two weeks to inspect / redrive poison messages before they age out.
#
# Naming: {label}-<kind>-inbound-dlq.fifo for kind in github/slack/webhook/h1.
# These match the km-operator-policy `{prefix}-<kind>-inbound-*.fifo` IAM
# wildcards (RESEARCH Pitfall 6) — no new IAM grant required. The name is a
# CONTRACT with pkg/aws.<Kind>InboundDLQName: the Go side derives the redrive
# ARN from that exact name and never reads this module's outputs, so a typo in
# the live unit's input fails at the next `km create`, not at apply.
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

resource "aws_sqs_queue" "h1_inbound_dlq" {
  name                        = var.h1_dlq_name
  fifo_queue                  = true
  content_based_deduplication = false
  message_retention_seconds   = 1209600

  tags = merge(var.tags, {
    Name      = var.h1_dlq_name
    Component = "km-h1-inbound"
  })
}
