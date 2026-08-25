package aws_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// stubAssumeRoleSTS is a minimal AssumeRoleSTSAPI stub that records every
// AssumeRole call it receives and returns either a canned success response
// or a canned error.
type stubAssumeRoleSTS struct {
	calls   []*sts.AssumeRoleInput
	err     error
	success *sts.AssumeRoleOutput
}

func (s *stubAssumeRoleSTS) AssumeRole(_ context.Context, params *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	s.calls = append(s.calls, params)
	if s.err != nil {
		return nil, s.err
	}
	if s.success != nil {
		return s.success, nil
	}
	return &sts.AssumeRoleOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     awssdk.String("AKIA-stub"),
			SecretAccessKey: awssdk.String("secret-stub"),
			SessionToken:    awssdk.String("token-stub"),
			Expiration:      awssdk.Time(time.Now().Add(15 * time.Minute)),
		},
	}, nil
}

// withStubSTSClient installs stub as the STS client AssumeRoleConfig
// constructs against, restoring the real constructor when the test ends.
func withStubSTSClient(t *testing.T, stub *stubAssumeRoleSTS) {
	t.Helper()
	original := kmaws.NewAssumeRoleSTSClient
	kmaws.NewAssumeRoleSTSClient = func(awssdk.Config) kmaws.AssumeRoleSTSAPI {
		return stub
	}
	t.Cleanup(func() { kmaws.NewAssumeRoleSTSClient = original })
}

// Test 1: a stub STS client returning valid credentials produces an
// aws.Config with a non-nil Credentials provider, Region set to the region
// argument, and the base config's other settings (a marker AppID and
// RetryMaxAttempts) preserved — proving the base was copied, not rebuilt.
func TestAssumeRoleConfig_Success_PreservesBaseAndSetsRegion(t *testing.T) {
	stub := &stubAssumeRoleSTS{}
	withStubSTSClient(t, stub)

	base := awssdk.Config{
		Region:           "us-west-2",
		AppID:            "km-marker-app-id",
		RetryMaxAttempts: 7,
	}

	got, err := kmaws.AssumeRoleConfig(context.Background(), base, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", "us-east-1")
	if err != nil {
		t.Fatalf("AssumeRoleConfig: %v", err)
	}
	if got.Credentials == nil {
		t.Fatal("Credentials provider is nil")
	}
	if got.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", got.Region, "us-east-1")
	}
	if got.AppID != base.AppID {
		t.Errorf("AppID = %q, want %q (base config not preserved)", got.AppID, base.AppID)
	}
	if got.RetryMaxAttempts != base.RetryMaxAttempts {
		t.Errorf("RetryMaxAttempts = %d, want %d (base config not preserved)", got.RetryMaxAttempts, base.RetryMaxAttempts)
	}
}

// Test 2: when the stub STS client errors, Retrieve on the returned
// credentials provider surfaces that error — the caller does NOT silently
// receive the base config's own credentials. This is the fail-closed
// guarantee the doc comment promises and the whole reason this helper exists
// instead of copying the uninit.go fallback pattern.
func TestAssumeRoleConfig_Failure_SurfacesErrorNoFallback(t *testing.T) {
	wantErr := fmt.Errorf("AccessDenied: not authorized to assume role")
	stub := &stubAssumeRoleSTS{err: wantErr}
	withStubSTSClient(t, stub)

	baseCreds := awssdk.Credentials{AccessKeyID: "BASE-AKID", SecretAccessKey: "base-secret"}
	base := awssdk.Config{
		Region:      "us-west-2",
		Credentials: awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) { return baseCreds, nil }),
	}

	got, err := kmaws.AssumeRoleConfig(context.Background(), base, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", "us-east-1")
	if err != nil {
		t.Fatalf("AssumeRoleConfig itself must not error on construction: %v", err)
	}

	retrieved, retrieveErr := got.Credentials.Retrieve(context.Background())
	if retrieveErr == nil {
		t.Fatal("expected Retrieve to surface the AssumeRole error, got nil")
	}
	if retrieved.AccessKeyID == baseCreds.AccessKeyID {
		t.Error("retrieved credentials must not equal the base config's own credentials — that would be a silent fallback")
	}
}

// Test 3: a non-empty externalID is passed through to the AssumeRole input
// as a pointer; an empty externalID results in a nil ExternalId field (AWS
// rejects an empty-string external id).
func TestAssumeRoleConfig_ExternalID(t *testing.T) {
	t.Run("non-empty external id is passed through", func(t *testing.T) {
		stub := &stubAssumeRoleSTS{}
		withStubSTSClient(t, stub)

		got, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "arn:aws:iam::999999999999:role/km-gpu-launcher", "km-ext-id-123", "us-east-1")
		if err != nil {
			t.Fatalf("AssumeRoleConfig: %v", err)
		}
		if _, retErr := got.Credentials.Retrieve(context.Background()); retErr != nil {
			t.Fatalf("Retrieve: %v", retErr)
		}
		if len(stub.calls) != 1 {
			t.Fatalf("expected 1 AssumeRole call, got %d", len(stub.calls))
		}
		if stub.calls[0].ExternalId == nil || *stub.calls[0].ExternalId != "km-ext-id-123" {
			t.Errorf("ExternalId = %v, want %q", stub.calls[0].ExternalId, "km-ext-id-123")
		}
	})

	t.Run("empty external id sets no field", func(t *testing.T) {
		stub := &stubAssumeRoleSTS{}
		withStubSTSClient(t, stub)

		got, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", "us-east-1")
		if err != nil {
			t.Fatalf("AssumeRoleConfig: %v", err)
		}
		if _, retErr := got.Credentials.Retrieve(context.Background()); retErr != nil {
			t.Fatalf("Retrieve: %v", retErr)
		}
		if len(stub.calls) != 1 {
			t.Fatalf("expected 1 AssumeRole call, got %d", len(stub.calls))
		}
		if stub.calls[0].ExternalId != nil {
			t.Errorf("ExternalId = %q, want nil", *stub.calls[0].ExternalId)
		}
	})
}

// Test 4: two consecutive Retrieve calls within the credential lifetime
// issue only one AssumeRole call against the stub — proving the credentials
// cache wrapper (aws.NewCredentialsCache) is present. stscreds.AssumeRoleProvider
// does not self-cache; without the wrapper this would be two calls.
func TestAssumeRoleConfig_CachesCredentials(t *testing.T) {
	stub := &stubAssumeRoleSTS{}
	withStubSTSClient(t, stub)

	got, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", "us-east-1")
	if err != nil {
		t.Fatalf("AssumeRoleConfig: %v", err)
	}

	if _, err := got.Credentials.Retrieve(context.Background()); err != nil {
		t.Fatalf("first Retrieve: %v", err)
	}
	if _, err := got.Credentials.Retrieve(context.Background()); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}

	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 AssumeRole call across 2 Retrieve calls (cache not wrapping the provider), got %d", len(stub.calls))
	}
}

// Test 5: RoleSessionName is non-empty and identifies km on every call.
func TestAssumeRoleConfig_RoleSessionNameIdentifiesKM(t *testing.T) {
	stub := &stubAssumeRoleSTS{}
	withStubSTSClient(t, stub)

	got, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", "us-east-1")
	if err != nil {
		t.Fatalf("AssumeRoleConfig: %v", err)
	}
	if _, err := got.Credentials.Retrieve(context.Background()); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 AssumeRole call, got %d", len(stub.calls))
	}
	name := awssdk.ToString(stub.calls[0].RoleSessionName)
	if name == "" {
		t.Fatal("RoleSessionName is empty")
	}
	if !containsKM(name) {
		t.Errorf("RoleSessionName = %q, want it to identify km", name)
	}
}

// TestAssumeRoleConfig_EmptyArgsFailClosed verifies the argument-validation
// guard: an empty roleARN or region must error rather than produce a
// regionless/roleless config that would silently resolve against the
// caller's own account.
func TestAssumeRoleConfig_EmptyArgsFailClosed(t *testing.T) {
	stub := &stubAssumeRoleSTS{}
	withStubSTSClient(t, stub)

	if _, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "", "", "us-east-1"); err == nil {
		t.Error("expected error for empty roleARN, got nil")
	}
	if _, err := kmaws.AssumeRoleConfig(context.Background(), awssdk.Config{}, "arn:aws:iam::999999999999:role/km-gpu-launcher", "", ""); err == nil {
		t.Error("expected error for empty region, got nil")
	}
	if len(stub.calls) != 0 {
		t.Errorf("expected no AssumeRole calls for invalid arguments, got %d", len(stub.calls))
	}
}

func containsKM(s string) bool {
	for i := 0; i+2 <= len(s); i++ {
		if (s[i] == 'k' || s[i] == 'K') && (s[i+1] == 'm') {
			return true
		}
	}
	return false
}
