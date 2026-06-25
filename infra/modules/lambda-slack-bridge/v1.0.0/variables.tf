variable "lambda_zip_path" {
  description = "Path to the compiled km-slack-bridge Lambda zip (linux/arm64 bootstrap binary)"
  type        = string
}

variable "bot_token_path" {
  description = "SSM parameter path for the Slack bot token (SecureString)"
  type        = string
  default     = "/km/slack/bot-token"
}

variable "identities_table_name" {
  description = "Name of the DynamoDB km-identities table for Ed25519 public key lookup"
  type        = string
  default     = "km-identities"
}

variable "identities_table_arn" {
  description = "ARN of the DynamoDB km-identities table (used in IAM policy)"
  type        = string
}

variable "sandboxes_table_name" {
  description = "Name of the DynamoDB km-sandboxes table for channel ownership lookup"
  type        = string
  default     = "km-sandboxes"
}

variable "sandboxes_table_arn" {
  description = "ARN of the DynamoDB km-sandboxes table (used in IAM policy)"
  type        = string
}

variable "nonces_table_name" {
  description = "Name of the DynamoDB km-slack-bridge-nonces table"
  type        = string
  default     = "km-slack-bridge-nonces"
}

variable "nonces_table_arn" {
  description = "ARN of the DynamoDB km-slack-bridge-nonces table (used in IAM policy)"
  type        = string
}

variable "kms_key_arn" {
  description = "ARN (or alias ARN) of the platform KMS key for decrypting SSM SecureString parameters. Empty string falls back to a broad account-scoped key resource."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources created by this module"
  type        = map(string)
  default     = {}
}

# ============================================================
# Phase 67-05 additions — inbound events path
# ============================================================

variable "resource_prefix" {
  description = "Resource prefix used for per-sandbox SQS queue names ({resource_prefix}-slack-inbound-{sandbox_id}.fifo)"
  type        = string
  default     = "km"
}

variable "slack_threads_table_name" {
  description = "Name of the DynamoDB km-slack-threads table for Slack thread tracking"
  type        = string
  default     = "km-slack-threads"
}

variable "signing_secret_path" {
  description = "SSM parameter path for the Slack signing secret (SecureString used for HMAC verification)"
  type        = string
  default     = "/km/slack/signing-secret"
}

# ============================================================
# Phase 67.1 — ACK reaction emoji (👀 by default)
# ============================================================

variable "slack_ack_emoji" {
  description = "Emoji NAME (without colons, e.g. \"eyes\" not \":eyes:\") used for the ACK reaction the bridge posts after a successful SQS enqueue of an inbound Slack message. Defaults to \"eyes\" (👀). Override to use a different emoji workspace-wide. Bot must have reactions:write scope."
  type        = string
  default     = "eyes"
}

# ============================================================
# Phase 68 — Slack transcript streaming
# ============================================================

variable "artifacts_bucket" {
  description = "Project-wide S3 artifacts bucket. The bridge reads transcript objects under transcripts/* (Phase 68). When empty, the bridge S3 read policy is omitted."
  type        = string
  default     = ""
}

# ============================================================
# Phase 91 — polite-bot @-mention-only mode
# ============================================================

# Phase 91: polite-bot mode toggle. When "true", the bridge events handler
# only processes inbound Slack messages whose text contains <@{bot_user_id}>.
# Default "false" → pre-Phase-91 every-message behaviour (full back-compat).
variable "slack_mention_only" {
  description = "When 'true', the bridge only processes messages that @-mention the bot (Phase 91 polite-bot). Default 'false' = every-message dispatch."
  type        = string
  default     = "false"
}

# Phase 91: pre-warmed bot user ID for the mention scan. Sourced from the SSM
# parameter {prefix}slack/bot-user-id (written by km slack init / rotate-token).
# Empty default → bridge falls back to a live auth.test call on the first
# mention scan via the existing CachedBotUserIDFetcher.
variable "slack_bot_user_id" {
  description = "Slack bot user ID (e.g. UBOT123) for @-mention detection (Phase 91). Empty → bridge fetches lazily via auth.test."
  type        = string
  default     = ""
}

# Phase 91.4: first-only-react toggle. When "false", the bridge posts the 👀
# reaction ONLY on top-level engagement messages — thread replies that reach
# the dispatcher dispatch silently. Default "true" → pre-Phase-91.4
# chatty-reactor behaviour (full back-compat).
variable "slack_react_always" {
  description = "When 'true' (default), bridge posts 👀 on every dispatched message. When 'false', reacts only on top-level engagement messages (Phase 91.4)."
  type        = string
  default     = "true"
}

# Phase 95: federated relay peer URLs (comma-joined). Empty => federation off.
variable "slack_peer_bridges" {
  description = "Comma-joined /events URLs of sibling km installs for federated relay (Phase 95). Empty string = federation off."
  type        = string
  default     = ""
}

# Phase 96: front-door router toggle. When "true", the bridge posts a threaded reply
# in orphan channels (bot-mention + front-door miss + zero peer claims). Default "false"
# = dormant (Phase 95 byte-identical). Meaningful only on the front-door install.
variable "slack_default_router" {
  description = "When 'true', the front-door bridge posts a helpful threaded reply in orphan channels after a scatter-gather finds no owner (Phase 96). Default 'false' = dormant."
  type        = string
  default     = "false"
}

# Phase 118: install-level Slack trigger allowlist (comma-joined Uxxxx). When non-empty,
# only listed users may trigger an agent turn; the bridge reads this into EventsHandler.Allow
# at cold-start. Empty = everyone allowed (byte-identical to pre-118). A non-empty per-sandbox
# allow (slack_allow attr on the km-sandboxes row) replaces this for that sandbox.
variable "slack_allow" {
  description = "Phase 118: install-level Slack trigger allowlist (comma-joined Uxxxx). Empty = everyone allowed."
  type        = string
  default     = ""
}
