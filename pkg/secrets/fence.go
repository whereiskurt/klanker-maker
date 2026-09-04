package secrets

import (
	"encoding/json"
	"fmt"
)

// SessionPolicy is the inline policy km-secretsd attaches when it self-assumes
// the instance role to mint credentials for a fenced sandbox (design doc §4.4).
//
// The result is the instance role MINUS exactly two things: the ability to
// decrypt the secrets bundle's KMS key, and the ability to fetch the bundle
// object from S3. Everything else the instance role can do, these credentials
// can still do — which is what keeps km-github, km-slack, km-h1 and the git
// credential helpers working behind the fence.
//
// Self-assume rather than a parallel role is the load-bearing choice: the
// narrowed credentials are DEFINITIONALLY the instance role minus two Denies, so
// the two can never drift. A parallel role would need the instance role's whole
// policy set duplicated and kept in sync forever — the drift class
// TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant exists to prevent elsewhere.
//
// Both Denies are conditioned or exact, never blanket:
//
//   - kms:Decrypt is denied only where kms:ResourceAliases names this install's
//     sandbox-secrets alias. The separate grant that lets helpers read SSM
//     SecureStrings targets a different key and is conditioned on
//     kms:ViaService=ssm, so it matches neither condition and keeps working.
//   - s3:GetObject is denied on this sandbox's own bundle object, not a prefix.
//
// The Allow is not decoration. A session policy INTERSECTS with the role's
// identity policies, so a document carrying only Denies would grant nothing at
// all and every helper would break the moment the fence came up.
func SessionPolicy(resourcePrefix, artifactsBucket, sandboxID string) (string, error) {
	// An empty component would interpolate into a Deny that matches nothing — a
	// fence that reports success and does not fence. Refuse instead of guessing.
	switch {
	case resourcePrefix == "":
		return "", fmt.Errorf("secrets: session policy needs a resource prefix")
	case artifactsBucket == "":
		return "", fmt.Errorf("secrets: session policy needs an artifacts bucket")
	case sandboxID == "":
		return "", fmt.Errorf("secrets: session policy needs a sandbox id")
	}

	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{
				"Sid":      "InheritTheInstanceRole",
				"Effect":   "Allow",
				"Action":   "*",
				"Resource": "*",
			},
			map[string]any{
				"Sid":      "DenySecretsBundleKMS",
				"Effect":   "Deny",
				"Action":   "kms:Decrypt",
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]any{
						"kms:ResourceAliases": "alias/" + resourcePrefix + "-sandbox-secrets",
					},
				},
			},
			map[string]any{
				"Sid":    "DenySecretsBundleObject",
				"Effect": "Deny",
				"Action": "s3:GetObject",
				"Resource": fmt.Sprintf("arn:aws:s3:::%s/sandboxes/%s/secrets.enc.yaml",
					artifactsBucket, sandboxID),
			},
		},
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("secrets: marshal session policy: %w", err)
	}
	return string(b), nil
}
