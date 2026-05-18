package cmd

// Phase 84.4 Plan 06 — TDD RED test file.
//
// Tests autoImportFoundationSESRecords (declared in bootstrap.go with a
// "not implemented" stub). This file uses package cmd (not cmd_test) because
// sesDkimGetter, route53RecordLister, resourceImporter, and FoundationStateReader
// are unexported / internal interfaces.
//
// Run:
//   go test ./internal/app/cmd/ -run TestRunBootstrapSharedSESAutoImport -v
//
// Expected before Step 4: tests compile and fail (RED) — the stub returns "not implemented".
// Expected after Step 4: all 7 subtests pass (GREEN).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// =============================================================================
// Test doubles
// =============================================================================

// fakeSesDkimGetter implements sesDkimGetter for unit tests.
// tokens: the DKIM tokens to return (empty = no DKIM set up, simulates domain absent or unconfigured).
// err: when non-nil, GetIdentityDkimAttributes returns this error.
type fakeSesDkimGetter struct {
	tokens []string
	err    error
}

func (f *fakeSesDkimGetter) GetIdentityDkimAttributes(ctx context.Context, in *ses.GetIdentityDkimAttributesInput, opts ...func(*ses.Options)) (*ses.GetIdentityDkimAttributesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	domain := ""
	if len(in.Identities) > 0 {
		domain = in.Identities[0]
	}
	return &ses.GetIdentityDkimAttributesOutput{
		DkimAttributes: map[string]sestypes.IdentityDkimAttributes{
			domain: {DkimTokens: f.tokens},
		},
	}, nil
}

// fakeRoute53RecordLister implements route53RecordLister for unit tests.
// hasMX / hasTXT / domain: controls which records appear in the fake response.
type fakeRoute53RecordLister struct {
	hasMX  bool
	hasTXT bool
	domain string
	err    error
}

func (f *fakeRoute53RecordLister) ListResourceRecordSets(ctx context.Context, in *route53.ListResourceRecordSetsInput, opts ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	var recs []r53types.ResourceRecordSet
	if f.hasMX {
		recs = append(recs, r53types.ResourceRecordSet{
			Name: aws.String(f.domain + "."),
			Type: r53types.RRTypeMx,
		})
	}
	if f.hasTXT {
		recs = append(recs, r53types.ResourceRecordSet{
			Name: aws.String("_amazonses." + f.domain + "."),
			Type: r53types.RRTypeTxt,
		})
	}
	return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: recs}, nil
}

// fakeStateOwner implements FoundationStateReader for unit tests.
// owned: set of resource addresses the state claims to own.
type fakeStateOwner struct {
	owned map[string]bool
}

func (f *fakeStateOwner) StateOwns(_ context.Context, addr string) (bool, error) {
	return f.owned[addr], nil
}

// fakeImporter implements resourceImporter for unit tests.
// Records calls to Import.
// failOnCall: 0 = never fail; N = fail on the Nth call (1-indexed).
// importErr: error to return on the failing call.
type fakeImporter struct {
	calls      []importCall
	importErr  error
	failOnCall int // 0 = never; N = fail on call N (1-indexed)
	failErr    error
}

type importCall struct {
	addr string
	id   string
}

func (f *fakeImporter) Import(_ context.Context, _ string, address, id string) error {
	f.calls = append(f.calls, importCall{addr: address, id: id})
	if f.failOnCall > 0 && len(f.calls) == f.failOnCall {
		return f.failErr
	}
	return f.importErr
}

// =============================================================================
// Tests
// =============================================================================

const (
	testDomain      = "sandboxes.example.com"
	testHostedZone  = "Z1ABCDEFGHIJKL"
	testSesDir      = "/fake/ses-dir"
	testToken0      = "aaabbbccc111"
	testToken1      = "dddeeefff222"
	testToken2      = "ggghhhjjj333"
)

func makeTokens() []string { return []string{testToken0, testToken1, testToken2} }

func dkimImportID(token string) string {
	return fmt.Sprintf("%s_%s._domainkey.%s_CNAME", testHostedZone, token, testDomain)
}

// TestRunBootstrapSharedSESAutoImport covers all 7 behaviors from the plan.
func TestRunBootstrapSharedSESAutoImport(t *testing.T) {
	ctx := context.Background()

	// 1. domain_absent_skips_all
	// GIVEN domain identity does NOT exist in AWS (empty DkimTokens),
	// the function logs a warning and returns nil (no imports, apply will create).
	t.Run("domain_absent_skips_all", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: []string{}} // empty = no DKIM set up
		r53Client := &fakeRoute53RecordLister{domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if len(imp.calls) != 0 {
			t.Errorf("expected 0 imports, got %d: %v", len(imp.calls), imp.calls)
		}
	})

	// 2. state_owns_all_skips_all
	// GIVEN domain identity exists AND state already owns all 5 Route53 resources,
	// the function skips all imports and returns nil.
	t.Run("state_owns_all_skips_all", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: makeTokens()}
		r53Client := &fakeRoute53RecordLister{hasMX: true, hasTXT: true, domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{
			"aws_route53_record.dkim[0]":            true,
			"aws_route53_record.dkim[1]":            true,
			"aws_route53_record.dkim[2]":            true,
			"aws_route53_record.mx[0]":              true,
			"aws_route53_record.ses_verification[0]": true,
		}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if len(imp.calls) != 0 {
			t.Errorf("expected 0 imports (all state-owned), got %d: %v", len(imp.calls), imp.calls)
		}
	})

	// 3. dkim_only_imports_three
	// GIVEN domain identity exists AND state has zero Route53 resources AND
	// AWS has 3 DKIM records but no MX/TXT, the function imports 3 DKIM records.
	t.Run("dkim_only_imports_three", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: makeTokens()}
		r53Client := &fakeRoute53RecordLister{hasMX: false, hasTXT: false, domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if len(imp.calls) != 3 {
			t.Fatalf("expected 3 DKIM imports, got %d: %v", len(imp.calls), imp.calls)
		}
		// Verify each DKIM import address and ID.
		for i, token := range makeTokens() {
			wantAddr := fmt.Sprintf("aws_route53_record.dkim[%d]", i)
			wantID := dkimImportID(token)
			if imp.calls[i].addr != wantAddr {
				t.Errorf("call[%d]: want addr %q, got %q", i, wantAddr, imp.calls[i].addr)
			}
			if imp.calls[i].id != wantID {
				t.Errorf("call[%d]: want id %q, got %q", i, wantID, imp.calls[i].id)
			}
		}
	})

	// 4. dkim_mx_txt_imports_five
	// GIVEN domain identity exists AND state has zero Route53 resources AND
	// AWS has 3 DKIM + MX + TXT, the function imports all 5.
	t.Run("dkim_mx_txt_imports_five", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: makeTokens()}
		r53Client := &fakeRoute53RecordLister{hasMX: true, hasTXT: true, domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if len(imp.calls) != 5 {
			t.Fatalf("expected 5 imports (3 DKIM + MX + TXT), got %d: %v", len(imp.calls), imp.calls)
		}
		// First 3 are DKIM.
		for i := 0; i < 3; i++ {
			wantAddr := fmt.Sprintf("aws_route53_record.dkim[%d]", i)
			if imp.calls[i].addr != wantAddr {
				t.Errorf("call[%d]: want addr %q, got %q", i, wantAddr, imp.calls[i].addr)
			}
		}
		// Call 3 is MX.
		wantMXAddr := "aws_route53_record.mx[0]"
		wantMXID := fmt.Sprintf("%s_%s_MX", testHostedZone, testDomain)
		if imp.calls[3].addr != wantMXAddr {
			t.Errorf("MX: want addr %q, got %q", wantMXAddr, imp.calls[3].addr)
		}
		if imp.calls[3].id != wantMXID {
			t.Errorf("MX: want id %q, got %q", wantMXID, imp.calls[3].id)
		}
		// Call 4 is TXT (double underscore — record starts with _amazonses).
		wantTXTAddr := "aws_route53_record.ses_verification[0]"
		wantTXTID := fmt.Sprintf("%s__amazonses.%s_TXT", testHostedZone, testDomain)
		if imp.calls[4].addr != wantTXTAddr {
			t.Errorf("TXT: want addr %q, got %q", wantTXTAddr, imp.calls[4].addr)
		}
		if imp.calls[4].id != wantTXTID {
			t.Errorf("TXT: want id %q, got %q", wantTXTID, imp.calls[4].id)
		}
		// Verify double underscore in TXT import ID.
		if !strings.Contains(wantTXTID, "__amazonses.") {
			t.Error("TXT import ID must have double underscore before _amazonses")
		}
	})

	// 5. partial_dkim_imports_missing
	// GIVEN domain identity exists AND state owns DKIM[0] but not DKIM[1], DKIM[2],
	// the function imports DKIM[1] and DKIM[2] only (idempotency).
	t.Run("partial_dkim_imports_missing", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: makeTokens()}
		r53Client := &fakeRoute53RecordLister{hasMX: false, hasTXT: false, domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{
			"aws_route53_record.dkim[0]": true, // DKIM[0] already owned — should be skipped
		}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if len(imp.calls) != 2 {
			t.Fatalf("expected 2 imports (DKIM[1] + DKIM[2]), got %d: %v", len(imp.calls), imp.calls)
		}
		if imp.calls[0].addr != "aws_route53_record.dkim[1]" {
			t.Errorf("want dkim[1], got %q", imp.calls[0].addr)
		}
		if imp.calls[1].addr != "aws_route53_record.dkim[2]" {
			t.Errorf("want dkim[2], got %q", imp.calls[1].addr)
		}
	})

	// 6. import_error_propagates
	// GIVEN any Runner.Import call fails with a non-"already-imported" error,
	// the function returns the error without calling apply.
	t.Run("import_error_propagates", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: makeTokens()}
		r53Client := &fakeRoute53RecordLister{domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{}}
		imp := &fakeImporter{importErr: errors.New("terragrunt: auth failure")}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err == nil {
			t.Fatal("expected error from failed import, got nil")
		}
		if !strings.Contains(err.Error(), "terragrunt: auth failure") {
			t.Errorf("expected error to wrap import failure, got %q", err.Error())
		}
		// Only the first import call should have been attempted.
		if len(imp.calls) != 1 {
			t.Errorf("expected 1 import attempt before failure, got %d", len(imp.calls))
		}
	})

	// 7. empty_dkim_tokens_logs_warning_returns_nil
	// GIVEN GetIdentityDkimAttributes returns empty DkimTokens (DKIM not yet set up),
	// the function logs a warning and returns nil — apply will create them.
	t.Run("empty_dkim_tokens_logs_warning_returns_nil", func(t *testing.T) {
		sesClient := &fakeSesDkimGetter{tokens: []string{}}
		r53Client := &fakeRoute53RecordLister{domain: testDomain}
		state := &fakeStateOwner{owned: map[string]bool{}}
		imp := &fakeImporter{}

		err := autoImportFoundationSESRecords(ctx, imp, testSesDir, state, sesClient, r53Client, testDomain, testHostedZone)
		if err != nil {
			t.Fatalf("expected nil (warning + return), got %v", err)
		}
		if len(imp.calls) != 0 {
			t.Errorf("expected 0 imports when DKIM tokens empty, got %d", len(imp.calls))
		}
	})
}

// TestRunBootstrapSharedSES_AutoImportFiresWhenStateOwnsIdentity verifies that
// autoImportFoundationSESRecords still imports un-imported records even when
// the foundation tfstate claims ownership of the domain identity
// (registerID = true in Phase 84.1 import-and-manage semantic).
//
// Phase 84.4.1 gap: the old gate at bootstrap.go:595 was `!registerID && ...`
// which prevented this path from running when registerID=true. This test
// exercises the function body directly, proving it is idempotent and correct
// regardless of the gate condition — the gate fix (dropping !registerID) is
// verified by inspection + the compile-level gate change.
func TestRunBootstrapSharedSES_AutoImportFiresWhenStateOwnsIdentity(t *testing.T) {
	// Configure fakeStateOwner to claim foundation owns the identity (registerID=true
	// scenario) but NOT yet the DKIM Route53 records. This mirrors the shared-domain
	// second-install case that caused the Phase 84.4 UAT failure.
	state := &fakeStateOwner{
		owned: map[string]bool{
			"aws_ses_domain_identity.sandbox[0]": true, // state owns identity
			"aws_ses_receipt_rule_set.shared[0]": true, // state owns rule set
			"aws_route53_record.dkim[0]":         false, // but DKIM not yet imported
			"aws_route53_record.dkim[1]":         false,
			"aws_route53_record.dkim[2]":         false,
		},
	}
	dkim := &fakeSesDkimGetter{tokens: []string{"tok0", "tok1", "tok2"}}
	r53 := &fakeRoute53RecordLister{hasMX: true, hasTXT: true, domain: "sandboxes.example.com"}
	importer := &fakeImporter{}

	err := autoImportFoundationSESRecords(
		context.Background(), importer, "/fake/ses-dir", state, dkim, r53,
		"sandboxes.example.com", "Z1ABC",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected: 3 DKIM + 1 MX + 1 TXT = 5 imports recorded.
	if len(importer.calls) != 5 {
		t.Errorf("expected 5 imports (3 DKIM + MX + TXT), got %d: %v", len(importer.calls), importer.calls)
	}
}

// TestRunBootstrapSharedSES_FailFastRecoveryMessage verifies that wrapAutoImportError
// produces an operator-actionable error message containing a `terragrunt import`
// recovery command, the ses dir path, and the email domain.
//
// This is a direct test of the helper — if wrapAutoImportError's format string
// changes and drops any of these elements, the test fails immediately.
func TestRunBootstrapSharedSES_FailFastRecoveryMessage(t *testing.T) {
	const sesDirPath = "/fake/ses-dir"
	const domain = "sandboxes.example.com"

	wrapped := wrapAutoImportError(errors.New("simulated terragrunt failure"), sesDirPath, domain)

	if !strings.Contains(wrapped.Error(), "terragrunt import") {
		t.Errorf("missing 'terragrunt import' recovery command in error: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), sesDirPath) {
		t.Errorf("missing foundation ses dir path %q in error: %v", sesDirPath, wrapped)
	}
	if !strings.Contains(wrapped.Error(), domain) {
		t.Errorf("missing email domain %q in error: %v", domain, wrapped)
	}
	if !strings.Contains(wrapped.Error(), "simulated terragrunt failure") {
		t.Errorf("inner error not wrapped; missing 'simulated terragrunt failure' in: %v", wrapped)
	}
}
