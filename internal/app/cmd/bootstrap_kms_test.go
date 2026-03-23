package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// mockKMSEnsureClient satisfies KMSEnsureAPI for bootstrap KMS tests.
type mockKMSEnsureClient struct {
	describeOut *kms.DescribeKeyOutput
	describeErr error
	createOut   *kms.CreateKeyOutput
	createErr   error
	aliasErr    error
}

func (m *mockKMSEnsureClient) DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	return m.describeOut, m.describeErr
}

func (m *mockKMSEnsureClient) CreateKey(ctx context.Context, params *kms.CreateKeyInput, optFns ...func(*kms.Options)) (*kms.CreateKeyOutput, error) {
	return m.createOut, m.createErr
}

func (m *mockKMSEnsureClient) CreateAlias(ctx context.Context, params *kms.CreateAliasInput, optFns ...func(*kms.Options)) (*kms.CreateAliasOutput, error) {
	return &kms.CreateAliasOutput{}, m.aliasErr
}

// Compile-time check
var _ KMSEnsureAPI = (*mockKMSEnsureClient)(nil)

// TestEnsureKMSPlatformKey_KeyAlreadyExists verifies that when the alias exists
// (DescribeKey returns nil error), the function logs "already exists" and returns nil.
func TestEnsureKMSPlatformKey_KeyAlreadyExists(t *testing.T) {
	client := &mockKMSEnsureClient{
		describeOut: &kms.DescribeKeyOutput{
			KeyMetadata: &kmstypes.KeyMetadata{
				KeyId: aws.String("key-id-existing"),
			},
		},
		describeErr: nil, // alias already exists
	}
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	var buf bytes.Buffer
	err := ensureKMSPlatformKey(context.Background(), cfg, &buf, client)
	if err != nil {
		t.Errorf("expected nil error when key exists, got: %v", err)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected 'already exists' in output, got: %s", buf.String())
	}
}

// TestEnsureKMSPlatformKey_CreatesKey verifies that when the alias does NOT exist
// (DescribeKey returns error), the function creates the key and alias.
func TestEnsureKMSPlatformKey_CreatesKey(t *testing.T) {
	client := &mockKMSEnsureClient{
		describeErr: errors.New("alias not found"),
		createOut: &kms.CreateKeyOutput{
			KeyMetadata: &kmstypes.KeyMetadata{
				KeyId: aws.String("key-id-new"),
			},
		},
		createErr: nil,
		aliasErr:  nil,
	}
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	var buf bytes.Buffer
	err := ensureKMSPlatformKey(context.Background(), cfg, &buf, client)
	if err != nil {
		t.Errorf("expected nil error when creating key, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "KMS key created") {
		t.Errorf("expected 'KMS key created' in output, got: %s", out)
	}
	if !strings.Contains(out, "key-id-new") {
		t.Errorf("expected key ID in output, got: %s", out)
	}
}
