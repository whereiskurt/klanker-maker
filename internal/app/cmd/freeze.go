package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// FreezeableDDB is the narrow DynamoDB interface needed by the freeze command.
// It is satisfied by *dynamodb.Client and by test mocks.
type FreezeableDDB interface {
	awspkg.SandboxMetadataAPI
}

// LatchAwareDDB is the narrow DynamoDB interface needed by the latch-aware
// unlock command (covers both UnlockSandboxDynamo + UnfreezeSandboxDynamo).
// Identical to FreezeableDDB in method set — kept separate so callers can
// express their intent precisely.
type LatchAwareDDB interface {
	awspkg.SandboxMetadataAPI
}

// NewFreezeCmd creates the "km freeze <sandbox> [--reason ...]" subcommand.
// km freeze is the operator panic-button that latches action_frozen=true on the
// sandbox's DynamoDB row. The box keeps running; all outbound actions gated by
// the quota middleware are silently dropped. Release is via km unlock only.
func NewFreezeCmd(cfg *config.Config) *cobra.Command {
	return NewFreezeCmdWithDDB(cfg, nil)
}

// NewFreezeCmdWithDDB builds the freeze command with an optional injected DDB
// client. Pass nil to use the real AWS-backed client. Used in tests to inject
// a mock without touching real DynamoDB.
func NewFreezeCmdWithDDB(cfg *config.Config, ddb FreezeableDDB) *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "freeze <sandbox-id | #number>",
		Short: "Quarantine a sandbox by latching action_frozen=true (panic button)",
		Long:  helpText("freeze"),
		Args:  cobra.ExactArgs(1),
		// Suppress usage on error — the error message is actionable enough.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runFreeze(ctx, cmd, cfg, ddb, sandboxID, reason)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "operator freeze", "Human-readable reason for the freeze (stored in DynamoDB)")

	return cmd
}

// runFreeze implements the km freeze logic: resolve the sandbox, call
// FreezeSandboxDynamo with reason + by="operator:<sandboxID>", print result.
func runFreeze(ctx context.Context, cmd *cobra.Command, cfg *config.Config, ddb FreezeableDDB, sandboxID, reason string) error {
	dynamoClient, err := resolveFreezeDDB(ctx, ddb)
	if err != nil {
		return err
	}

	tableName := cfg.GetSandboxTableName()
	by := "operator:" + sandboxID

	freezeErr := awspkg.FreezeSandboxDynamo(ctx, dynamoClient, tableName, sandboxID, reason, by)
	if freezeErr != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(freezeErr, &rnf) {
			return fmt.Errorf("freeze: DynamoDB table %q not found — run 'km init --dry-run=false' first", tableName)
		}
		return freezeErr
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, ansiRed+"Frozen %s (%s)."+ansiReset+" Release with: km unlock %s\n", sandboxID, reason, sandboxID)
	fmt.Fprintf(out, "The box keeps running; outbound actions are now blocked by the quota middleware.\n")
	return nil
}

// resolveFreezeDDB returns the injected client if non-nil, otherwise loads the
// real AWS config and returns a *dynamodb.Client.
func resolveFreezeDDB(ctx context.Context, injected FreezeableDDB) (awspkg.SandboxMetadataAPI, error) {
	if injected != nil {
		return injected, nil
	}
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return dynamodb.NewFromConfig(awsCfg), nil
}
