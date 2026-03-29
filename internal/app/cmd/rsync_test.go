package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// makeProfile builds a minimal SandboxProfile with the given rsyncPaths and rsyncFileList.
func makeProfile(paths []string, fileList string) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha1",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Execution: profile.ExecutionSpec{
				RsyncPaths:    paths,
				RsyncFileList: fileList,
			},
		},
	}
}

// TestResolveRsyncPaths covers all documented resolution scenarios.
func TestResolveRsyncPaths(t *testing.T) {
	globalFallback := []string{".bashrc", ".gitconfig"}

	t.Run("profile rsyncPaths returned instead of global", func(t *testing.T) {
		prof := makeProfile([]string{".claude", "projects"}, "")
		paths, err := resolveRsyncPaths(prof, ".", globalFallback)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(paths) != 2 || paths[0] != ".claude" || paths[1] != "projects" {
			t.Errorf("got %v, want [.claude projects]", paths)
		}
	})

	t.Run("profile without rsyncPaths falls back to global", func(t *testing.T) {
		prof := makeProfile(nil, "")
		paths, err := resolveRsyncPaths(prof, ".", globalFallback)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(paths) != 2 || paths[0] != ".bashrc" || paths[1] != ".gitconfig" {
			t.Errorf("got %v, want %v", paths, globalFallback)
		}
	})

	t.Run("nil profile falls back to global", func(t *testing.T) {
		paths, err := resolveRsyncPaths(nil, ".", globalFallback)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(paths) != 2 || paths[0] != ".bashrc" || paths[1] != ".gitconfig" {
			t.Errorf("got %v, want %v", paths, globalFallback)
		}
	})

	t.Run("profile with rsyncPaths and rsyncFileList merges and deduplicates", func(t *testing.T) {
		// Write a temp file list YAML
		tmpDir := t.TempDir()
		fileListPath := filepath.Join(tmpDir, "extra.yaml")
		fileListContent := "paths:\n  - .claude\n  - .ssh\n"
		if err := os.WriteFile(fileListPath, []byte(fileListContent), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}

		prof := makeProfile([]string{".claude", ".bashrc"}, fileListPath)
		paths, err := resolveRsyncPaths(prof, tmpDir, globalFallback)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Expected: .claude, .bashrc from rsyncPaths + .ssh from fileList (.claude deduplicated)
		found := map[string]bool{}
		for _, p := range paths {
			found[p] = true
		}
		if !found[".claude"] || !found[".bashrc"] || !found[".ssh"] {
			t.Errorf("missing expected paths; got %v", paths)
		}
		// Ensure no duplicates
		seen := map[string]int{}
		for _, p := range paths {
			seen[p]++
		}
		for p, count := range seen {
			if count > 1 {
				t.Errorf("duplicate path %q in result", p)
			}
		}
	})

	t.Run("rsyncFileList not found returns error", func(t *testing.T) {
		prof := makeProfile([]string{".claude"}, "/nonexistent/path/extra.yaml")
		_, err := resolveRsyncPaths(prof, ".", globalFallback)
		if err == nil {
			t.Error("expected error for missing file list, got nil")
		}
	})
}

// TestLoadFileList covers parsing of external YAML file lists.
func TestLoadFileList(t *testing.T) {
	t.Run("parses paths array correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		f, err := os.CreateTemp(tmpDir, "rsync-*.yaml")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		_, _ = f.WriteString("paths:\n  - .claude\n  - projects/*/config\n  - .gitconfig\n")
		f.Close()

		paths, err := loadFileList(f.Name())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(paths) != 3 {
			t.Errorf("got %d paths, want 3: %v", len(paths), paths)
		}
		if paths[0] != ".claude" || paths[1] != "projects/*/config" || paths[2] != ".gitconfig" {
			t.Errorf("unexpected paths: %v", paths)
		}
	})

	t.Run("returns error on invalid YAML", func(t *testing.T) {
		tmpDir := t.TempDir()
		f, err := os.CreateTemp(tmpDir, "bad-*.yaml")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		_, _ = f.WriteString("paths: [unclosed\n")
		f.Close()

		_, err = loadFileList(f.Name())
		if err == nil {
			t.Error("expected error for invalid YAML, got nil")
		}
	})
}

// TestRsyncSaveCmd covers the tar shell command format produced by buildTarShellCmd.
// These tests assert RSYNC-06: paths are passed unquoted so bash expands wildcards.
func TestRsyncSaveCmd(t *testing.T) {
	const bucket = "my-artifacts-bucket"
	const s3Key = "rsync/test-snapshot.tar.gz"

	t.Run("literal paths appear unquoted in for-loop", func(t *testing.T) {
		cmd := buildTarShellCmd([]string{".claude", ".bashrc"}, bucket, s3Key)
		want := "for p in .claude .bashrc;"
		if !strings.Contains(cmd, want) {
			t.Errorf("expected command to contain %q\ngot: %s", want, cmd)
		}
	})

	t.Run("wildcard path appears unquoted in for-loop", func(t *testing.T) {
		cmd := buildTarShellCmd([]string{"projects/*/config", ".claude"}, bucket, s3Key)
		want := "for p in projects/*/config .claude;"
		if !strings.Contains(cmd, want) {
			t.Errorf("expected command to contain %q\ngot: %s", want, cmd)
		}
		// Ensure paths are NOT single-quoted (which would prevent glob expansion)
		if strings.Contains(cmd, "'projects/*/config'") {
			t.Errorf("wildcard path must not be single-quoted; got: %s", cmd)
		}
	})

	t.Run("command contains tar and aws s3 cp with correct bucket and key", func(t *testing.T) {
		cmd := buildTarShellCmd([]string{".gitconfig"}, bucket, s3Key)
		if !strings.Contains(cmd, "tar czf /tmp/km-rsync.tar.gz") {
			t.Errorf("expected tar command not found in: %s", cmd)
		}
		wantS3 := fmt.Sprintf(`aws s3 cp /tmp/km-rsync.tar.gz "s3://%s/%s"`, bucket, s3Key)
		if !strings.Contains(cmd, wantS3) {
			t.Errorf("expected s3 cp %q not found in: %s", wantS3, cmd)
		}
	})

	t.Run("single path works correctly without trailing space", func(t *testing.T) {
		cmd := buildTarShellCmd([]string{".bashrc"}, bucket, s3Key)
		want := "for p in .bashrc;"
		if !strings.Contains(cmd, want) {
			t.Errorf("expected command to contain %q\ngot: %s", want, cmd)
		}
		// The for-loop argument should not have a leading or trailing space before semicolon
		if strings.Contains(cmd, "for p in .bashrc ;") {
			t.Errorf("unexpected trailing space before semicolon in: %s", cmd)
		}
	})
}

// TestValidateRsyncPath covers path validation rules.
func TestValidateRsyncPath(t *testing.T) {
	valid := []string{
		".claude",
		"projects/*/config",
		".bash_profile",
		".gitconfig",
		"work/my-project",
		"data/[0-9]*",
		"config?.yaml",
	}
	for _, p := range valid {
		t.Run("valid: "+p, func(t *testing.T) {
			if err := validateRsyncPath(p); err != nil {
				t.Errorf("expected valid path %q to pass, got: %v", p, err)
			}
		})
	}

	invalid := []string{
		"$(rm -rf ~)",
		"path;evil",
		"path|pipe",
		"path with spaces",
		"`whoami`",
		"path&bg",
		"path>out",
		"path<in",
	}
	for _, p := range invalid {
		t.Run("invalid: "+p, func(t *testing.T) {
			if err := validateRsyncPath(p); err == nil {
				t.Errorf("expected path %q to be rejected, but it passed", p)
			}
		})
	}
}
