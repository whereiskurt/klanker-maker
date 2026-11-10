package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// wizOutpostModule is the regional module that creates the Wiz Outpost
// connector roles.
const wizOutpostModule = "wiz-outpost-connector"

// requireWizBlock fails when the wiz-outpost-connector module is requested but
// km-config.yaml has no wiz.outpost block (the module's env var would be unset).
func requireWizBlock(cfg *config.Config, module string) error {
	if module != wizOutpostModule {
		return nil
	}
	if cfg == nil || cfg.Wiz.Outpost == nil {
		return fmt.Errorf("--wiz requires a wiz.outpost block in km-config.yaml")
	}
	return nil
}

// printWizPasteOut prints the three values the operator pastes into the Wiz UI.
func printWizPasteOut(w io.Writer, accessARN, scannerARN, externalID string) {
	fmt.Fprintln(w, "Wiz Outpost connector — paste into the Wiz UI:")
	fmt.Fprintf(w, "  Access role ARN   %s\n", accessARN)
	fmt.Fprintf(w, "  Scanner role ARN  %s\n", scannerARN)
	fmt.Fprintf(w, "  External ID       %s\n", externalID)
}

// printWizOutputsAfterApply reads the module outputs and prints the paste-out.
// Never fails the apply: a missing output yields a one-line warning.
func printWizOutputsAfterApply(ctx context.Context, runner InitRunner, dir string, w io.Writer) {
	outputMap, err := runner.Output(ctx, dir)
	if err != nil {
		fmt.Fprintf(w, "  Warning: could not read wiz-outpost-connector outputs: %v (run `km wiz status`)\n", err)
		return
	}
	get := func(k string) (string, bool) {
		v, ok := outputMap[k]
		if !ok {
			return "", false
		}
		str := fmt.Sprintf("%v", extractValue(v))
		return str, str != ""
	}
	access, ok1 := get("access_role_arn")
	scanner, ok2 := get("scanner_role_arn")
	extID, ok3 := get("external_id")
	if !ok1 || !ok2 || !ok3 {
		fmt.Fprintln(w, "  Warning: wiz-outpost-connector outputs incomplete (run `km wiz status`)")
		return
	}
	fmt.Fprintln(w)
	printWizPasteOut(w, access, scanner, extID)
}
