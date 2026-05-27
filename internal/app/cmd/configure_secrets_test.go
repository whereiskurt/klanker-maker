package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// TestEnsureSecretsGitignore exercises the ensureSecretsGitignore helper using
// tempdir round-trips. No real filesystem state is mutated outside the tempdir.
//
// Phase 89-03: SOPS-19-CONFIGURE-GITIGNORE
func TestEnsureSecretsGitignore(t *testing.T) {
	const (
		line1   = "/secrets/*"
		line2   = "!/secrets/*.enc.yaml"
		comment = "# Phase 89: SOPS-encrypted secrets (km configure)"
	)

	t.Run("EmptyGitignore", func(t *testing.T) {
		repoRoot := t.TempDir()

		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		gitignorePath := filepath.Join(repoRoot, ".gitignore")
		content, err := os.ReadFile(gitignorePath)
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}

		got := string(content)
		if !strings.Contains(got, comment) {
			t.Errorf("expected comment header %q in .gitignore, got:\n%s", comment, got)
		}
		if !strings.Contains(got, line1) {
			t.Errorf("expected line %q in .gitignore, got:\n%s", line1, got)
		}
		if !strings.Contains(got, line2) {
			t.Errorf("expected line %q in .gitignore, got:\n%s", line2, got)
		}
	})

	t.Run("IdempotentReRun", func(t *testing.T) {
		repoRoot := t.TempDir()

		// First call
		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("first call: %v", err)
		}
		first, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
		if err != nil {
			t.Fatalf("read after first call: %v", err)
		}

		// Second call — must be a no-op
		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("second call: %v", err)
		}
		second, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
		if err != nil {
			t.Fatalf("read after second call: %v", err)
		}

		if string(first) != string(second) {
			t.Errorf("second call mutated .gitignore (not idempotent)\nbefore:\n%s\nafter:\n%s",
				string(first), string(second))
		}
	})

	t.Run("BothLinesAlreadyPresent", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Pre-write a .gitignore with both required lines plus unrelated content.
		initial := "# existing content\n/node_modules\n" + line1 + "\n" + line2 + "\n# end\n"
		if err := os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte(initial), 0644); err != nil {
			t.Fatalf("write initial .gitignore: %v", err)
		}

		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}

		if string(content) != initial {
			t.Errorf("expected no change when both lines present\nbefore:\n%s\nafter:\n%s",
				initial, string(content))
		}
	})

	t.Run("FirstLinePresentSecondMissing", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Pre-write only the first required line.
		initial := "# existing\n" + line1 + "\n"
		if err := os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte(initial), 0644); err != nil {
			t.Fatalf("write initial .gitignore: %v", err)
		}

		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}

		got := string(content)

		// line2 must now be present.
		if !strings.Contains(got, line2) {
			t.Errorf("expected %q to be appended, got:\n%s", line2, got)
		}

		// line1 must appear exactly once (no duplicate).
		// Use line-anchored count to avoid counting "/secrets/*" inside "!/secrets/*.enc.yaml".
		lineCount := 0
		for _, l := range strings.Split(got, "\n") {
			if strings.TrimSpace(l) == line1 {
				lineCount++
			}
		}
		if lineCount != 1 {
			t.Errorf("expected exactly 1 line equal to %q, got %d in:\n%s", line1, lineCount, got)
		}
	})

	t.Run("PartialMatchAvoidsFalseHit", func(t *testing.T) {
		// Substring-pitfall test: a line like "unrelated/secrets/*foo" must NOT
		// satisfy the requirement for "/secrets/*". We use line-anchored matching
		// (exact line equality after TrimSpace), not strings.Contains(body, line).
		repoRoot := t.TempDir()

		// Pre-write lines that contain the target strings as substrings but are
		// NOT equal to the required lines.
		initial := "# unrelated\nunrelated/secrets/*foo\n!/other/secrets/*.enc.yaml\n"
		if err := os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte(initial), 0644); err != nil {
			t.Fatalf("write initial .gitignore: %v", err)
		}

		if err := cmd.EnsureSecretsGitignore(repoRoot); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}

		got := string(content)

		// Both required lines must now be present (the partial matches above
		// should NOT have satisfied the requirement).
		if !strings.Contains(got, line1+"\n") && !strings.HasSuffix(got, line1) {
			// Check exact line presence by splitting
			lines := strings.Split(got, "\n")
			found := false
			for _, l := range lines {
				if strings.TrimSpace(l) == line1 {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected exact line %q to be appended (partial substring should not satisfy), got:\n%s", line1, got)
			}
		}
		if !strings.Contains(got, line2+"\n") && !strings.HasSuffix(got, line2) {
			lines := strings.Split(got, "\n")
			found := false
			for _, l := range lines {
				if strings.TrimSpace(l) == line2 {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected exact line %q to be appended (partial substring should not satisfy), got:\n%s", line2, got)
			}
		}
	})
}
