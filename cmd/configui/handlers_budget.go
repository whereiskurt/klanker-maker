// Package main — handlers_budget.go
// BudgetFetcher and BudgetDisplayData for the ConfigUI dashboard.
// Wraps pkg/aws.GetBudget and formats monetary values and CSS classes for
// the dashboard template.
package main

import (
	"context"
	"fmt"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// BudgetFetcher is the narrow interface for fetching per-sandbox budget display data.
// In production, wraps pkg/aws.GetBudget. In tests, use mockBudgetFetcher.
type BudgetFetcher interface {
	GetBudget(ctx context.Context, sandboxID string) (*BudgetDisplayData, error)
}

// BudgetDisplayData holds formatted budget values ready for template rendering.
// Amounts are pre-formatted as "$1.23" strings so templates need no logic.
type BudgetDisplayData struct {
	ComputeSpent string // formatted "$1.23"
	ComputeLimit string // formatted "$5.00"
	ComputePct   int    // percentage 0-100+ of compute limit used
	AISpent      string
	AILimit      string
	AIPct        int    // percentage 0-100+ of AI limit used
	CSSClass     string // "budget-ok", "budget-warn", "budget-exceeded"
	HasBudget    bool   // false when no BUDGET#limits row exists for this sandbox
}

// budgetCSSClass returns the CSS class for a given spend percentage.
//
//	< 80%  → "budget-ok"      (green)
//	80-99% → "budget-warn"    (yellow)
//	≥ 100% → "budget-exceeded" (red)
//
// The worst class across compute and AI is used for the overall row badge.
func budgetCSSClass(pct int) string {
	switch {
	case pct >= 100:
		return "budget-exceeded"
	case pct >= 80:
		return "budget-warn"
	default:
		return "budget-ok"
	}
}

// worstCSSClass returns the more severe of two CSS class strings.
// budget-exceeded > budget-warn > budget-ok.
func worstCSSClass(a, b string) string {
	rank := map[string]int{
		"budget-ok":       0,
		"budget-warn":     1,
		"budget-exceeded": 2,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// formatUSD formats a float64 dollar amount as "$N.NNNN" (4 decimal places).
// Sub-penny AI charges (e.g. $0.0012) display correctly instead of rounding to "$0.00".
func formatUSD(amount float64) string {
	return fmt.Sprintf("$%.4f", amount)
}

// calcPct computes the integer percentage of spent/limit.
// Returns 0 when limit is zero (no limit configured).
func calcPct(spent, limit float64) int {
	if limit <= 0 {
		return 0
	}
	return int((spent / limit) * 100)
}

// BuildBudgetDisplayData converts a pkg/aws.BudgetSummary into a BudgetDisplayData
// ready for template rendering. When the summary has no limits configured
// (ComputeLimit == 0 and AILimit == 0), HasBudget is set to false.
func BuildBudgetDisplayData(summary *kmaws.BudgetSummary) *BudgetDisplayData {
	if summary == nil {
		return &BudgetDisplayData{HasBudget: false}
	}
	// If no limits are set, treat as no budget.
	if summary.ComputeLimit <= 0 && summary.AILimit <= 0 {
		return &BudgetDisplayData{HasBudget: false}
	}

	computePct := calcPct(summary.ComputeSpent, summary.ComputeLimit)
	aiPct := calcPct(summary.AISpent, summary.AILimit)
	computeClass := budgetCSSClass(computePct)
	aiClass := budgetCSSClass(aiPct)

	return &BudgetDisplayData{
		ComputeSpent: formatUSD(summary.ComputeSpent),
		ComputeLimit: formatUSD(summary.ComputeLimit),
		ComputePct:   computePct,
		AISpent:      formatUSD(summary.AISpent),
		AILimit:      formatUSD(summary.AILimit),
		AIPct:        aiPct,
		CSSClass:     worstCSSClass(computeClass, aiClass),
		HasBudget:    true,
	}
}

// dynoBudgetFetcher satisfies BudgetFetcher using the real DynamoDB API.
type dynoBudgetFetcher struct {
	client    kmaws.BudgetAPI
	tableName string
}

// GetBudget queries DynamoDB for the sandbox budget and returns formatted display data.
// If DynamoDB is unreachable or the sandbox has no budget rows, returns HasBudget=false
// (graceful degradation — dashboard shows dash instead of an error).
func (f *dynoBudgetFetcher) GetBudget(ctx context.Context, sandboxID string) (*BudgetDisplayData, error) {
	summary, err := kmaws.GetBudget(ctx, f.client, f.tableName, sandboxID)
	if err != nil {
		// Graceful degradation: treat unreachable DynamoDB as no-budget.
		return &BudgetDisplayData{HasBudget: false}, nil
	}
	return BuildBudgetDisplayData(summary), nil
}
