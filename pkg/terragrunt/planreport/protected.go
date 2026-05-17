// Package planreport parses terraform show -json output and runs a curated
// destroy-class safety gate over the resource_changes array. The gate, the
// protected-types list, and the parser are all pure logic — no terragrunt,
// AWS, or cmd dependencies.
package planreport

// ProtectedTypes is the compiled-in allowlist of resource types that trip the
// destroy-class gate when a plan would destroy or replace them.
//
// Adding to this list requires PR review — by design, there is no
// operator-side config file (CONTEXT.md decision 6). Each entry MUST
// reference the incident report (UAT log / postmortem) that motivated it.
var ProtectedTypes = []string{
	// Phase 84 Gap 3 — destroyed during 82.x→84 cutover, inbound email broken
	"aws_ses_domain_identity",
	// Phase 84 Gap 3 — DKIM keys lost with the identity
	"aws_ses_domain_dkim",
	// Phase 84 Gap 6 — active pointer nulled, inbound stopped
	"aws_ses_active_receipt_rule_set",
	// Shared rule set — prevent_destroy in code, but plan-time check catches earlier
	"aws_ses_receipt_rule_set",
	// Phase 82->84 incident — receipt rule children (operator inbound + sandbox catchall)
	// destroyed via removed{destroy=true} orphan path; the parent rule_set alone is insufficient
	"aws_ses_receipt_rule",
	// MX, DKIM CNAMEs, verification TXT — recovery required manual re-creation
	"aws_route53_record",
	// Mailbox + artifacts — data loss
	"aws_s3_bucket",
	// Detaching SES write policy silently breaks inbound
	"aws_s3_bucket_policy",
	// Sandbox metadata, lock table — destroy = irrecoverable
	"aws_dynamodb_table",
	// Schedule-deleted with 7-30d window, hard to undo cleanly
	"aws_kms_key",
}
