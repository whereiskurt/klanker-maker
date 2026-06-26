// github_review_secrets_test.go — Phase 98-04 validation test for github-review.yaml
// SOPS cold-box auth (GH-COLD-CREATE defect 4).
//
// TestGitHubReviewProfileSecrets verifies that profiles/github-review.yaml:
//  1. Declares spec.secrets.sopsFile (ANTHROPIC_API_KEY injection for cold boxes).
//  2. Sets spec.execution.useBedrock = false (cold box authenticates via SOPS creds,
//     NOT Bedrock — aligns with the bridge's cold-create path).
//  3. Still passes km validate (no schema regression from adding the secrets field).
package profile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestGitHubReviewProfileSecrets validates that profiles/github-review.yaml
// carries spec.secrets.sopsFile and useBedrock:false (GH-COLD-CREATE auth).
func TestGitHubReviewProfileSecrets(t *testing.T) {
	// Locate the repository root by walking up from the test file's directory.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate test file path")
	}
	// thisFile is .../pkg/profile/github_review_secrets_test.go
	// repo root is two directories up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	profilePath := filepath.Join(repoRoot, "testdata", "profiles", "github-review.yaml")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profiles/github-review.yaml: %v", err)
	}

	// ── Parse ─────────────────────────────────────────────────────────────────
	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("Parse(github-review.yaml): %v", err)
	}

	// ── Assert spec.secrets.sopsFile is set ───────────────────────────────────
	if p.Spec.Secrets == nil {
		t.Fatal("spec.secrets is nil; want spec.secrets.sopsFile set for cold-box Claude auth (GH-COLD-CREATE)")
	}
	if p.Spec.Secrets.SopsFile == "" {
		t.Error("spec.secrets.sopsFile is empty; want a non-empty sopsFile path for cold-box Claude auth")
	}

	// ── Assert spec.execution.useBedrock is false ─────────────────────────────
	if p.Spec.Execution.UseBedrock {
		t.Error("spec.execution.useBedrock must be false; cold-box authenticates via SOPS Claude creds, not Bedrock")
	}

	// ── Validate the whole profile ────────────────────────────────────────────
	errs := profile.Validate(data)
	if len(errs) > 0 {
		for _, ve := range errs {
			t.Errorf("validation error: %s", ve.Message)
		}
		t.Fatalf("profiles/github-review.yaml validation failed after adding spec.secrets (regression check)")
	}
}
