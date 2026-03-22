package terragrunt_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// Helper: create a fake sandbox directory that looks like a real one
// (has a service.hcl so the runner knows it's a sandbox dir).
func makeFakeSandboxDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "service.hcl"), []byte(`locals { sandbox_id = "sb-test1234" }`), 0o644); err != nil {
		t.Fatalf("failed to write service.hcl: %v", err)
	}
	return dir
}

// Helper: create a fake repo root with _template directory and terragrunt.hcl.
func makeFakeRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	templateDir := filepath.Join(root, "infra", "live", "_template")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("failed to create template dir: %v", err)
	}
	content := `locals { sandbox_id = "SANDBOX_ID_PLACEHOLDER" }`
	if err := os.WriteFile(filepath.Join(templateDir, "terragrunt.hcl"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write terragrunt.hcl: %v", err)
	}
	return root
}

// ---- Sandbox lifecycle tests ----

func TestCreateSandboxDir(t *testing.T) {
	repoRoot := makeFakeRepoRoot(t)
	sandboxID := "sb-a1b2c3d4"

	sandboxDir, err := terragrunt.CreateSandboxDir(repoRoot, "use1", sandboxID)
	if err != nil {
		t.Fatalf("CreateSandboxDir failed: %v", err)
	}

	// Directory should exist
	if _, err := os.Stat(sandboxDir); os.IsNotExist(err) {
		t.Fatalf("sandbox directory not created: %s", sandboxDir)
	}

	// Expected path
	expected := filepath.Join(repoRoot, "infra", "live", "use1", "sandboxes", sandboxID)
	if sandboxDir != expected {
		t.Errorf("sandbox dir = %q, want %q", sandboxDir, expected)
	}

	// terragrunt.hcl should have been copied
	tgFile := filepath.Join(sandboxDir, "terragrunt.hcl")
	if _, err := os.Stat(tgFile); os.IsNotExist(err) {
		t.Fatalf("terragrunt.hcl not copied to sandbox directory")
	}

	// Content should match the template
	content, err := os.ReadFile(tgFile)
	if err != nil {
		t.Fatalf("failed to read copied terragrunt.hcl: %v", err)
	}
	if len(content) == 0 {
		t.Error("copied terragrunt.hcl is empty")
	}
}

func TestPopulateSandboxDir(t *testing.T) {
	sandboxDir := t.TempDir()
	serviceHCL := `locals { sandbox_id = "sb-a1b2c3d4" }`
	userData := `#!/bin/bash
echo "hello from user-data"`

	if err := terragrunt.PopulateSandboxDir(sandboxDir, serviceHCL, userData); err != nil {
		t.Fatalf("PopulateSandboxDir failed: %v", err)
	}

	// service.hcl must exist and match
	got, err := os.ReadFile(filepath.Join(sandboxDir, "service.hcl"))
	if err != nil {
		t.Fatalf("service.hcl not written: %v", err)
	}
	if string(got) != serviceHCL {
		t.Errorf("service.hcl content mismatch: got %q, want %q", string(got), serviceHCL)
	}

	// user-data.sh must exist and match
	ud, err := os.ReadFile(filepath.Join(sandboxDir, "user-data.sh"))
	if err != nil {
		t.Fatalf("user-data.sh not written: %v", err)
	}
	if string(ud) != userData {
		t.Errorf("user-data.sh content mismatch: got %q, want %q", string(ud), userData)
	}
}

func TestPopulateSandboxDir_NoUserData(t *testing.T) {
	sandboxDir := t.TempDir()
	serviceHCL := `locals { sandbox_id = "sb-a1b2c3d4" }`

	if err := terragrunt.PopulateSandboxDir(sandboxDir, serviceHCL, ""); err != nil {
		t.Fatalf("PopulateSandboxDir (no user-data) failed: %v", err)
	}

	// service.hcl must exist
	if _, err := os.Stat(filepath.Join(sandboxDir, "service.hcl")); os.IsNotExist(err) {
		t.Fatal("service.hcl not written")
	}

	// user-data.sh must NOT exist when userData is empty
	if _, err := os.Stat(filepath.Join(sandboxDir, "user-data.sh")); err == nil {
		t.Error("user-data.sh should not be written when userData is empty")
	}
}

func TestCleanupSandboxDir(t *testing.T) {
	sandboxDir := t.TempDir()
	// Write a file to confirm it really gets removed
	if err := os.WriteFile(filepath.Join(sandboxDir, "service.hcl"), []byte("data"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := terragrunt.CleanupSandboxDir(sandboxDir); err != nil {
		t.Fatalf("CleanupSandboxDir failed: %v", err)
	}

	if _, err := os.Stat(sandboxDir); !os.IsNotExist(err) {
		t.Errorf("sandbox directory should have been removed, but still exists: %s", sandboxDir)
	}
}

// ---- Runner command construction tests ----
// These tests do NOT run terragrunt; they inspect the command built by the runner.

func TestRunnerApplyCommand(t *testing.T) {
	r := terragrunt.NewRunner("klanker-terraform", "/repo/root")
	sandboxDir := makeFakeSandboxDir(t)

	cmd := r.BuildApplyCommand(context.Background(), sandboxDir)

	// Verify binary and args
	if cmd.Path == "" {
		t.Fatal("command path is empty")
	}
	args := cmd.Args
	if len(args) < 3 {
		t.Fatalf("expected at least 3 args, got %v", args)
	}
	// Args[0] is the binary path; check the remaining args form the expected command
	if args[1] != "apply" {
		t.Errorf("args[1] = %q, want %q", args[1], "apply")
	}
	if args[2] != "-auto-approve" {
		t.Errorf("args[2] = %q, want %q", args[2], "-auto-approve")
	}

	// Dir should be set to the sandbox directory
	if cmd.Dir != sandboxDir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, sandboxDir)
	}

	// AWS_PROFILE must be in env
	found := false
	for _, e := range cmd.Env {
		if e == "AWS_PROFILE=klanker-terraform" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AWS_PROFILE=klanker-terraform not found in cmd.Env; got %v", cmd.Env)
	}
}

func TestRunnerDestroyCommand(t *testing.T) {
	r := terragrunt.NewRunner("klanker-terraform", "/repo/root")
	sandboxDir := makeFakeSandboxDir(t)

	cmd := r.BuildDestroyCommand(context.Background(), sandboxDir)

	args := cmd.Args
	if len(args) < 3 {
		t.Fatalf("expected at least 3 args, got %v", args)
	}
	if args[1] != "destroy" {
		t.Errorf("args[1] = %q, want %q", args[1], "destroy")
	}
	if args[2] != "-auto-approve" {
		t.Errorf("args[2] = %q, want %q", args[2], "-auto-approve")
	}
	if cmd.Dir != sandboxDir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, sandboxDir)
	}
}

func TestRunnerOutputCommand(t *testing.T) {
	r := terragrunt.NewRunner("klanker-terraform", "/repo/root")
	sandboxDir := makeFakeSandboxDir(t)

	cmd := r.BuildOutputCommand(context.Background(), sandboxDir)

	args := cmd.Args
	if len(args) < 3 {
		t.Fatalf("expected at least 3 args, got %v", args)
	}
	if args[1] != "output" {
		t.Errorf("args[1] = %q, want %q", args[1], "output")
	}
	if args[2] != "-json" {
		t.Errorf("args[2] = %q, want %q", args[2], "-json")
	}
	if cmd.Dir != sandboxDir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, sandboxDir)
	}
}
