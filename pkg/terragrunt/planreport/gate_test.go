package planreport

import (
	"testing"
)

func TestEvaluate_EmptyReports(t *testing.T) {
	result := Evaluate([]Report{}, false)
	if result.Blocked {
		t.Errorf("expected Blocked=false for empty reports")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected empty Trips, got %d", len(result.Trips))
	}
}

func TestEvaluate_AllClean(t *testing.T) {
	r := Report{
		Module: "network",
		Adds: []ResourceChange{
			{Address: "aws_vpc.main", Type: "aws_vpc", Action: "create"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if result.Blocked {
		t.Errorf("expected Blocked=false for adds-only report")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected empty Trips, got %d", len(result.Trips))
	}
}

func TestEvaluate_ProtectedDestroy(t *testing.T) {
	r := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_domain_identity.sandboxes", Type: "aws_ses_domain_identity", Action: "delete"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if !result.Blocked {
		t.Errorf("expected Blocked=true for protected destroy")
	}
	if len(result.Trips) != 1 {
		t.Fatalf("expected 1 Trip, got %d", len(result.Trips))
	}
	if result.Trips[0].Action != "DESTROY" {
		t.Errorf("expected Trip.Action=DESTROY, got %q", result.Trips[0].Action)
	}
	if result.Trips[0].Type != "aws_ses_domain_identity" {
		t.Errorf("expected Trip.Type=aws_ses_domain_identity, got %q", result.Trips[0].Type)
	}
	if result.Trips[0].Address != "aws_ses_domain_identity.sandboxes" {
		t.Errorf("expected Trip.Address=aws_ses_domain_identity.sandboxes, got %q", result.Trips[0].Address)
	}
}

func TestEvaluate_ProtectedReplace(t *testing.T) {
	r := Report{
		Module: "ses",
		Replaces: []ResourceChange{
			{Address: "aws_s3_bucket.mailbox", Type: "aws_s3_bucket", Action: "replace"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if !result.Blocked {
		t.Errorf("expected Blocked=true for protected replace")
	}
	if len(result.Trips) != 1 {
		t.Fatalf("expected 1 Trip, got %d", len(result.Trips))
	}
	if result.Trips[0].Action != "REPLACE" {
		t.Errorf("expected Trip.Action=REPLACE, got %q", result.Trips[0].Action)
	}
}

func TestEvaluate_NoTripOnUpdate(t *testing.T) {
	r := Report{
		Module: "ses",
		Changes: []ResourceChange{
			{Address: "aws_dynamodb_table.metadata", Type: "aws_dynamodb_table", Action: "update"},
		},
		Adds: []ResourceChange{
			{Address: "aws_s3_bucket.new", Type: "aws_s3_bucket", Action: "create"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if result.Blocked {
		t.Errorf("expected Blocked=false: updates and creates do not trip the gate")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected empty Trips, got %d", len(result.Trips))
	}
}

func TestEvaluate_NoTripOnNonProtected(t *testing.T) {
	r := Report{
		Module: "create-handler",
		Destroys: []ResourceChange{
			{Address: "aws_lambda_function.create_handler", Type: "aws_lambda_function", Action: "delete"},
		},
		Replaces: []ResourceChange{
			{Address: "aws_iam_role.create_handler", Type: "aws_iam_role", Action: "replace"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if result.Blocked {
		t.Errorf("expected Blocked=false: aws_lambda_function and aws_iam_role are not in ProtectedTypes")
	}
	if len(result.Trips) != 0 {
		t.Errorf("expected empty Trips, got %d", len(result.Trips))
	}
}

func TestEvaluate_OverrideClearsBlock(t *testing.T) {
	r := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_domain_identity.sandboxes", Type: "aws_ses_domain_identity", Action: "delete"},
		},
	}
	result := Evaluate([]Report{r}, true) // acceptDestroys=true
	if result.Blocked {
		t.Errorf("expected Blocked=false when acceptDestroys=true")
	}
	// Trips list STILL populated (operator visibility contract)
	if len(result.Trips) != 1 {
		t.Errorf("expected Trips still populated when override active, got %d trips", len(result.Trips))
	}
	if result.Trips[0].Action != "DESTROY" {
		t.Errorf("expected Trip.Action=DESTROY, got %q", result.Trips[0].Action)
	}
}

func TestEvaluate_ParseFailTrips(t *testing.T) {
	r := Report{
		Module:     "network",
		ParseFailed: true,
	}
	result := Evaluate([]Report{r}, false)
	if !result.Blocked {
		t.Errorf("expected Blocked=true for ParseFailed report (conservative trip)")
	}
	if len(result.Trips) != 1 {
		t.Fatalf("expected 1 Trip, got %d", len(result.Trips))
	}
	if result.Trips[0].Action != "PARSE-FAIL" {
		t.Errorf("expected Trip.Action=PARSE-FAIL, got %q", result.Trips[0].Action)
	}
	want := "plan JSON parse failed — conservative trip"
	if result.Trips[0].Reason != want {
		t.Errorf("expected Trip.Reason=%q, got %q", want, result.Trips[0].Reason)
	}
	// acceptDestroys=true should clear Blocked even for parse-fail
	result2 := Evaluate([]Report{r}, true)
	if result2.Blocked {
		t.Errorf("expected Blocked=false when acceptDestroys=true even for parse-fail")
	}
	if len(result2.Trips) != 1 {
		t.Errorf("expected Trips still populated on override, got %d", len(result2.Trips))
	}
}

func TestEvaluate_MultiModuleAggregation(t *testing.T) {
	clean := Report{
		Module: "network",
		Adds:   []ResourceChange{{Address: "aws_vpc.main", Type: "aws_vpc", Action: "create"}},
	}
	withProtected := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_domain_identity.sandboxes", Type: "aws_ses_domain_identity", Action: "delete"},
		},
	}
	nonProtected := Report{
		Module: "create-handler",
		Destroys: []ResourceChange{
			{Address: "aws_lambda_function.create_handler", Type: "aws_lambda_function", Action: "delete"},
		},
	}
	result := Evaluate([]Report{clean, withProtected, nonProtected}, false)
	if !result.Blocked {
		t.Errorf("expected Blocked=true: one module has protected destroy")
	}
	if len(result.Trips) != 1 {
		t.Fatalf("expected 1 Trip, got %d", len(result.Trips))
	}
	if result.Trips[0].Module != "ses" {
		t.Errorf("expected Trip.Module=ses, got %q", result.Trips[0].Module)
	}
}

func TestEvaluate_SESReceiptRuleDestroy(t *testing.T) {
	// GAP-1: aws_ses_receipt_rule was the exact resource type destroyed in the Phase 82->84
	// incident. This test asserts the gate trips (Blocked=true) for that resource type.
	r := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_receipt_rule.sandbox_catchall", Type: "aws_ses_receipt_rule", Action: "delete"},
		},
	}
	result := Evaluate([]Report{r}, false)
	if !result.Blocked {
		t.Errorf("expected Blocked=true for aws_ses_receipt_rule destroy (GAP-1 regression)")
	}
	if len(result.Trips) != 1 {
		t.Fatalf("expected 1 Trip, got %d", len(result.Trips))
	}
	if result.Trips[0].Action != "DESTROY" {
		t.Errorf("expected Trip.Action=DESTROY, got %q", result.Trips[0].Action)
	}
	if result.Trips[0].Type != "aws_ses_receipt_rule" {
		t.Errorf("expected Trip.Type=aws_ses_receipt_rule, got %q", result.Trips[0].Type)
	}
	if result.Trips[0].Address != "aws_ses_receipt_rule.sandbox_catchall" {
		t.Errorf("expected Trip.Address=aws_ses_receipt_rule.sandbox_catchall, got %q", result.Trips[0].Address)
	}
}

func TestEvaluate_SESReceiptRuleOverride(t *testing.T) {
	// Confirms override semantics: acceptDestroys=true clears Blocked but Trips still populated
	// (operator visibility contract), even for the GAP-1 incident type.
	r := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_receipt_rule.sandbox_catchall", Type: "aws_ses_receipt_rule", Action: "delete"},
		},
	}
	result := Evaluate([]Report{r}, true) // acceptDestroys=true
	if result.Blocked {
		t.Errorf("expected Blocked=false when acceptDestroys=true")
	}
	// Trips list STILL populated (operator visibility contract)
	if len(result.Trips) != 1 {
		t.Errorf("expected Trips still populated when override active, got %d trips", len(result.Trips))
	}
	if result.Trips[0].Type != "aws_ses_receipt_rule" {
		t.Errorf("expected Trip.Type=aws_ses_receipt_rule, got %q", result.Trips[0].Type)
	}
}

func TestEvaluate_TripsListedOnOverride(t *testing.T) {
	r := Report{
		Module: "ses",
		Destroys: []ResourceChange{
			{Address: "aws_ses_domain_identity.sandboxes", Type: "aws_ses_domain_identity", Action: "delete"},
			{Address: "aws_ses_domain_dkim.sandboxes", Type: "aws_ses_domain_dkim", Action: "delete"},
			{Address: "aws_route53_record.dkim[0]", Type: "aws_route53_record", Action: "delete"},
		},
	}
	result := Evaluate([]Report{r}, true) // acceptDestroys=true
	if result.Blocked {
		t.Errorf("expected Blocked=false when acceptDestroys=true")
	}
	if len(result.Trips) != 3 {
		t.Errorf("expected 3 Trips (operator visibility), got %d", len(result.Trips))
	}
}
