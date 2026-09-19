variable "github_dlq_name" {
  description = "Name of the shared GitHub inbound FIFO DLQ (must end in .fifo, e.g. km-github-inbound-dlq.fifo)."
  type        = string
  default     = "km-github-inbound-dlq.fifo"
}

variable "slack_dlq_name" {
  description = "Name of the shared Slack inbound FIFO DLQ (must end in .fifo, e.g. km-slack-inbound-dlq.fifo)."
  type        = string
  default     = "km-slack-inbound-dlq.fifo"
}

variable "webhook_dlq_name" {
  description = "Name of the shared generic-webhook inbound FIFO DLQ (must end in .fifo, e.g. km-webhook-inbound-dlq.fifo). Phase 127."
  type        = string
  default     = "km-webhook-inbound-dlq.fifo"
}

variable "h1_dlq_name" {
  description = "Name of the shared HackerOne inbound FIFO DLQ (must end in .fifo, e.g. km-h1-inbound-dlq.fifo). MUST equal pkg/aws.H1InboundDLQName(prefix) = {prefix}-h1-inbound-dlq.fifo: create_h1_inbound.go derives the RedrivePolicy ARN from that name and never reads this module's output. v1.2.0."
  type        = string
  default     = "km-h1-inbound-dlq.fifo"
}

variable "tags" {
  description = "Resource tags to merge onto the DLQ queues."
  type        = map(string)
  default     = {}
}
