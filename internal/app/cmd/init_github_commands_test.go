// init_github_commands_test.go — Unit tests for km init GitHub command @file resolution
// and SSM publication (Phase 99 Plan 03).
//
// Requirements covered:
//   - GH-CMD-FILEREF: @file prompts resolved at km init time, relative to km-config.yaml dir;
//     missing file = hard error; @@ escape = literal; inline (no @) = unchanged.
//   - GH-CMD-SSM: Assembled command JSON published to SSM {prefix}/config/github/commands as
//     plain String; dormancy gate (no commands → no SSM write); drift WARN on divergent prior value.
package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ============================================================
// Fake SSM client for commands tests
// ============================================================

// fakeCommandsSSM records PutParameter calls and supports a seeded GetParameter store.
type fakeCommandsSSM struct {
	params map[string]string       // pre-seeded SSM params for GetParameter
	puts   []*ssm.PutParameterInput // recorded PutParameter calls
	getErr error                   // optional error returned by GetParameter
	putErr error                   // optional error returned by PutParameter
}

func newFakeCommandsSSM(params map[string]string) *fakeCommandsSSM {
	if params == nil {
		params = map[string]string{}
	}
	return &fakeCommandsSSM{params: params}
}

func (f *fakeCommandsSSM) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	name := aws.ToString(input.Name)
	val, ok := f.params[name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:  aws.String(name),
			Value: aws.String(val),
		},
	}, nil
}

func (f *fakeCommandsSSM) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	f.puts = append(f.puts, input)
	return &ssm.PutParameterOutput{}, nil
}

// Compile-time check: fakeCommandsSSM must satisfy cmd.SSMReadWriteAPI.
var _ cmd.SSMReadWriteAPI = &fakeCommandsSSM{}

// ============================================================
// TestResolveCommandPrompts — pure @file resolution helper
// ============================================================

// TestResolveCommandPrompts verifies the four resolution cases for command prompt fields:
//
//  1. Inline prompt (no @) → unchanged.
//  2. @@ escape → literal @-prefixed string, no file read.
//  3. @relative/path.txt resolved relative to config-dir → file contents inlined.
//  4. @missing.txt → hard error (err != nil, message contains path).
//  5. Config-dir-relative: file written under configDir, resolver with different CWD resolves it.
func TestResolveCommandPrompts(t *testing.T) {
	// ---- shared fixtures ----
	configDir := t.TempDir()
	promptFile := filepath.Join(configDir, "gh-review.txt")
	promptContent := "Please review this PR for correctness and style."
	if err := os.WriteFile(promptFile, []byte(promptContent), 0o600); err != nil {
		t.Fatalf("setup: write prompt file: %v", err)
	}

	// Sub-dir to confirm relative path resolution.
	subDir := filepath.Join(configDir, "prompts")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("setup: mkdir prompts: %v", err)
	}
	subPrompt := filepath.Join(subDir, "triage.txt")
	subContent := "Triage this issue: classify severity and assign labels."
	if err := os.WriteFile(subPrompt, []byte(subContent), 0o600); err != nil {
		t.Fatalf("setup: write sub prompt: %v", err)
	}

	t.Run("inline prompt unchanged", func(t *testing.T) {
		cmds := map[string]config.GithubCommandEntry{
			"review": {Prompt: "Do a code review."},
		}
		got, err := cmd.ResolveCommandPrompts(cmds, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["review"].Prompt != "Do a code review." {
			t.Errorf("want %q, got %q", "Do a code review.", got["review"].Prompt)
		}
	})

	t.Run("@@ escape yields literal @-prefix", func(t *testing.T) {
		cmds := map[string]config.GithubCommandEntry{
			"mention": {Prompt: "@@bot please help"},
		}
		got, err := cmd.ResolveCommandPrompts(cmds, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "@bot please help"
		if got["mention"].Prompt != want {
			t.Errorf("want %q, got %q", want, got["mention"].Prompt)
		}
	})

	t.Run("@file resolved relative to configDir", func(t *testing.T) {
		cmds := map[string]config.GithubCommandEntry{
			"review": {Prompt: "@gh-review.txt"},
			"triage": {Prompt: "@prompts/triage.txt"},
		}
		got, err := cmd.ResolveCommandPrompts(cmds, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["review"].Prompt != promptContent {
			t.Errorf("review: want %q, got %q", promptContent, got["review"].Prompt)
		}
		if got["triage"].Prompt != subContent {
			t.Errorf("triage: want %q, got %q", subContent, got["triage"].Prompt)
		}
	})

	t.Run("missing @file is a hard error", func(t *testing.T) {
		cmds := map[string]config.GithubCommandEntry{
			"broken": {Prompt: "@nope.txt"},
		}
		_, err := cmd.ResolveCommandPrompts(cmds, configDir)
		if err == nil {
			t.Fatal("expected error for missing @file, got nil")
		}
		if !strings.Contains(err.Error(), "nope.txt") {
			t.Errorf("error message should contain the missing path; got: %v", err)
		}
	})

	t.Run("resolver uses configDir, not CWD", func(t *testing.T) {
		// Create file ONLY in configDir (not in CWD or any other temp dir).
		cwd, _ := os.Getwd()
		if _, err := os.Stat(filepath.Join(cwd, "gh-review.txt")); err == nil {
			t.Skip("gh-review.txt unexpectedly exists in CWD — skipping CWD isolation test")
		}
		cmds := map[string]config.GithubCommandEntry{
			"review": {Prompt: "@gh-review.txt"},
		}
		// Run with CWD != configDir — should still resolve from configDir.
		got, err := cmd.ResolveCommandPrompts(cmds, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["review"].Prompt != promptContent {
			t.Errorf("want %q, got %q", promptContent, got["review"].Prompt)
		}
	})
}

// ============================================================
// TestInitGitHubCommands — SSM publication with fake SSM client
// ============================================================

// TestInitGitHubCommands_WritesAssembledJSON verifies that when cfg has 2 commands
// (one @file, one inline), PublishGitHubCommandsToSSM writes the CommandSet envelope
// JSON to {prefix}config/github/commands as ParameterTypeString with Overwrite=true.
// The @file content is inlined in the written value.
// The envelope shape is: {"commands": {...}, "default_command": "..."}.
func TestInitGitHubCommands_WritesAssembledJSON(t *testing.T) {
	// Write an @file template.
	configDir := t.TempDir()
	promptPath := filepath.Join(configDir, "review.txt")
	promptContent := "Review PR {{args}} for style and correctness."
	if err := os.WriteFile(promptPath, []byte(promptContent), 0o600); err != nil {
		t.Fatalf("setup: write prompt file: %v", err)
	}

	cfg := &config.Config{
		ResourcePrefix: "km",
		ConfigFilePath: filepath.Join(configDir, "km-config.yaml"), // used to derive configDir
		Github: config.GithubConfig{
			DefaultCommand: "review", // install-wide default — must appear in envelope
			Commands: map[string]config.GithubCommandEntry{
				"review": {
					Description: "Code review",
					Alias:       "gh-review",
					Profile:     "github-review.yaml",
					Allow:       []string{"github.com"},
					Prompt:      "@review.txt", // @file — should be inlined
				},
				"triage": {
					Description: "Issue triage",
					Prompt:      "Triage this issue inline.", // inline — unchanged
				},
			},
		},
	}

	fakeSSM := newFakeCommandsSSM(nil) // no pre-seeded params (fresh install)
	var stderr bytes.Buffer

	err := cmd.PublishGitHubCommandsToSSM(context.Background(), fakeSSM, cfg, &stderr)
	if err != nil {
		t.Fatalf("PublishGitHubCommandsToSSM returned error: %v", err)
	}

	// Expect exactly one PutParameter call.
	if len(fakeSSM.puts) != 1 {
		t.Fatalf("expected 1 PutParameter call, got %d", len(fakeSSM.puts))
	}

	call := fakeSSM.puts[0]

	// Verify parameter name.
	wantName := "/km/config/github/commands"
	if aws.ToString(call.Name) != wantName {
		t.Errorf("parameter name: want %q, got %q", wantName, aws.ToString(call.Name))
	}

	// Verify type is plain String (NOT SecureString).
	if call.Type != ssmtypes.ParameterTypeString {
		t.Errorf("parameter type: want String, got %v", call.Type)
	}

	// Verify Overwrite=true.
	if !aws.ToBool(call.Overwrite) {
		t.Errorf("Overwrite should be true")
	}

	// Parse the written JSON as the CommandSet envelope.
	// Shape: {"commands": {"name": {<entry>}, ...}, "default_command": "..."}
	var envelope struct {
		DefaultCommand string `json:"default_command"`
		Commands       map[string]struct {
			Description string   `json:"description"`
			Alias       string   `json:"alias"`
			Profile     string   `json:"profile"`
			Allow       []string `json:"allow"`
			Prompt      string   `json:"prompt"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(call.Value)), &envelope); err != nil {
		t.Fatalf("written value is not valid JSON: %v\nvalue: %s", err, aws.ToString(call.Value))
	}

	// Verify install-wide default_command is present in the envelope.
	if envelope.DefaultCommand != "review" {
		t.Errorf("envelope.default_command: want %q, got %q", "review", envelope.DefaultCommand)
	}

	// Check review command — @file should be inlined.
	review, ok := envelope.Commands["review"]
	if !ok {
		t.Fatal("written JSON missing 'review' key in commands")
	}
	if review.Prompt != promptContent {
		t.Errorf("review.prompt: want %q (inlined @file), got %q", promptContent, review.Prompt)
	}
	if review.Alias != "gh-review" {
		t.Errorf("review.alias: want %q, got %q", "gh-review", review.Alias)
	}

	// Check triage command — inline prompt unchanged.
	triage, ok := envelope.Commands["triage"]
	if !ok {
		t.Fatal("written JSON missing 'triage' key in commands")
	}
	if triage.Prompt != "Triage this issue inline." {
		t.Errorf("triage.prompt: want inline text, got %q", triage.Prompt)
	}

	// No WARN should have been emitted (no prior SSM value).
	if stderr.Len() > 0 {
		t.Errorf("expected empty stderr on fresh install, got: %s", stderr.String())
	}
}

// TestInitGitHubCommands_DefaultCommandRoundTrip verifies that the install-wide
// github.default_command travels via the SSM envelope and is preserved in the
// written JSON. This is the gap-closure test for SC3a: previously default_command
// was silently dropped because it was written only to KM_GITHUB_DEFAULT_COMMAND
// env (which nothing set). Now it rides inside the CommandSet envelope.
func TestInitGitHubCommands_DefaultCommandRoundTrip(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix: "km",
		Github: config.GithubConfig{
			DefaultCommand: "triage", // install-wide default to preserve
			Commands: map[string]config.GithubCommandEntry{
				"triage": {Prompt: "Triage this issue."},
			},
		},
	}

	fakeSSM := newFakeCommandsSSM(nil)
	var stderr bytes.Buffer

	err := cmd.PublishGitHubCommandsToSSM(context.Background(), fakeSSM, cfg, &stderr)
	if err != nil {
		t.Fatalf("PublishGitHubCommandsToSSM returned error: %v", err)
	}

	if len(fakeSSM.puts) != 1 {
		t.Fatalf("expected 1 PutParameter call, got %d", len(fakeSSM.puts))
	}

	writtenJSON := aws.ToString(fakeSSM.puts[0].Value)

	// The written JSON must be parseable as the CommandSet envelope.
	var envelope struct {
		DefaultCommand string                     `json:"default_command"`
		Commands       map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal([]byte(writtenJSON), &envelope); err != nil {
		t.Fatalf("written value is not valid envelope JSON: %v\nvalue: %s", err, writtenJSON)
	}

	// Install-wide default_command must be present in the envelope.
	if envelope.DefaultCommand != "triage" {
		t.Errorf("envelope.default_command: want %q, got %q (gap SC3a: default lost at runtime)", "triage", envelope.DefaultCommand)
	}

	// Commands map must still be present and non-empty.
	if len(envelope.Commands) == 0 {
		t.Error("envelope.commands must be non-empty")
	}
}

// TestInitGitHubCommands_Dormancy verifies that when cfg has NO commands, no
// PutParameter call is made for the commands param.
func TestInitGitHubCommands_Dormancy(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix: "km",
		Github: config.GithubConfig{
			// Commands is nil / empty — dormant.
			Commands: nil,
		},
	}

	fakeSSM := newFakeCommandsSSM(nil)
	var stderr bytes.Buffer

	err := cmd.PublishGitHubCommandsToSSM(context.Background(), fakeSSM, cfg, &stderr)
	if err != nil {
		t.Fatalf("PublishGitHubCommandsToSSM returned error: %v", err)
	}

	// NO PutParameter call should have been made.
	if len(fakeSSM.puts) != 0 {
		t.Errorf("dormancy: expected 0 PutParameter calls, got %d", len(fakeSSM.puts))
	}
}

// TestInitGitHubCommands_DriftWarn verifies that when the existing SSM value differs
// from the newly assembled JSON, a WARN is emitted to stderr and the new value is still
// written (yaml-derived value wins, but operator is warned about the divergence).
func TestInitGitHubCommands_DriftWarn(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix: "km",
		Github: config.GithubConfig{
			Commands: map[string]config.GithubCommandEntry{
				"review": {Prompt: "Review this PR."},
			},
		},
	}

	// Pre-seed SSM with a DIFFERENT value for the commands param.
	staleCmds := `{"review":{"prompt":"stale old prompt"}}`
	paramName := "/km/config/github/commands"
	fakeSSM := newFakeCommandsSSM(map[string]string{
		paramName: staleCmds,
	})
	var stderr bytes.Buffer

	err := cmd.PublishGitHubCommandsToSSM(context.Background(), fakeSSM, cfg, &stderr)
	if err != nil {
		t.Fatalf("PublishGitHubCommandsToSSM returned error: %v", err)
	}

	// A WARN must have been emitted.
	warnOut := stderr.String()
	if !strings.Contains(strings.ToUpper(warnOut), "WARN") {
		t.Errorf("expected WARN in stderr on drift, got: %q", warnOut)
	}

	// The new value must still have been written (yaml wins).
	if len(fakeSSM.puts) != 1 {
		t.Fatalf("drift WARN: expected 1 PutParameter call after WARN, got %d", len(fakeSSM.puts))
	}

	// Written value should be the NEW assembled JSON (not the stale one).
	writtenVal := aws.ToString(fakeSSM.puts[0].Value)
	if strings.Contains(writtenVal, "stale old prompt") {
		t.Errorf("expected written value to be the NEW prompt, not the stale one; got: %s", writtenVal)
	}
	if !strings.Contains(writtenVal, "Review this PR.") {
		t.Errorf("expected written value to contain 'Review this PR.'; got: %s", writtenVal)
	}
}

// TestInitGitHubCommands_NoDriftWarnWhenSame verifies that when the existing SSM value
// matches the assembled JSON exactly, no WARN is emitted.
func TestInitGitHubCommands_NoDriftWarnWhenSame(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix: "km",
		Github: config.GithubConfig{
			Commands: map[string]config.GithubCommandEntry{
				"review": {Prompt: "Review this PR."},
			},
		},
	}

	// Assemble what the function will compute, pre-seed that exact value.
	// We can't predict the exact JSON (key ordering may vary), so we'll use an
	// idiomatic approach: run the function once, capture the written value,
	// then re-run with that value pre-seeded and verify no WARN.
	firstSSM := newFakeCommandsSSM(nil)
	var stderr1 bytes.Buffer
	if err := cmd.PublishGitHubCommandsToSSM(context.Background(), firstSSM, cfg, &stderr1); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(firstSSM.puts) == 0 {
		t.Fatal("first run: expected at least one PutParameter call")
	}
	computedJSON := aws.ToString(firstSSM.puts[0].Value)

	// Second run with the same value pre-seeded — no WARN expected.
	seededSSM := newFakeCommandsSSM(map[string]string{
		"/km/config/github/commands": computedJSON,
	})
	var stderr2 bytes.Buffer
	if err := cmd.PublishGitHubCommandsToSSM(context.Background(), seededSSM, cfg, &stderr2); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if stderr2.Len() > 0 {
		t.Errorf("no WARN expected when SSM matches assembled JSON, got: %s", stderr2.String())
	}
	// Still writes (idempotent overwrite).
	if len(seededSSM.puts) != 1 {
		t.Errorf("expected 1 PutParameter call on idempotent re-init, got %d", len(seededSSM.puts))
	}
}

// Dummy reference to fmt to keep import used in non-test helper.
var _ = fmt.Sprintf
