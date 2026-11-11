package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

// ValidateUninitFlags rejects flag combinations km uninit cannot honour.
// --wiz is a single-unit teardown; --include-scp belongs to the full one.
func ValidateUninitFlags(wiz, includeSCP bool) error {
	if wiz && includeSCP {
		return errors.New("--wiz and --include-scp cannot be combined: --wiz removes only the Wiz Outpost connector stack")
	}
	return nil
}

// UninitWizPrompt is the confirmation shown by `km uninit --wiz`. It works
// whether or not km-config.yaml still has a wiz.outpost block (the common
// case is that the block was just removed): without one it names the stack
// and the default access role, and says the scanner role is the stack's.
func UninitWizPrompt(cfg *config.Config, region string) string {
	stack := wizoutpost.StackName(cfg.GetResourcePrefix())
	roles := "WizAccess-Role and the stack's WizScannerRole-*"
	if s := cfg.Wiz.Outpost; s != nil {
		roles = s.AccessRoleNameOrDefault() + " and " + s.ScannerRoleName()
	}
	return fmt.Sprintf("Destroy the Wiz Outpost connector stack %s (%s) in %s? Remove the connector in the Wiz UI first. [y/N] ",
		stack, roles, region)
}

// RunUninitScopedWithRunner destroys one regional module. Unlike the full
// uninit loop it has a single target, so a destroy failure is returned rather
// than logged and skipped. The unit's own get_env fallback ("{}") lets it
// evaluate with no wiz.outpost block configured.
//
// Exported so cmd_test can drive it with a mock runner.
func RunUninitScopedWithRunner(runner UninitRunner, repoRoot, region, module string) error {
	ctx := context.Background()
	label := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", label)
	if err := ensureRegionHCL(regionDir, label, region); err != nil {
		return err
	}
	dir := filepath.Join(regionDir, module)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("module %s not found: directory %s does not exist", module, dir)
	}

	printBanner(fmt.Sprintf("km uninit --only %s", module), fmt.Sprintf("%s (%s)", region, label))

	// Reconfigure first, as the full loop does; a failure is informational.
	if err := runner.Reconfigure(ctx, dir); err != nil {
		fmt.Printf("  [info] %s init -reconfigure failed (continuing to destroy): %v\n", module, err)
	}
	fmt.Printf("  Destroying %s...", module)
	if err := runner.Destroy(ctx, dir); err != nil {
		fmt.Println()
		if isBackendDriftError(err) {
			return fmt.Errorf("destroying %s: state lives in a different backend bucket than the current config resolves to (resource_prefix change?): %w", module, err)
		}
		return fmt.Errorf("destroying %s: %w", module, err)
	}
	fmt.Println(" done")
	if module == wizOutpostModule {
		fmt.Println()
		fmt.Println("Wiz Outpost connector roles removed. If you have not already, remove the connector in the Wiz UI.")
		fmt.Println("To keep it removed, delete the wiz: block from km-config.yaml (a later km init would recreate it).")
	}
	return nil
}

// runUninitScoped is the production wrapper: credentials, the KM_* env the
// live unit's site.hcl reads, and a real terragrunt runner. It skips the
// active-sandbox check — no sandbox depends on the Wiz roles.
func runUninitScoped(cfg *config.Config, awsProfile, region string, verbose bool, module string) error {
	ctx := context.Background()
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}
	ExportTerragruntEnvVars(cfg)

	repoRoot := findRepoRoot()
	tgRunner := terragrunt.NewRunner(awsProfile, repoRoot)
	tgRunner.Verbose = verbose
	return RunUninitScopedWithRunner(&uninitRunnerAdapter{inner: tgRunner}, repoRoot, region, module)
}
