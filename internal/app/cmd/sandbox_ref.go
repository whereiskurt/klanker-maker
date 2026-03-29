package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// sandboxIDLike matches any valid sandbox ID: {prefix}-{8hex}
var sandboxIDLike = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`)

// ResolveSandboxID resolves a sandbox reference to a sandbox ID.
// The ref can be:
//   - A sandbox ID like "sb-a1b2c3d4" or "claude-a1b2c3d4" (returned as-is)
//   - A number "1"-"N" referring to the Nth sandbox from km list
func ResolveSandboxID(ctx context.Context, cfg *config.Config, ref string) (string, error) {
	// If it matches the sandbox ID pattern, treat it as a sandbox ID (further
	// validation happens in the individual commands like runDestroy).
	if sandboxIDLike.MatchString(ref) {
		return ref, nil
	}

	// Try parsing as a number.
	num, err := strconv.Atoi(ref)
	if err != nil || num < 1 {
		return "", fmt.Errorf("invalid sandbox reference %q: must be a sandbox ID ({prefix}-xxxxxxxx) or a number from 'km list'", ref)
	}

	// Fetch the sandbox list to resolve the number.
	records, err := listSandboxes(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("could not list sandboxes to resolve #%d: %w", num, err)
	}

	if num > len(records) {
		return "", fmt.Errorf("sandbox #%d does not exist (only %d sandboxes listed)", num, len(records))
	}

	resolved := records[num-1].SandboxID
	fmt.Printf("Resolved #%d → %s\n", num, resolved)
	return resolved, nil
}

// listSandboxes fetches the current sandbox list from S3.
func listSandboxes(ctx context.Context, cfg *config.Config) ([]kmaws.SandboxRecord, error) {
	if cfg.StateBucket == "" {
		return nil, fmt.Errorf("state bucket not configured")
	}
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, err
	}
	lister := newRealLister(awsCfg, cfg.StateBucket)
	return lister.ListSandboxes(ctx, false)
}
