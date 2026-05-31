package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
)

// --ignore-prefix / doctor_ignore_prefixes: a sibling install's resources should
// report OK (with a note) instead of WARN. These tests lock that behaviour into
// each of the three cross-install checks plus the shared ignoredNote helper.

func TestCheckSESRules_IgnoredSiblingIsOK(t *testing.T) {
	mock := &mockSESReceiptRuleAPI{
		ruleNames: []string{"kph-operator-inbound", "km2-operator-inbound"},
	}
	ignore := map[string]bool{"km2": true}
	result := checkSESRules(context.Background(), mock, "kph", ignore)
	if result.Status != CheckOK {
		t.Fatalf("status = %v; want CheckOK (km2 ignored): %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "ignored sibling") || !strings.Contains(result.Message, "km2") {
		t.Errorf("message %q should note the ignored km2 sibling", result.Message)
	}
}

func TestCheckSESRules_IgnoredPlusUnknownOrphanWarns(t *testing.T) {
	mock := &mockSESReceiptRuleAPI{
		ruleNames: []string{"kph-operator-inbound", "km2-operator-inbound", "xx-operator-inbound"},
	}
	ignore := map[string]bool{"km2": true}
	result := checkSESRules(context.Background(), mock, "kph", ignore)
	if result.Status != CheckWarn {
		t.Fatalf("status = %v; want CheckWarn (xx still unknown): %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "xx-operator-inbound") {
		t.Errorf("message %q should still WARN about the unknown xx orphan", result.Message)
	}
	if strings.Contains(result.Message, "orphan SES rules: km2-operator-inbound") {
		t.Errorf("message %q should NOT list km2 as an orphan", result.Message)
	}
	if !strings.Contains(result.Message, "ignored sibling") || !strings.Contains(result.Message, "km2") {
		t.Errorf("message %q should note km2 was ignored", result.Message)
	}
}

func TestCheckSharedSecretsKey_IgnoredSiblingIsOK(t *testing.T) {
	fake := &doctorFakeKMSAliasLister{
		aliasNames: []string{"alias/km-sandbox-secrets", "alias/km2-sandbox-secrets"},
	}
	ignore := map[string]bool{"km2": true}
	result := checkSharedSecretsKey(context.Background(), fake, "km", ignore)
	if result.Status != CheckOK {
		t.Fatalf("status = %v; want CheckOK (km2 ignored): %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "healthy") {
		t.Errorf("message %q should report own alias healthy", result.Message)
	}
	if !strings.Contains(result.Message, "km2") {
		t.Errorf("message %q should note the ignored km2 sibling", result.Message)
	}
}

func TestCheckOrphanSCPs_IgnoredSiblingIsOK(t *testing.T) {
	client := &fakeOrgsListAllPoliciesClient{
		policies: []orgtypes.PolicySummary{
			{Name: aws.String("rg-sandbox-containment"), Id: aws.String("p-rg001")},
		},
	}
	ignore := map[string]bool{"rg": true}
	result := checkOrphanSCPs(context.Background(), client, "tg", ignore)
	if result.Status != CheckOK {
		t.Fatalf("status = %v; want CheckOK (rg ignored): %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "rg") {
		t.Errorf("message %q should note the ignored rg sibling", result.Message)
	}
}

func TestCheckOrphanSCPs_IgnoredPlusUnknownWarns(t *testing.T) {
	client := &fakeOrgsListAllPoliciesClient{
		policies: []orgtypes.PolicySummary{
			{Name: aws.String("rg-sandbox-containment"), Id: aws.String("p-rg001")},
			{Name: aws.String("xx-sandbox-containment"), Id: aws.String("p-xx001")},
		},
	}
	ignore := map[string]bool{"rg": true}
	result := checkOrphanSCPs(context.Background(), client, "tg", ignore)
	if result.Status != CheckWarn {
		t.Fatalf("status = %v; want CheckWarn (xx unknown): %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "xx-sandbox-containment") {
		t.Errorf("message %q should WARN about the unknown xx orphan", result.Message)
	}
	if strings.Contains(result.Message, "rg-sandbox-containment (id:") {
		t.Errorf("message %q should NOT list rg as an orphan", result.Message)
	}
}

func TestIgnoredNote(t *testing.T) {
	if got := ignoredNote(nil); got != "" {
		t.Errorf("ignoredNote(nil) = %q; want empty", got)
	}
	// De-duplicated and sorted, with a count.
	got := ignoredNote([]string{"rg", "km2", "rg", "ak"})
	if !strings.Contains(got, "3 ignored sibling(s)") {
		t.Errorf("ignoredNote = %q; want count of 3 unique", got)
	}
	if !strings.Contains(got, "ak, km2, rg") {
		t.Errorf("ignoredNote = %q; want sorted 'ak, km2, rg'", got)
	}
}
