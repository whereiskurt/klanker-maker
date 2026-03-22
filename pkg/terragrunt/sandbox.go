package terragrunt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CreateSandboxDir creates a new per-sandbox directory under
// <repoRoot>/infra/live/sandboxes/<sandboxID>/ and copies the
// _template/terragrunt.hcl file into it. Returns the absolute path to the
// newly created directory.
func CreateSandboxDir(repoRoot, sandboxID string) (string, error) {
	templateDir := filepath.Join(repoRoot, "infra", "live", "sandboxes", "_template")
	sandboxDir := filepath.Join(repoRoot, "infra", "live", "sandboxes", sandboxID)

	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return "", fmt.Errorf("create sandbox directory %s: %w", sandboxDir, err)
	}

	// Copy terragrunt.hcl from the template into the new sandbox directory.
	src := filepath.Join(templateDir, "terragrunt.hcl")
	dst := filepath.Join(sandboxDir, "terragrunt.hcl")
	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy template terragrunt.hcl: %w", err)
	}

	return sandboxDir, nil
}

// PopulateSandboxDir writes the profile-compiled service.hcl into sandboxDir,
// and optionally writes user-data.sh if userData is non-empty.
func PopulateSandboxDir(sandboxDir, serviceHCL, userData string) error {
	// Write service.hcl — always required.
	if err := os.WriteFile(filepath.Join(sandboxDir, "service.hcl"), []byte(serviceHCL), 0o644); err != nil {
		return fmt.Errorf("write service.hcl: %w", err)
	}

	// Write user-data.sh — only when content is provided.
	if userData != "" {
		if err := os.WriteFile(filepath.Join(sandboxDir, "user-data.sh"), []byte(userData), 0o755); err != nil {
			return fmt.Errorf("write user-data.sh: %w", err)
		}
	}

	return nil
}

// CleanupSandboxDir removes the sandbox directory and all its contents.
func CleanupSandboxDir(sandboxDir string) error {
	if err := os.RemoveAll(sandboxDir); err != nil {
		return fmt.Errorf("remove sandbox directory %s: %w", sandboxDir, err)
	}
	return nil
}

// copyFile copies a single file from src to dst, preserving content.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination file %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy file contents: %w", err)
	}

	return out.Close()
}
