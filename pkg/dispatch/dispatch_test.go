package dispatch_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/dispatch"
)

// errNotFound is the sentinel the mock resolver returns for an absent alias.
var errNotFound = errors.New("alias not found")

// mockResolver records the alias it was called with and returns a configurable result.
type mockResolver struct {
	sandboxID string
	status    string
	err       error
	called    bool
	lastAlias string
}

func (m *mockResolver) ResolveByAliasWithStatus(_ context.Context, alias string) (string, string, error) {
	m.called = true
	m.lastAlias = alias
	return m.sandboxID, m.status, m.err
}

// mockAgentRunSink records calls to DispatchAgentRun.
type mockAgentRunSink struct {
	called     bool
	sandboxID  string
	prompt     string
	returnErr  error
}

func (m *mockAgentRunSink) DispatchAgentRun(_ context.Context, sandboxID, prompt string) error {
	m.called = true
	m.sandboxID = sandboxID
	m.prompt = prompt
	return m.returnErr
}

// mockColdCreate records calls to ColdCreate.
type mockColdCreate struct {
	called    bool
	alias     string
	profile   string
	prompt    string
	returnErr error
}

func (m *mockColdCreate) ColdCreate(_ context.Context, alias, profile, prompt string) error {
	m.called = true
	m.alias = alias
	m.profile = profile
	m.prompt = prompt
	return m.returnErr
}

// mockNonceStore controls alreadySeen to test cooldown suppression.
type mockNonceStore struct {
	alreadySeen bool
	err         error
	callCount   int
	lastKey     string
	lastTTL     int
}

func (m *mockNonceStore) CheckAndStore(_ context.Context, key string, ttlSeconds int) (bool, error) {
	m.callCount++
	m.lastKey = key
	m.lastTTL = ttlSeconds
	return m.alreadySeen, m.err
}

// logger returns a discard logger for tests (keeps test output clean).
func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestResumeOrCreate_RunningAgentRun: resolver returns (id, "running", nil)
// → AgentRunSink.DispatchAgentRun called with (id, prompt); ColdCreate NOT called.
func TestResumeOrCreate_RunningAgentRun(t *testing.T) {
	const (
		sandboxID = "sb-abc123"
		alias     = "my-audit-box"
		prompt    = "audit the repo"
		profile   = "audit.yaml"
		checkName = "wiz-audit"
	)

	resolver := &mockResolver{sandboxID: sandboxID, status: "running"}
	agentRun := &mockAgentRunSink{}
	cold := &mockColdCreate{}
	nonces := &mockNonceStore{alreadySeen: false}

	err := dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "cold-create",
		0, // no cooldown
		resolver, agentRun, cold, nonces,
		logger(),
	)

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !agentRun.called {
		t.Error("expected AgentRunSink.DispatchAgentRun to be called for running sandbox")
	}
	if agentRun.sandboxID != sandboxID {
		t.Errorf("DispatchAgentRun sandboxID = %q, want %q", agentRun.sandboxID, sandboxID)
	}
	if agentRun.prompt != prompt {
		t.Errorf("DispatchAgentRun prompt = %q, want %q", agentRun.prompt, prompt)
	}
	if cold.called {
		t.Error("ColdCreate must NOT be called for a running sandbox")
	}
}

// TestResumeOrCreate_StoppedAgentRun: resolver returns (id, "stopped", nil)
// → AgentRunSink.DispatchAgentRun called (auto-resume is the sink's responsibility);
// ColdCreate NOT called.
func TestResumeOrCreate_StoppedAgentRun(t *testing.T) {
	const (
		sandboxID = "sb-def456"
		alias     = "paused-auditor"
		prompt    = "resume and check CVEs"
		profile   = "security.yaml"
		checkName = "cve-scan"
	)

	resolver := &mockResolver{sandboxID: sandboxID, status: "stopped"}
	agentRun := &mockAgentRunSink{}
	cold := &mockColdCreate{}
	nonces := &mockNonceStore{alreadySeen: false}

	err := dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "cold-create",
		0,
		resolver, agentRun, cold, nonces,
		logger(),
	)

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !agentRun.called {
		t.Error("expected AgentRunSink.DispatchAgentRun to be called for stopped sandbox (sink owns auto-resume)")
	}
	if agentRun.sandboxID != sandboxID {
		t.Errorf("DispatchAgentRun sandboxID = %q, want %q", agentRun.sandboxID, sandboxID)
	}
	if cold.called {
		t.Error("ColdCreate must NOT be called for a stopped (auto-resumable) sandbox")
	}
}

// TestResumeOrCreate_AbsentColdCreate: resolver returns ("", "", errNotFound),
// onAbsent="cold-create" → ColdCreateSink.ColdCreate called with (alias, profile, prompt);
// AgentRun NOT called.
func TestResumeOrCreate_AbsentColdCreate(t *testing.T) {
	const (
		alias     = "new-audit-box"
		prompt    = "onboard the new repo"
		profile   = "onboard.yaml"
		checkName = "repo-onboard"
	)

	resolver := &mockResolver{err: errNotFound}
	agentRun := &mockAgentRunSink{}
	cold := &mockColdCreate{}
	nonces := &mockNonceStore{alreadySeen: false}

	err := dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "cold-create",
		0,
		resolver, agentRun, cold, nonces,
		logger(),
	)

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if agentRun.called {
		t.Error("AgentRunSink must NOT be called when alias is absent")
	}
	if !cold.called {
		t.Error("ColdCreateSink.ColdCreate must be called when alias is absent and onAbsent=cold-create")
	}
	if cold.alias != alias {
		t.Errorf("ColdCreate alias = %q, want %q", cold.alias, alias)
	}
	if cold.profile != profile {
		t.Errorf("ColdCreate profile = %q, want %q", cold.profile, profile)
	}
	if cold.prompt != prompt {
		t.Errorf("ColdCreate prompt = %q, want %q", cold.prompt, prompt)
	}
}

// TestResumeOrCreate_AbsentSkip: resolver returns absent, onAbsent="skip"
// → neither sink called; returns nil.
func TestResumeOrCreate_AbsentSkip(t *testing.T) {
	const (
		alias     = "optional-box"
		prompt    = "analyze threat"
		profile   = "threat.yaml"
		checkName = "wiz-threat"
	)

	resolver := &mockResolver{err: errNotFound}
	agentRun := &mockAgentRunSink{}
	cold := &mockColdCreate{}
	nonces := &mockNonceStore{alreadySeen: false}

	err := dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "skip",
		0,
		resolver, agentRun, cold, nonces,
		logger(),
	)

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if agentRun.called {
		t.Error("AgentRunSink must NOT be called when onAbsent=skip")
	}
	if cold.called {
		t.Error("ColdCreateSink must NOT be called when onAbsent=skip")
	}
}

// TestResumeOrCreate_Cooldown: first call fires; second call (nonces returns
// alreadySeen=true) → neither sink called, returns nil.
func TestResumeOrCreate_Cooldown(t *testing.T) {
	const (
		sandboxID       = "sb-ghi789"
		alias           = "rate-limited-box"
		prompt          = "check the quota"
		profile         = "quota.yaml"
		checkName       = "quota-check"
		cooldownSeconds = 3600
	)

	// First call: not yet seen — fires normally.
	resolver := &mockResolver{sandboxID: sandboxID, status: "running"}
	agentRun := &mockAgentRunSink{}
	cold := &mockColdCreate{}
	nonces := &mockNonceStore{alreadySeen: false}

	err := dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "cold-create",
		cooldownSeconds,
		resolver, agentRun, cold, nonces,
		logger(),
	)

	if err != nil {
		t.Fatalf("first call: expected nil error, got: %v", err)
	}
	if !agentRun.called {
		t.Error("first call: expected AgentRunSink to be called")
	}

	// Verify the nonce key format: "check-trigger:{checkName}".
	wantKey := "check-trigger:" + checkName
	if nonces.lastKey != wantKey {
		t.Errorf("nonce key = %q, want %q", nonces.lastKey, wantKey)
	}
	if nonces.lastTTL != cooldownSeconds {
		t.Errorf("nonce TTL = %d, want %d", nonces.lastTTL, cooldownSeconds)
	}

	// Second call: cooldown active — both sinks must be suppressed.
	agentRun2 := &mockAgentRunSink{}
	cold2 := &mockColdCreate{}
	nonces2 := &mockNonceStore{alreadySeen: true}

	err = dispatch.ResumeOrCreate(
		context.Background(),
		checkName, alias, prompt, profile, "cold-create",
		cooldownSeconds,
		resolver, agentRun2, cold2, nonces2,
		logger(),
	)

	if err != nil {
		t.Fatalf("second call (cooldown): expected nil error, got: %v", err)
	}
	if agentRun2.called {
		t.Error("second call (cooldown): AgentRunSink must NOT be called during cooldown")
	}
	if cold2.called {
		t.Error("second call (cooldown): ColdCreateSink must NOT be called during cooldown")
	}
}
