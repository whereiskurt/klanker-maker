package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func TestAccessParamPaths(t *testing.T) {
	if got, want := kmaws.AccessParamPrefix("km2"), "/km2/access/"; got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
	if got, want := kmaws.SSHKeyPath("km", "learn-abc"), "/km/access/learn-abc/ssh-key"; got != want {
		t.Errorf("ssh path = %q, want %q", got, want)
	}
	if got, want := kmaws.DesktopCredPath("km", "learn-abc"), "/km/access/learn-abc/desktop-cred"; got != want {
		t.Errorf("desktop path = %q, want %q", got, want)
	}
	// The sandbox instance role reads /{prefix}/sandbox/{id}/*; the access
	// namespace must never be under it.
	for _, p := range []string{kmaws.SSHKeyPath("km", "x"), kmaws.DesktopCredPath("km", "x")} {
		if strings.HasPrefix(p, "/km/sandbox/") {
			t.Errorf("access path must not live under /{prefix}/sandbox/: %q", p)
		}
	}
}

func TestGetAccessParam_NotFoundIsAbsentNotError(t *testing.T) {
	m := &mockIdentitySSMAPI{getParameterErr: &ssmtypes.ParameterNotFound{}}
	v, found, err := kmaws.GetAccessParam(context.Background(), m, "/km/access/x/ssh-key")
	if err != nil || found || v != "" {
		t.Errorf("got (%q,%v,%v), want (\"\",false,nil)", v, found, err)
	}
	if !awssdk.ToBool(m.getParameterInput.WithDecryption) {
		t.Error("GetAccessParam must request WithDecryption=true")
	}
}

func TestGetAccessParam_Present(t *testing.T) {
	m := &mockIdentitySSMAPI{getParameterValue: "kasm:hunter2"}
	v, found, err := kmaws.GetAccessParam(context.Background(), m, "/km/access/x/desktop-cred")
	if err != nil || !found || v != "kasm:hunter2" {
		t.Errorf("got (%q,%v,%v)", v, found, err)
	}
}

func TestGetAccessParam_OtherErrorPropagates(t *testing.T) {
	boom := errors.New("throttled")
	m := &mockIdentitySSMAPI{getParameterErr: boom}
	_, _, err := kmaws.GetAccessParam(context.Background(), m, "/km/access/x/ssh-key")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wraps %v", err, boom)
	}
}

func TestPutAccessParam_SecureStringOverwriteWithKey(t *testing.T) {
	m := &mockIdentitySSMAPI{}
	if err := kmaws.PutAccessParam(context.Background(), m, "/km/access/x/ssh-key", "PEM", "alias/km-platform-km-use1"); err != nil {
		t.Fatal(err)
	}
	in := m.putParameterInput
	if awssdk.ToString(in.Name) != "/km/access/x/ssh-key" || awssdk.ToString(in.Value) != "PEM" {
		t.Errorf("name/value = %q/%q", awssdk.ToString(in.Name), awssdk.ToString(in.Value))
	}
	if in.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("type = %v, want SecureString", in.Type)
	}
	if !awssdk.ToBool(in.Overwrite) {
		t.Error("Overwrite must be true — last rekey wins")
	}
	if awssdk.ToString(in.KeyId) != "alias/km-platform-km-use1" {
		t.Errorf("KeyId = %q", awssdk.ToString(in.KeyId))
	}
}

func TestPutAccessParam_EmptyKeyIDOmitsKeyId(t *testing.T) {
	m := &mockIdentitySSMAPI{}
	if err := kmaws.PutAccessParam(context.Background(), m, "/km/access/x/ssh-key", "PEM", ""); err != nil {
		t.Fatal(err)
	}
	if m.putParameterInput.KeyId != nil {
		t.Error("KeyId must be nil when no key is given (SSM default key)")
	}
}
