package profile

import (
	"fmt"
	"regexp"
	"strings"
)

// SecretPathPrefixToken is the ONLY token recognised in
// spec.iam.allowedSecretPaths. It expands to "/" + the install's
// resource_prefix at compile time (see InterpolateSecretPaths).
const SecretPathPrefixToken = "{{prefix}}"

// secretPathTokenRe matches any {{...}} token so unknown ones can be rejected
// rather than silently passed through into an IAM policy.
var secretPathTokenRe = regexp.MustCompile(`\{\{[^}]*\}\}`)

// ValidateSecretPaths enforces that every allowedSecretPaths entry is
// prefix-relative.
//
// This is a SECURITY GUARD, not a style rule. These paths are compiled into the
// sandbox role's ssm:GetParameter grant. Requiring the {{prefix}} token as the
// leading segment makes it structurally impossible for a profile to grant the
// sandbox read access to SSM parameters outside its own install's namespace —
// an absolute path such as "/*" or "/other-install/..." cannot be expressed.
//
// Validation is shape-only: it deliberately does NOT need to know the prefix's
// value, so km validate works on a workstation with no configured install.
func ValidateSecretPaths(paths []string) []ValidationError {
	var errs []ValidationError

	for _, p := range paths {
		// Reject any token that is not exactly SecretPathPrefixToken, wherever
		// it appears. Checked first so "{{prefix2}}/x" reports the precise
		// cause rather than the generic leading-segment error.
		for _, tok := range secretPathTokenRe.FindAllString(p, -1) {
			if tok != SecretPathPrefixToken {
				errs = append(errs, ValidationError{
					Path: "spec.iam.allowedSecretPaths",
					Message: fmt.Sprintf(
						"%q contains unknown token %q — the only supported token is %q",
						p, tok, SecretPathPrefixToken),
				})
			}
		}

		if !strings.HasPrefix(p, SecretPathPrefixToken+"/") {
			errs = append(errs, ValidationError{
				Path: "spec.iam.allowedSecretPaths",
				Message: fmt.Sprintf(
					"%q must start with %q/ — paths are compiled into the sandbox role's "+
						"ssm:GetParameter grant, and requiring the token keeps that grant "+
						"inside this install's own namespace",
					p, SecretPathPrefixToken),
			})
		}
	}

	return errs
}

// InterpolateSecretPaths expands SecretPathPrefixToken to "/" + resourcePrefix.
//
// "{{prefix}}/wiz/wiz-api-client-id" with prefix "km" becomes
// "/km/wiz/wiz-api-client-id".
//
// FAIL LOUD: when any path carries the token and resourcePrefix is empty, this
// returns an error rather than defaulting. Other call sites in this codebase
// default an empty KM_RESOURCE_PREFIX to "km"; doing that here would silently
// render an IAM policy pointing at a DIFFERENT install's SSM namespace on any
// non-default install. An error at compile time is the only safe outcome.
func InterpolateSecretPaths(paths []string, resourcePrefix string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !strings.Contains(p, SecretPathPrefixToken) {
			// Should be unreachable: ValidateSecretPaths rejects these. Kept as
			// a defence in depth so an un-validated path can never reach IAM.
			return nil, fmt.Errorf(
				"allowedSecretPaths entry %q does not start with %s — refusing to compile",
				p, SecretPathPrefixToken)
		}
		if resourcePrefix == "" {
			return nil, fmt.Errorf(
				"allowedSecretPaths entry %q needs the install's resource prefix, but "+
					"KM_RESOURCE_PREFIX is unset — refusing to default to \"km\", which "+
					"would grant the sandbox role access to another install's SSM namespace",
				p)
		}
		out = append(out, strings.Replace(p, SecretPathPrefixToken, "/"+resourcePrefix, 1))
	}
	return out, nil
}
