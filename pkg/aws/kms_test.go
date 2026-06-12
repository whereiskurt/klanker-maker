package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type fakeKMSDescribe struct {
	gotKeyID string
	arn      string
	err      error
}

func (f *fakeKMSDescribe) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	f.gotKeyID = awssdk.ToString(in.KeyId)
	if f.err != nil {
		return nil, f.err
	}
	return &kms.DescribeKeyOutput{
		KeyMetadata: &kmstypes.KeyMetadata{Arn: awssdk.String(f.arn)},
	}, nil
}

// ResolveKeyARN must resolve an alias (or key id) to the underlying key ARN via DescribeKey.
func TestResolveKeyARN_Alias(t *testing.T) {
	fake := &fakeKMSDescribe{arn: "arn:aws:kms:us-east-1:123456789012:key/abc-123"}
	arn, err := ResolveKeyARN(context.Background(), fake, "alias/km-platform-km-use1")
	if err != nil {
		t.Fatalf("ResolveKeyARN returned error: %v", err)
	}
	if arn != "arn:aws:kms:us-east-1:123456789012:key/abc-123" {
		t.Errorf("arn=%q; want the key ARN from DescribeKey", arn)
	}
	if fake.gotKeyID != "alias/km-platform-km-use1" {
		t.Errorf("DescribeKey called with KeyId=%q; want the alias", fake.gotKeyID)
	}
}

// A DescribeKey error (e.g. alias not found yet) must propagate so the caller can fail-soft.
func TestResolveKeyARN_Error(t *testing.T) {
	fake := &fakeKMSDescribe{err: errors.New("NotFoundException: alias not found")}
	_, err := ResolveKeyARN(context.Background(), fake, "alias/km-platform-km-use1")
	if err == nil {
		t.Fatal("expected error when DescribeKey fails")
	}
}

// An empty key id is an input error, not an AWS round-trip.
func TestResolveKeyARN_EmptyInput(t *testing.T) {
	fake := &fakeKMSDescribe{arn: "arn:aws:kms:...:key/x"}
	_, err := ResolveKeyARN(context.Background(), fake, "")
	if err == nil {
		t.Fatal("expected error for empty keyIDOrAlias")
	}
	if fake.gotKeyID != "" {
		t.Error("DescribeKey must not be called for empty input")
	}
}
