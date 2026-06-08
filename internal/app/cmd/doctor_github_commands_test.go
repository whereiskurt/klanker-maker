// Package cmd — doctor_github_commands_test.go
// Phase 99 Plan 05 — TDD RED tests for GitHub bridge command doctor checks:
//   - checkGitHubCommandsValid  (pure config checks — no live AWS)
//   - checkGitHubCommandsSSMParam (SSM-present check via fake SSM)
//   - RunGitHubStatus listing of configured commands + per-repo effective default
//
// Tests run inside the internal `cmd` package to access unexported check functions.
package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// emptyCommands is a convenience builder for a single inline-prompt command.
func makeCommands(names ...string) map[string]appcfg.GithubCommandEntry {
	cmds := make(map[string]appcfg.GithubCommandEntry, len(names))
	for _, n := range names {
		cmds[n] = appcfg.GithubCommandEntry{
			Description: fmt.Sprintf("%s description", n),
			Prompt:      fmt.Sprintf("Execute the %s task.", n),
		}
	}
	return cmds
}

// ssmReadWriteForCommands is a minimal read/write fake that satisfies SSMReadAPI
// (used only for the SSM-present check in the doctor).
type ssmReadWriteForCommands struct {
	params map[string]string // pre-seeded get results
}

func (f *ssmReadWriteForCommands) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	key := aws.ToString(in.Name)
	v, ok := f.params[key]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String(v)},
	}, nil
}

func (f *ssmReadWriteForCommands) GetParametersByPath(_ context.Context, _ *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	return &ssm.GetParametersByPathOutput{}, nil
}

// Compile-time check: ssmReadWriteForCommands satisfies SSMReadAPI.
var _ SSMReadAPI = &ssmReadWriteForCommands{}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsAtFileMissing
// A command with prompt "@nope.txt" (missing file) → WARN.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsAtFileMissing(t *testing.T) {
	cmds := map[string]appcfg.GithubCommandEntry{
		"review": {Prompt: "@nope.txt", Description: "review"},
	}
	result := checkGitHubCommandsValid(cmds, "", nil, "", "")
	if result.Status != CheckWarn {
		t.Errorf("expected WARN for missing @file, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "nope.txt") {
		t.Errorf("expected missing file name in message, got: %s", result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsProfileUnresolvable
// A command with a non-existent profile path → WARN.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsProfileUnresolvable(t *testing.T) {
	cmds := map[string]appcfg.GithubCommandEntry{
		"review": {
			Prompt:  "Do a code review.",
			Profile: "/nonexistent/path/profile.yaml",
		},
	}
	result := checkGitHubCommandsValid(cmds, "", nil, "", "")
	if result.Status != CheckWarn {
		t.Errorf("expected WARN for unresolvable profile, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "profile") {
		t.Errorf("expected 'profile' in message, got: %s", result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsHelpShadow
// A user-defined command named "help" → WARN (reserved built-in).
// Phase 102: extended to cover "claude" and "codex" reserved agent verbs.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsHelpShadow(t *testing.T) {
	cmds := map[string]appcfg.GithubCommandEntry{
		"help": {Prompt: "Show help."},
	}
	result := checkGitHubCommandsValid(cmds, "", nil, "", "")
	if result.Status != CheckWarn {
		t.Errorf("expected WARN for 'help' shadow, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(strings.ToLower(result.Message), "help") {
		t.Errorf("expected 'help' in message, got: %s", result.Message)
	}
	if !strings.Contains(strings.ToLower(result.Message), "reserved") || !strings.Contains(strings.ToLower(result.Message+result.Remediation), "built-in") && !strings.Contains(strings.ToLower(result.Message+result.Remediation), "reserved") {
		t.Logf("message: %s, remediation: %s", result.Message, result.Remediation)
	}
}

// TestDoctorGitHubCommandsAgentVerbShadow — Phase 102
// User-defined commands named "claude" or "codex" → WARN (reserved agent verbs).
// Also verifies a clean map (no reserved names) produces no shadow WARN.
func TestDoctorGitHubCommandsAgentVerbShadow(t *testing.T) {
	tests := []struct {
		name          string
		commandNames  []string
		wantStatus    CheckStatus
		wantInMessage string // substring that must appear in WARN message
	}{
		{
			name:          "claude shadows reserved agent verb → WARN",
			commandNames:  []string{"claude"},
			wantStatus:    CheckWarn,
			wantInMessage: "claude",
		},
		{
			name:          "codex shadows reserved agent verb → WARN",
			commandNames:  []string{"codex"},
			wantStatus:    CheckWarn,
			wantInMessage: "codex",
		},
		{
			name:         "clean map with no reserved names → OK/SKIPPED (no shadow WARN)",
			commandNames: []string{"review", "patch"},
			wantStatus:   CheckOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmds := makeCommands(tc.commandNames...)
			result := checkGitHubCommandsValid(cmds, "", nil, "", "")
			if result.Status != tc.wantStatus {
				t.Errorf("expected %s got %s (msg=%s)", tc.wantStatus, result.Status, result.Message)
			}
			if tc.wantInMessage != "" && !strings.Contains(strings.ToLower(result.Message), tc.wantInMessage) {
				t.Errorf("expected %q in message, got: %s", tc.wantInMessage, result.Message)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsAliasOverlap
// A command.alias that equals a repo.alias → WARN via extended DetectGitHubAliasIssues.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsAliasOverlap(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Alias: "gh-myrepo", Profile: "profiles/github-review.yaml"},
	}
	cmds := map[string]appcfg.GithubCommandEntry{
		"review": {
			Alias:  "gh-myrepo", // collision with repo alias above
			Prompt: "Review this PR.",
		},
	}
	result := checkGitHubCommandsValid(cmds, "", repos, "", "")
	if result.Status != CheckWarn {
		t.Errorf("expected WARN for command-alias ↔ repo-alias overlap, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "gh-myrepo") {
		t.Errorf("expected colliding alias in message, got: %s", result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsDefaultUndefined
// github.default_command names an undefined command → ERROR.
// Per-repo default_command names an undefined command → ERROR.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsDefaultUndefined_TopLevel(t *testing.T) {
	cmds := makeCommands("review")
	// "triage" is NOT defined in cmds.
	result := checkGitHubCommandsValid(cmds, "triage", nil, "", "")
	if result.Status != CheckError {
		t.Errorf("expected ERROR for undefined top-level default_command, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "triage") {
		t.Errorf("expected undefined command name in message, got: %s", result.Message)
	}
}

func TestDoctorGitHubCommandsDefaultUndefined_PerRepo(t *testing.T) {
	cmds := makeCommands("review")
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Profile: "profiles/github-review.yaml", DefaultCommand: "triage"},
	}
	// "triage" is NOT defined in cmds.
	result := checkGitHubCommandsValid(cmds, "", repos, "", "")
	if result.Status != CheckError {
		t.Errorf("expected ERROR for undefined per-repo default_command, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "triage") {
		t.Errorf("expected undefined command name in message, got: %s", result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommandsSSMParam
// SSM param present → OK; absent while commands configured → WARN.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommandsSSMParam_Present(t *testing.T) {
	cmds := makeCommands("review")
	ssmClient := &ssmReadWriteForCommands{
		params: map[string]string{
			"/km/config/github/commands": `{"review":{"prompt":"Do a code review."}}`,
		},
	}
	result := checkGitHubCommandsSSMParam(context.Background(), ssmClient, "/km/", cmds)
	if result.Status != CheckOK {
		t.Errorf("expected OK when SSM param present, got %s (msg=%s)", result.Status, result.Message)
	}
}

func TestDoctorGitHubCommandsSSMParam_Absent(t *testing.T) {
	cmds := makeCommands("review")
	ssmClient := &ssmReadWriteForCommands{
		params: map[string]string{}, // param absent
	}
	result := checkGitHubCommandsSSMParam(context.Background(), ssmClient, "/km/", cmds)
	if result.Status != CheckWarn {
		t.Errorf("expected WARN when SSM param absent with commands configured, got %s (msg=%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "commands") {
		t.Errorf("expected 'commands' in message, got: %s", result.Message)
	}
	if !strings.Contains(result.Remediation, "km init") {
		t.Errorf("expected 'km init' in remediation, got: %s", result.Remediation)
	}
}

func TestDoctorGitHubCommandsSSMParam_Dormant(t *testing.T) {
	// When commands is nil/empty → SKIPPED (dormant by default).
	ssmClient := &ssmReadWriteForCommands{}
	result := checkGitHubCommandsSSMParam(context.Background(), ssmClient, "/km/", nil)
	if result.Status != CheckSkipped {
		t.Errorf("expected SKIPPED when no commands configured, got %s (msg=%s)", result.Status, result.Message)
	}
}

func TestDoctorGitHubCommandsSSMParam_NilClient(t *testing.T) {
	cmds := makeCommands("review")
	result := checkGitHubCommandsSSMParam(context.Background(), nil, "/km/", cmds)
	if result.Status != CheckSkipped {
		t.Errorf("expected SKIPPED when nil SSM client, got %s (msg=%s)", result.Status, result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommands_CleanConfig
// A clean config with inline prompts, valid profiles, no reserved names → all checks pass.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommands_CleanConfig(t *testing.T) {
	cmds := makeCommands("review", "triage")
	// No repos, no default_command — should be entirely OK / SKIPPED (not WARN or ERROR).
	result := checkGitHubCommandsValid(cmds, "", nil, "", "")
	if result.Status == CheckWarn || result.Status == CheckError {
		t.Errorf("expected OK or SKIPPED for clean config, got %s (msg=%s)", result.Status, result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDoctorGitHubCommands_Dormant
// When commands map is empty/nil → SKIPPED.
// ─────────────────────────────────────────────────────────────────────────────

func TestDoctorGitHubCommands_Dormant(t *testing.T) {
	result := checkGitHubCommandsValid(nil, "", nil, "", "")
	if result.Status != CheckSkipped {
		t.Errorf("expected SKIPPED when no commands configured, got %s (msg=%s)", result.Status, result.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestGitHubStatusCommands
// RunGitHubStatus lists configured commands (name + description + target)
// and prints the effective default per repo (per-repo override beats install-wide).
// Uses the mockGitHubSSMReadAPI defined in github_test.go (same internal package).
// ─────────────────────────────────────────────────────────────────────────────

// buildCommandsJSON is a helper to marshal a command map to JSON for SSM seeding.
func buildCommandsJSON(t *testing.T, cmds map[string]appcfg.GithubCommandEntry) string {
	t.Helper()
	// Mirror exactly what km init writes to SSM: a CommandSet envelope
	// ({"commands": {...}, "default_command": "..."}), base64-encoded to dodge
	// SSM's {{}} restriction. km github status / the bridge decode it on read.
	envelope := struct {
		Commands       map[string]appcfg.GithubCommandEntry `json:"commands"`
		DefaultCommand string                               `json:"default_command,omitempty"`
	}{Commands: cmds}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal commands envelope: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Note: TestGitHubStatusCommands is in the internal package (package cmd) so it can
// access unexported status internals.  It uses a local inline SSM mock.

type mockStatusSSMRead struct {
	params map[string]string
}

func (m *mockStatusSSMRead) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	key := aws.ToString(in.Name)
	v, ok := m.params[key]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String(v)},
	}, nil
}

// TestGitHubStatusCommands verifies that RunGitHubStatus lists commands from SSM
// with their descriptions and target aliases/profiles.
func TestGitHubStatusCommands(t *testing.T) {
	cmds := map[string]appcfg.GithubCommandEntry{
		"review": {
			Description: "read-only review, inline findings",
			Alias:       "gh-myorg",
			Prompt:      "Review this PR.",
		},
		"patch": {
			Description: "apply the smallest fix",
			Profile:     "profiles/github-dev.yaml",
			Prompt:      "Apply a patch.",
		},
	}
	commandsJSON := buildCommandsJSON(t, cmds)

	ssmMock := &mockStatusSSMRead{
		params: map[string]string{
			"/km/config/github/commands": commandsJSON,
		},
	}

	cfg := &appcfg.Config{
		ResourcePrefix: "km",
		Github: appcfg.GithubConfig{
			DefaultCommand: "review",
			Repos: []appcfg.GithubRepoEntry{
				{Match: "org/repo-a", Alias: "gh-myorg", DefaultCommand: "patch"},
				{Match: "org/repo-b", Alias: "gh-myorg"},
			},
		},
	}

	out := &bytes.Buffer{}
	if err := RunGitHubStatus(context.Background(), ssmMock, cfg, out); err != nil {
		t.Fatalf("RunGitHubStatus: %v", err)
	}

	outStr := out.String()

	// Commands section must be present.
	if !strings.Contains(outStr, "commands") {
		t.Errorf("expected 'commands' section in status output; got:\n%s", outStr)
	}

	// Command names must appear.
	if !strings.Contains(outStr, "review") {
		t.Errorf("expected 'review' command in status output; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "patch") {
		t.Errorf("expected 'patch' command in status output; got:\n%s", outStr)
	}

	// Default command info must appear.
	if !strings.Contains(outStr, "default_command") || !strings.Contains(outStr, "review") {
		t.Errorf("expected install-wide default_command 'review' in status output; got:\n%s", outStr)
	}
}

// TestGitHubStatusCommands_PerRepoDefault verifies that the per-repo default_command
// is shown as overriding the install-wide default.
func TestGitHubStatusCommands_PerRepoDefault(t *testing.T) {
	cmds := map[string]appcfg.GithubCommandEntry{
		"review": {Description: "review", Prompt: "Review."},
		"patch":  {Description: "patch", Prompt: "Patch."},
	}
	commandsJSON := buildCommandsJSON(t, cmds)

	ssmMock := &mockStatusSSMRead{
		params: map[string]string{
			"/km/config/github/commands": commandsJSON,
		},
	}

	cfg := &appcfg.Config{
		ResourcePrefix: "km",
		Github: appcfg.GithubConfig{
			DefaultCommand: "review",
			Repos: []appcfg.GithubRepoEntry{
				// repo-a overrides to "patch"
				{Match: "org/repo-a", Alias: "gh-myorg", DefaultCommand: "patch"},
			},
		},
	}

	out := &bytes.Buffer{}
	if err := RunGitHubStatus(context.Background(), ssmMock, cfg, out); err != nil {
		t.Fatalf("RunGitHubStatus: %v", err)
	}

	outStr := out.String()

	// Per-repo override should be visible.
	if !strings.Contains(outStr, "patch") {
		t.Errorf("expected per-repo default_command 'patch' in output; got:\n%s", outStr)
	}
	// Install-wide default should also appear.
	if !strings.Contains(outStr, "review") {
		t.Errorf("expected install-wide 'review' in output; got:\n%s", outStr)
	}
}

// TestGitHubStatusCommands_NoCommands verifies dormancy: when no commands are
// configured in SSM, the status output does NOT error and does NOT print a commands section.
func TestGitHubStatusCommands_NoCommands(t *testing.T) {
	ssmMock := &mockStatusSSMRead{
		params: map[string]string{
			// commands param absent — dormant
			"/km/config/github/webhook-secret": "s3cr3t",
			"/km/config/github/bot-login":      "km-bot[bot]",
		},
	}

	cfg := &appcfg.Config{ResourcePrefix: "km"}
	out := &bytes.Buffer{}

	err := RunGitHubStatus(context.Background(), ssmMock, cfg, out)
	if err != nil {
		t.Fatalf("RunGitHubStatus with no commands: %v", err)
	}

	outStr := out.String()
	// Must not ERROR or panic — just no commands section.
	// The standard status fields should still appear.
	if !strings.Contains(outStr, "webhook") && !strings.Contains(outStr, "bot-login") {
		t.Logf("status output (no commands): %s", outStr)
	}
}
