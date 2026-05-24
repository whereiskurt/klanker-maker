package main

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// mockIdentitySSMAPI records calls to PutParameter, GetParameter, and DeleteParameter.
type mockIdentitySSMAPI struct {
	deletedParams []string
}

func (m *mockIdentitySSMAPI) PutParameter(ctx context.Context, in *ssm.PutParameterInput, opts ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	return &ssm.PutParameterOutput{}, nil
}

func (m *mockIdentitySSMAPI) GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{}, nil
}

func (m *mockIdentitySSMAPI) DeleteParameter(ctx context.Context, in *ssm.DeleteParameterInput, opts ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	m.deletedParams = append(m.deletedParams, awssdk.ToString(in.Name))
	return &ssm.DeleteParameterOutput{}, nil
}

// mockIdentityTableAPI records calls to DeleteItem so tests can assert the row key.
type mockIdentityTableAPI struct {
	deletedSandboxIDs []string
	deletedTables     []string
}

func (m *mockIdentityTableAPI) PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockIdentityTableAPI) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockIdentityTableAPI) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deletedTables = append(m.deletedTables, awssdk.ToString(in.TableName))
	if v, ok := in.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		m.deletedSandboxIDs = append(m.deletedSandboxIDs, v.Value)
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

// TestCleanupSandboxIdentityWith_DeletesAllExpectedKeys is the smoking-gun test for
// the Phase 70 follow-up: a remote km destroy MUST delete the SSM signing/encryption/
// safe-phrase parameters and the km-identities DDB row. Failing to clean these up
// causes 401 bad_signature when the alias is reused (the bridge's alias-index GSI
// returns the stale pubkey).
func TestCleanupSandboxIdentityWith_DeletesAllExpectedKeys(t *testing.T) {
	ssmMock := &mockIdentitySSMAPI{}
	ddbMock := &mockIdentityTableAPI{}

	cleanupSandboxIdentityWith(context.Background(), ssmMock, ddbMock, "km-identities", "km", "sb-aabbccdd")

	wantParams := []string{
		"/km/sandbox/sb-aabbccdd/signing-key",
		"/km/sandbox/sb-aabbccdd/encryption-key",
		"/km/sandbox/sb-aabbccdd/safe-phrase",
	}
	if len(ssmMock.deletedParams) != len(wantParams) {
		t.Fatalf("expected %d SSM DeleteParameter calls, got %d: %v", len(wantParams), len(ssmMock.deletedParams), ssmMock.deletedParams)
	}
	for i, want := range wantParams {
		if ssmMock.deletedParams[i] != want {
			t.Errorf("SSM delete[%d]: want %q, got %q", i, want, ssmMock.deletedParams[i])
		}
	}

	if len(ddbMock.deletedSandboxIDs) != 1 || ddbMock.deletedSandboxIDs[0] != "sb-aabbccdd" {
		t.Errorf("expected DDB DeleteItem on sandbox_id=sb-aabbccdd, got %v", ddbMock.deletedSandboxIDs)
	}
	if len(ddbMock.deletedTables) != 1 || ddbMock.deletedTables[0] != "km-identities" {
		t.Errorf("expected DDB DeleteItem on table km-identities, got %v", ddbMock.deletedTables)
	}
}

// TestCleanupSandboxIdentityWith_NilClientSkipsSilently ensures the cleanup is a
// no-op when either client is nil — preserves the TTL handler's optional-dependency
// pattern so tests that don't wire identity clients still work.
func TestCleanupSandboxIdentityWith_NilClientSkipsSilently(t *testing.T) {
	// Should not panic.
	cleanupSandboxIdentityWith(context.Background(), nil, &mockIdentityTableAPI{}, "km-identities", "km", "sb-aabbccdd")
	cleanupSandboxIdentityWith(context.Background(), &mockIdentitySSMAPI{}, nil, "km-identities", "km", "sb-aabbccdd")
	cleanupSandboxIdentityWith(context.Background(), nil, nil, "km-identities", "km", "sb-aabbccdd")
}

// TestIdentitiesTable_FallbackUsesResourcePrefix verifies the env-var fallback
// matches the same "<prefix>-identities" convention used everywhere else.
func TestIdentitiesTable_FallbackUsesResourcePrefix(t *testing.T) {
	t.Setenv("KM_IDENTITIES_TABLE", "")
	t.Setenv("KM_RESOURCE_PREFIX", "kph")

	if got := identitiesTable(); got != "kph-identities" {
		t.Errorf("identitiesTable() with KM_RESOURCE_PREFIX=kph: want %q, got %q", "kph-identities", got)
	}

	t.Setenv("KM_IDENTITIES_TABLE", "custom-identities")
	if got := identitiesTable(); got != "custom-identities" {
		t.Errorf("identitiesTable() with KM_IDENTITIES_TABLE set: want %q, got %q", "custom-identities", got)
	}
}
