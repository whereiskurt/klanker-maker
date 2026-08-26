package cmd_test

// Task 10 (Phase 127 deploy surface) — pins the four registration points that
// each fail SILENTLY if missed: a module absent from regionalModules() is
// never applied (km doctor stays green regardless); a Lambda absent from the
// zip build list ships stale or absent code; and an unwired sugar alias just
// returns an error at the cobra layer with no other symptom. A future refactor
// touching any of these lists cannot quietly un-deploy the generic webhook
// ingress bridge.

import (
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

func TestWebhookBridge_RegisteredEverywhere(t *testing.T) {
	t.Run("regionalModules includes lambda-webhook-bridge", func(t *testing.T) {
		mods := cmd.RegionalModules("infra/live/use1")
		for _, m := range mods {
			if m.Name == "lambda-webhook-bridge" {
				return
			}
		}
		t.Fatal("lambda-webhook-bridge missing from regionalModules(): the module " +
			"would never be applied and km doctor would stay green")
	})

	t.Run("lambda zip build list includes km-webhook-bridge", func(t *testing.T) {
		for _, name := range cmd.LambdaBuildNames() {
			if name == "km-webhook-bridge" {
				return
			}
		}
		t.Fatal("km-webhook-bridge missing from buildLambdaZips: the zip is " +
			"never built and the deploy ships stale or absent code")
	})

	t.Run("--webhooks resolves to the module", func(t *testing.T) {
		got, err := cmd.ResolveScopedModule("", false, false, false, false, true)
		if err != nil {
			t.Fatalf("ResolveScopedModule(webhooks=true): unexpected error: %v", err)
		}
		if got != "lambda-webhook-bridge" {
			t.Fatalf("ResolveScopedModule(webhooks=true): got %q, want %q", got, "lambda-webhook-bridge")
		}
	})
}
