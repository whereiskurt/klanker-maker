package main

import (
	"testing"
)

// TestMain_RequiresBudgetTable verifies that resolveBudgetTable calls exit(1)
// when KM_BUDGET_TABLE is not set and returns the env value when it is set.
func TestMain_RequiresBudgetTable(t *testing.T) {
	t.Run("exits when KM_BUDGET_TABLE is empty", func(t *testing.T) {
		exitCalled := false
		var exitCode int
		captureExit := func(code int) {
			exitCalled = true
			exitCode = code
		}

		getenv := func(key string) (string, bool) {
			return "", false
		}

		_ = resolveBudgetTable(getenv, captureExit)

		if !exitCalled {
			t.Fatal("expected exit to be called when KM_BUDGET_TABLE is unset")
		}
		if exitCode != 1 {
			t.Fatalf("expected exit code 1, got %d", exitCode)
		}
	})

	t.Run("returns env value when KM_BUDGET_TABLE is set", func(t *testing.T) {
		exitCalled := false
		captureExit := func(code int) {
			exitCalled = true
		}

		getenv := func(key string) (string, bool) {
			if key == "KM_BUDGET_TABLE" {
				return "rg-budgets", true
			}
			return "", false
		}

		result := resolveBudgetTable(getenv, captureExit)

		if exitCalled {
			t.Fatal("exit should not be called when KM_BUDGET_TABLE is set")
		}
		if result != "rg-budgets" {
			t.Fatalf("expected 'rg-budgets', got %q", result)
		}
	})
}
