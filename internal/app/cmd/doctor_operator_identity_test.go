package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeIdentityGetItem implements IdentityRowGetItemAPI for checkOperatorIdentity tests.
type fakeIdentityGetItem struct {
	item map[string]dynamodbtypes.AttributeValue
	err  error
}

func (f *fakeIdentityGetItem) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}

// signing-key path SigningKeyPath("sec","operator") resolves to; hardcoded so the test
// and the check agree on the SSM key the presence-probe reads.
const testOperatorSigningKeyPath = "/sec/sandbox/operator/signing-key"

func ssmWithOperatorKey() *mockSSMReadClient {
	return &mockSSMReadClient{outputs: map[string]*ssm.GetParameterOutput{
		testOperatorSigningKeyPath: {Parameter: &ssmtypes.Parameter{Value: aws.String("AAAA-base64-ed25519-priv")}},
	}}
}

func TestCheckOperatorIdentity(t *testing.T) {
	const table = "sec-identities"
	pubKeyRow := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "operator"},
		"public_key": &dynamodbtypes.AttributeValueMemberS{Value: "ZmFrZS1wdWJrZXk="},
	}

	tests := []struct {
		name        string
		ddb         IdentityRowGetItemAPI
		ssm         SSMReadAPI
		wantStatus  CheckStatus
		wantSubstrs []string
	}{
		{
			name:       "row present with public key → OK",
			ddb:        &fakeIdentityGetItem{item: pubKeyRow},
			ssm:        ssmWithOperatorKey(),
			wantStatus: CheckOK,
		},
		{
			name:        "row absent but SSM key present → WARN with unknown_sender remediation",
			ddb:         &fakeIdentityGetItem{item: nil}, // empty item = absent row
			ssm:         ssmWithOperatorKey(),
			wantStatus:  CheckWarn,
			wantSubstrs: []string{"unknown_sender", "--republish-operator-identity", "km init"},
		},
		{
			name:        "row and SSM key both absent → WARN pointing at km init",
			ddb:         &fakeIdentityGetItem{item: nil},
			ssm:         &mockSSMReadClient{}, // no params → ParameterNotFound
			wantStatus:  CheckWarn,
			wantSubstrs: []string{"km init"},
		},
		{
			name: "row present but no public_key → WARN",
			ddb: &fakeIdentityGetItem{item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "operator"},
			}},
			ssm:         ssmWithOperatorKey(),
			wantStatus:  CheckWarn,
			wantSubstrs: []string{"--republish-operator-identity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkOperatorIdentity(context.Background(), tt.ddb, tt.ssm, table, "sec")
			if got.Status != tt.wantStatus {
				t.Errorf("status = %v; want %v (msg=%q)", got.Status, tt.wantStatus, got.Message)
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(got.Message, sub) {
					t.Errorf("message %q missing %q", got.Message, sub)
				}
			}
		})
	}
}

// TestCheckOperatorIdentity_DescribeErrorIsWarn ensures a transient DDB error doesn't
// crash the check — it degrades to a non-fatal WARN (a read failure must not mask the
// platform as healthy, nor abort the doctor run).
func TestCheckOperatorIdentity_DescribeErrorIsWarn(t *testing.T) {
	got := checkOperatorIdentity(context.Background(),
		&fakeIdentityGetItem{err: errors.New("AWS: throttled")},
		ssmWithOperatorKey(), "sec-identities", "sec")
	if got.Status == CheckOK {
		t.Errorf("a DDB read error must not report OK; got %v (%q)", got.Status, got.Message)
	}
}
