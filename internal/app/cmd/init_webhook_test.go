package cmd_test

// Task 10 (Phase 127 deploy surface) — pins the four registration points that
// each fail SILENTLY if missed: a module absent from regionalModules() is
// never applied (km doctor stays green regardless); a Lambda absent from the
// zip build list ships stale or absent code; and an unwired sugar alias just
// returns an error at the cobra layer with no other symptom. A future refactor
// touching any of these lists cannot quietly un-deploy the generic webhook
// ingress bridge.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	// defaultModuleTimeout's case list (init.go, exported for tests as
	// ModuleTimeoutFunc) gives ses/ttl-handler/create-handler/email-handler and
	// all three prior bridge Lambdas a 5-minute apply bound instead of the
	// 3-minute default — IAM role + Lambda propagation on a fresh apply can
	// exceed 3 minutes, and RunInitWithRunner SIGINTs (context deadline
	// exceeded) a module that overruns its bound, leaving a wedged orphan
	// terragrunt process the operator has to hunt down manually (see the
	// "wedged after %s" error text this same function's callers produce). A
	// silently-omitted lambda-webhook-bridge would still apply MOST of the
	// time — the failure only shows up on a slow apply, which is exactly the
	// silent-until-it-bites shape this task exists to close.
	t.Run("lambda-webhook-bridge gets the 5-minute bridge-Lambda timeout, not the 3-minute default", func(t *testing.T) {
		got := cmd.ModuleTimeoutFunc("lambda-webhook-bridge")
		want := 5 * time.Minute
		if got != want {
			t.Fatalf("ModuleTimeoutFunc(\"lambda-webhook-bridge\") = %v, want %v — missing from "+
				"the case list falls through to the 3-minute default and risks a wedged apply on "+
				"a slow IAM/Lambda propagation, exactly like an un-timed-out ses/h1/github/slack apply would",
				got, want)
		}
	})

	// The live terragrunt unit is what actually gets applied — a module present
	// in regionalModules() but missing its infra/live/use1/<name>/terragrunt.hcl
	// fails at apply time with "directory not found", which RunInitWithRunner
	// only reports as a `[skip]` line among dozens of others in a full `km init`
	// run: easy to miss, and the module is silently never provisioned.
	t.Run("live terragrunt unit exists for lambda-webhook-bridge", func(t *testing.T) {
		// internal/app/cmd/ is three directories below the repo root.
		liveUnit := filepath.Join("..", "..", "..", "infra", "live", "use1", "lambda-webhook-bridge", "terragrunt.hcl")
		if _, err := os.Stat(liveUnit); err != nil {
			t.Fatalf("live terragrunt unit missing at %s: %v — lambda-webhook-bridge is registered "+
				"in regionalModules() but has no unit to apply, so km init would skip it with only a "+
				"[skip] ... directory not found line", liveUnit, err)
		}
	})
}
