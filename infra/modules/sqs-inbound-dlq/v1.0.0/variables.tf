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

variable "tags" {
  description = "Resource tags to merge onto the DLQ queues."
  type        = map(string)
  default     = {}
}
