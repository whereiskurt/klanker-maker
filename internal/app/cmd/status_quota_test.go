package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/pkg/quota"
)

// TestRenderQuotas_Basic — the Quotas section renders one aligned line per
// configured (action, window) with used/limit and the onBreach policy, and
// marks limit==0 windows as hard-deny.
func TestRenderQuotas_Basic(t *testing.T) {
	actionLimitsJSON := `{
		"slack_post":  {"lifetime": 100, "onBreach": "block"},
		"github_pr":   {"perDay": 0,    "onBreach": "block"},
		"email_send":  {"perHour": 10,  "onBreach": "warn"}
	}`
	usage := []quota.UsageRow{
		{Action: quota.ActionSlackPost, Window: "lifetime", Used: 3, Limit: 100, OnBreach: quota.BreachBlock},
		{Action: quota.ActionGithubPR, Window: "day", Used: 0, Limit: 0, OnBreach: quota.BreachBlock},
		{Action: quota.ActionEmailSend, Window: "hour", Used: 2, Limit: 10, OnBreach: quota.BreachWarn},
	}

	buf := &bytes.Buffer{}
	cmd.RenderQuotas(buf, actionLimitsJSON, usage)
	out := buf.String()

	if !strings.Contains(out, "Quotas:") {
		t.Fatalf("expected 'Quotas:' header, got:\n%s", out)
	}
	if !strings.Contains(out, "slack_post") || !strings.Contains(out, "3/100") {
		t.Errorf("expected slack_post 3/100, got:\n%s", out)
	}
	if !strings.Contains(out, "block") {
		t.Errorf("expected onBreach policy 'block', got:\n%s", out)
	}
	if !strings.Contains(out, "0/0") || !strings.Contains(out, "hard-deny") {
		t.Errorf("expected github_pr 0/0 (hard-deny), got:\n%s", out)
	}
	if !strings.Contains(out, "email_send") || !strings.Contains(out, "2/10") || !strings.Contains(out, "warn") {
		t.Errorf("expected email_send 2/10 warn, got:\n%s", out)
	}
}

// TestRenderQuotas_NoUsage — when usage is unavailable, render configured limits
// with used "?" (still showing the limit and policy).
func TestRenderQuotas_NoUsage(t *testing.T) {
	actionLimitsJSON := `{"slack_post": {"lifetime": 50, "onBreach": "freeze"}}`

	buf := &bytes.Buffer{}
	cmd.RenderQuotas(buf, actionLimitsJSON, nil) // nil usage → unavailable
	out := buf.String()

	if !strings.Contains(out, "Quotas:") {
		t.Fatalf("expected 'Quotas:' header, got:\n%s", out)
	}
	if !strings.Contains(out, "?/50") {
		t.Errorf("expected '?/50' when usage unavailable, got:\n%s", out)
	}
	if !strings.Contains(out, "freeze") {
		t.Errorf("expected onBreach 'freeze', got:\n%s", out)
	}
}

// TestRenderQuotas_Empty — empty/blank ActionLimits omits the section entirely.
func TestRenderQuotas_Empty(t *testing.T) {
	buf := &bytes.Buffer{}
	cmd.RenderQuotas(buf, "", nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty ActionLimits, got:\n%s", buf.String())
	}

	buf.Reset()
	cmd.RenderQuotas(buf, "   ", nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for blank ActionLimits, got:\n%s", buf.String())
	}
}
