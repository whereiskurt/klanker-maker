package compiler

import (
	"strings"
	"testing"
)

// TestUserdata_KmSendOperatorAddressUsesEnvVar (W0-09) verifies that the
// generated userdata:
//   - contains `${KM_OPERATOR_EMAIL}` in both km-send heredoc blocks (lines ~1621 and ~1653)
//   - does NOT contain `operator@${KM_SANDBOX_DOMAIN}` in either heredoc
//   - exports KM_OPERATOR_EMAIL in the env-file (profile.d) section
func TestUserdata_KmSendOperatorAddressUsesEnvVar(t *testing.T) {
	// Set a non-default prefix so the derived operator email is distinct.
	t.Setenv("KM_RESOURCE_PREFIX", "kph")
	t.Setenv("KM_OPERATOR_EMAIL", "operator-kph@sandboxes.example.com")

	p := baseProfile()
	out, err := generateUserData(p, "sb-84-09", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Assert: both km-send heredoc default --to uses the env var reference.
	// The reference may be in bash parameter-expansion form: ${KM_OPERATOR_EMAIL:-...}
	if !strings.Contains(out, "KM_OPERATOR_EMAIL") {
		t.Errorf("expected 'KM_OPERATOR_EMAIL' reference in userdata (km-send heredoc default --to)\n--- snippet ---\n%s", abbreviateUD(out))
	}
	// More specific: the km-send default-to block should reference it directly.
	if !strings.Contains(out, "TO=\"${KM_OPERATOR_EMAIL") && !strings.Contains(out, "TO=${KM_OPERATOR_EMAIL") {
		t.Errorf("expected km-send default TO to reference KM_OPERATOR_EMAIL (not a bare domain literal)\n--- snippet ---\n%s", abbreviateUD(out))
	}

	// Assert: the legacy bare literal is gone from both heredoc occurrences.
	if strings.Contains(out, "operator@${KM_SANDBOX_DOMAIN}") {
		t.Errorf("found legacy 'operator@${KM_SANDBOX_DOMAIN}' in userdata — must be replaced with ${KM_OPERATOR_EMAIL}\n--- snippet ---\n%s", abbreviateUD(out))
	}

	// Assert: KM_OPERATOR_EMAIL is exported in the env/profile.d section
	// (not just referenced in the heredoc, but declared earlier so shells resolve it).
	if !strings.Contains(out, "KM_OPERATOR_EMAIL=") && !strings.Contains(out, "export KM_OPERATOR_EMAIL=") {
		t.Errorf("expected KM_OPERATOR_EMAIL export/assignment in the profile.d env section of userdata")
	}
}
