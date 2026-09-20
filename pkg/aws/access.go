package aws

import (
	"context"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Shared sandbox access credentials.
//
// One SSH private key and one KasmVNC password per sandbox, shared by every
// operator of the install, stored as SecureStrings under
// /{prefix}/access/{sandbox-id}/. SSM is the source of truth; the files under
// ~/.km/keys and ~/.km/desktop are a per-laptop cache reconciled on every
// `km vscode|desktop|herdr|tunnel start`.
//
// The namespace is deliberately NOT /{prefix}/sandbox/{id}/: the instance role
// is granted ssm:GetParameter* on that whole path, and the box has no business
// holding its own operator private key. /{prefix}/access/ sits inside the
// operator policy's parameter/{prefix}/* grant, so laptops need no IAM change.
// See docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md.

const (
	AccessKindSSHKey      = "ssh-key"      // OpenSSH private-key PEM; the public key is derived from it
	AccessKindDesktopCred = "desktop-cred" // "user:pass", byte-identical to ~/.km/desktop/<id>
)

// AccessParamPrefix is the SSM path every shared access credential lives under.
func AccessParamPrefix(resourcePrefix string) string {
	return fmt.Sprintf("/%s/access/", resourcePrefix)
}

// AccessParamPath is the parameter for one credential kind of one sandbox.
func AccessParamPath(resourcePrefix, sandboxID, kind string) string {
	return AccessParamPrefix(resourcePrefix) + sandboxID + "/" + kind
}

// SSHKeyPath is the shared VS Code / herdr / tunnel ed25519 private key.
func SSHKeyPath(resourcePrefix, sandboxID string) string {
	return AccessParamPath(resourcePrefix, sandboxID, AccessKindSSHKey)
}

// DesktopCredPath is the shared KasmVNC "user:pass".
func DesktopCredPath(resourcePrefix, sandboxID string) string {
	return AccessParamPath(resourcePrefix, sandboxID, AccessKindDesktopCred)
}

// GetAccessParam reads one shared credential. ParameterNotFound is reported as
// (found=false, err=nil) because "nobody has published yet" is an ordinary
// state, not a failure; every other error is returned so the caller can
// decide whether to fail open on it.
func GetAccessParam(ctx context.Context, client IdentitySSMAPI, path string) (string, bool, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(path),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		var nf *ssmtypes.ParameterNotFound
		if errors.As(err, &nf) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get %s: %w", path, err)
	}
	if out == nil || out.Parameter == nil || out.Parameter.Value == nil {
		return "", false, nil
	}
	return *out.Parameter.Value, true, nil
}

// PutAccessParam writes one shared credential, overwriting whatever was there:
// the last rekey wins, by design (no version CAS). kmsKeyID may be a key id,
// ARN or alias name; empty means the SSM default key.
func PutAccessParam(ctx context.Context, client IdentitySSMAPI, path, value, kmsKeyID string) error {
	in := &ssm.PutParameterInput{
		Name:      awssdk.String(path),
		Value:     awssdk.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: awssdk.Bool(true),
	}
	if kmsKeyID != "" {
		in.KeyId = awssdk.String(kmsKeyID)
	}
	if _, err := client.PutParameter(ctx, in); err != nil {
		return fmt.Errorf("put %s: %w", path, err)
	}
	return nil
}
