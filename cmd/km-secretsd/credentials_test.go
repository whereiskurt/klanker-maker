package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

type fakeSTS struct {
	callerArn  string
	assumeIn   *sts.AssumeRoleInput
	assumeErr  error
	expiresIn  time.Duration
	assumeCall int
}

func (f *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{Arn: aws.String(f.callerArn)}, nil
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.assumeCall++
	f.assumeIn = in
	if f.assumeErr != nil {
		return nil, f.assumeErr
	}
	exp := time.Now().Add(f.expiresIn)
	return &sts.AssumeRoleOutput{Credentials: &ststypes.Credentials{
		AccessKeyId: aws.String("AKIATEST"), SecretAccessKey: aws.String("sk"),
		SessionToken: aws.String("tok"), Expiration: &exp,
	}}, nil
}

const testCallerARN = "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00"

func fencedServer(f *fakeSTS) *Server {
	return &Server{
		FenceEnabled: true, ResourcePrefix: "km",
		ArtifactsBucket: "km-artifacts-1", SandboxID: "abc123",
		STS: f, Audit: &recordingAudit{},
	}
}

// The role ARN is DERIVED from the broker's own identity, not configured, so it
// cannot drift from the role the instance actually runs as.
func TestMintCredentials_DerivesTheRoleARNFromItsOwnIdentity(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:iam::052251888500:role/km-ec2spot-ssm-abc123-use1"
	if got := aws.ToString(f.assumeIn.RoleArn); got != want {
		t.Fatalf("RoleArn = %q, want %q", got, want)
	}
}

// Role chaining caps a session at one hour. Asking for more is a hard STS error,
// not a silent clamp.
func TestMintCredentials_NeverExceedsTheRoleChainingCap(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := aws.ToInt32(f.assumeIn.DurationSeconds); d > 3600 {
		t.Fatalf("DurationSeconds = %d, exceeds the 3600s role-chaining cap", d)
	}
}

// Without the session policy the "narrowed" credentials are the instance role in
// full and the fence is decorative.
func TestMintCredentials_AttachesTheSessionPolicy(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	pol := aws.ToString(f.assumeIn.Policy)
	if pol == "" {
		t.Fatal("no session policy attached: the credentials would be the full instance role")
	}
	for _, want := range []string{"alias/km-sandbox-secrets", "sandboxes/abc123/secrets.enc.yaml", `"Deny"`} {
		if !strings.Contains(pol, want) {
			t.Errorf("session policy missing %q:\n%s", want, pol)
		}
	}
}

// A live credential is reused rather than re-minted on every `aws` invocation:
// the pollers shell out in loops, and a fresh AssumeRole per call is a
// self-inflicted STS throttle.
func TestMintCredentials_ReusesALiveCredential(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour}
	s := fencedServer(f)
	for i := 0; i < 3; i++ {
		if _, err := s.mintCredentials(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.assumeCall != 1 {
		t.Fatalf("AssumeRole called %d times, want 1 (the cache did not hold)", f.assumeCall)
	}
}

// ...and is NOT reused once close enough to expiry that a consumer could still
// be holding it when it dies.
func TestMintCredentials_RefreshesNearExpiry(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: 2 * time.Minute}
	s := fencedServer(f)
	for i := 0; i < 2; i++ {
		if _, err := s.mintCredentials(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.assumeCall != 2 {
		t.Fatalf("AssumeRole called %d times, want 2 (a stale credential was reused)", f.assumeCall)
	}
}

// Fence off means no credentials at all — never a silent fall-through handing
// out un-narrowed ones on a box whose IAM was never provisioned for the assume.
func TestMintCredentials_RefusedWhenFenceOff(t *testing.T) {
	s := fencedServer(&fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour})
	s.FenceEnabled = false
	if _, err := s.mintCredentials(context.Background()); err == nil {
		t.Fatal("mintCredentials succeeded with the fence off")
	}
}

// A misparse would make the broker assume some OTHER role, so every unexpected
// shape is an error rather than a best guess.
func TestMintCredentials_RejectsAnUnexpectedCallerARN(t *testing.T) {
	for _, arn := range []string{"", "arn:aws:iam::052251888500:user/someone", "garbage",
		"arn:aws:sts::052251888500:federated-user/bob"} {
		f := &fakeSTS{callerArn: arn, expiresIn: time.Hour}
		if _, err := fencedServer(f).mintCredentials(context.Background()); err == nil {
			t.Errorf("caller arn %q was accepted", arn)
		}
	}
}

func TestMintCredentials_ReturnsTheCredentialProcessShape(t *testing.T) {
	f := &fakeSTS{callerArn: testCallerARN, expiresIn: time.Hour}
	got, err := fencedServer(f).mintCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.AccessKeyID != "AKIATEST" || got.SessionToken != "tok" {
		t.Errorf("credentials not carried through: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.Expiration); err != nil {
		t.Errorf("Expiration %q is not RFC3339: %v", got.Expiration, err)
	}
}
