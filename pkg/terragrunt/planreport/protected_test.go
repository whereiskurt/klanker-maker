package planreport

import (
	"slices"
	"testing"
)

func TestProtectedTypes_Contents(t *testing.T) {
	expected := []string{
		"aws_ses_domain_identity",
		"aws_ses_domain_dkim",
		"aws_ses_active_receipt_rule_set",
		"aws_ses_receipt_rule_set",
		"aws_route53_record",
		"aws_s3_bucket",
		"aws_s3_bucket_policy",
		"aws_dynamodb_table",
		"aws_kms_key",
	}
	for _, e := range expected {
		if !slices.Contains(ProtectedTypes, e) {
			t.Errorf("ProtectedTypes missing expected entry %q", e)
		}
	}
}

func TestProtectedTypes_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, pt := range ProtectedTypes {
		if seen[pt] {
			t.Errorf("ProtectedTypes contains duplicate entry %q", pt)
		}
		seen[pt] = true
	}
}
