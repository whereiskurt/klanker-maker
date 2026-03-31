package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ShellExecFunc is the function signature for executing the AWS CLI subprocess.
// It is package-level so tests can replace it to capture args without executing.
type ShellExecFunc func(c *exec.Cmd) error

// defaultShellExec calls cmd.Run() — the real subprocess execution path.
func defaultShellExec(c *exec.Cmd) error {
	return c.Run()
}

// NewShellCmd creates the "km shell" subcommand using the real AWS-backed fetcher.
// Usage: km shell <sandbox-id>
func NewShellCmd(cfg *config.Config) *cobra.Command {
	return NewShellCmdWithFetcher(cfg, nil, nil)
}

// NewAgentCmd creates the "km agent" command that launches an AI agent inside a sandbox.
func NewAgentCmd(cfg *config.Config) *cobra.Command {
	var useClaude bool
	var useCodex bool

	cmd := &cobra.Command{
		Use:          "agent <sandbox-id | #number> [-- extra-args...]",
		Short:        "Launch an AI coding agent inside a sandbox",
		Long: `Launch an AI coding agent inside a running sandbox via SSM.

Connects as the sandbox user and runs the selected agent binary.
Extra arguments after -- are passed through to the agent.

Examples:
  km agent 1 --claude                    # interactive claude session
  km agent 1 --claude -- -p "fix tests"  # pass args to claude
  km agent #2 --codex                    # launch codex (future)`,
		Args:         cobra.MinimumNArgs(1),
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

			var agentCmd string
			switch {
			case useClaude:
				agentCmd = "claude"
			case useCodex:
				agentCmd = "codex"
			default:
				return fmt.Errorf("specify an agent: --claude or --codex")
			}

			// Pass any extra args after the sandbox ID
			if len(args) > 1 {
				agentCmd += " " + strings.Join(args[1:], " ")
			}

			return runAgent(cmd, cfg, nil, nil, sandboxID, agentCmd)
		},
	}

	cmd.Flags().BoolVar(&useClaude, "claude", false, "Launch Claude Code")
	cmd.Flags().BoolVar(&useCodex, "codex", false, "Launch OpenAI Codex (future)")

	return cmd
}

// NewShellCmdWithFetcher builds the shell command with an optional custom fetcher and
// exec function. Pass nil for real AWS-backed clients. Used in tests for DI.
func NewShellCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	var asRoot bool
	var ports []string

	cmd := &cobra.Command{
		Use:     "shell <sandbox-id | #number>",
		Aliases: []string{"sh"},
		Short:   "Open an interactive shell into a running sandbox",
		Long: `Open an interactive SSM session into a running sandbox.

Port forwarding:
  --ports 8080         forward localhost:8080 → remote:8080
  --ports 8080:80      forward localhost:8080 → remote:80
  --ports 8080,3000    forward multiple ports
  --ports 8080:80,3000 mix of mapped and same-port forwards`,
		Args:         cobra.ExactArgs(1),
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
			if len(ports) > 0 {
				return runPortForward(cmd, cfg, fetcher, execFn, sandboxID, ports)
			}
			return runShell(cmd, cfg, fetcher, execFn, sandboxID, asRoot)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "Connect as root instead of the restricted sandbox user")
	cmd.Flags().StringSliceVar(&ports, "ports", nil, "Port forwards: 8080, 8080:80, or comma-separated list")

	return cmd
}

// runShell is the command RunE logic for km shell.
func runShell(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, asRoot ...bool) error {
	root := len(asRoot) > 0 && asRoot[0]
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
		}
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket)
	}

	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped — start it with 'km budget add %s --compute <amount>' first", sandboxID, sandboxID)
	}

	switch rec.Substrate {
	case "ec2":
		instanceID, err := extractResourceID(rec.Resources, ":instance/")
		if err != nil {
			return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
		}
		return execSSMSession(ctx, instanceID, rec.Region, root, execFn)
	case "ecs":
		clusterARN, err := findResourceARN(rec.Resources, ":cluster/")
		if err != nil {
			return fmt.Errorf("find ECS cluster for sandbox %s: %w", sandboxID, err)
		}
		taskARN, err := findResourceARN(rec.Resources, ":task/")
		if err != nil {
			return fmt.Errorf("find ECS task for sandbox %s: %w", sandboxID, err)
		}
		return execECSCommand(ctx, clusterARN, taskARN, rec.Region, execFn)
	case "docker":
		return execDockerShell(ctx, sandboxID, root, execFn)
	default:
		return fmt.Errorf("unsupported substrate %q for km shell", rec.Substrate)
	}
}

// runAgent launches claude inside a sandbox via SSM.
func runAgent(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID, claudeCmd string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket)
	}
	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped", sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Open an interactive login shell as sandbox user that auto-execs the agent.
	// sudo -u sandbox -i gives a login shell with PTY (required for Claude's TUI).
	// We pass the agent command via an env var and use .bashrc-style exec trick:
	// bash --rcfile <(script) gives us both PTY and auto-exec.
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
		"--document-name", "AWS-StartInteractiveCommand",
		"--parameters", fmt.Sprintf(
			`{"command":["sudo -u sandbox -i bash -c 'source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; cd /workspace; exec %s'"]}`,
			claudeCmd))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// execSSMSession builds and runs an SSM session.
// When root is false, it runs: sudo -u sandbox -i (restricted non-root user).
// When root is true, it starts a standard root SSM session.
func execSSMSession(ctx context.Context, instanceID, region string, root bool, execFn ShellExecFunc) error {
	if root {
		c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
			"--target", instanceID, "--region", region, "--profile", "klanker-terraform")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return execFn(c)
	}

	// Non-root: use SSM document to start session as 'sandbox' user
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", region, "--profile", "klanker-terraform",
		"--document-name", "AWS-StartInteractiveCommand",
		"--parameters", `{"command":["sudo -u sandbox -i"]}`)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// execECSCommand builds and runs:
// aws ecs execute-command --cluster <clusterARN> --task <taskARN>
// --interactive --command /bin/bash --region <region>
func execECSCommand(ctx context.Context, clusterARN, taskARN, region string, execFn ShellExecFunc) error {
	c := exec.CommandContext(ctx, "aws", "ecs", "execute-command",
		"--cluster", clusterARN, "--task", taskARN,
		"--interactive", "--command", "/bin/bash", "--region", region, "--profile", "klanker-terraform")
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// runPortForward starts SSM port forwarding sessions for each requested port.
// Ports are specified as "local" (same port both sides) or "local:remote".
func runPortForward(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, ports []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket)
	}
	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Parse port specs and launch SSM port forwarding sessions
	// For multiple ports, launch all but the last in background, last in foreground.
	parsed := parsePortSpecs(ports)
	if len(parsed) == 0 {
		return fmt.Errorf("no valid port specifications provided")
	}

	fmt.Printf("Port forwarding for %s (%s):\n", sandboxID, instanceID)
	for _, p := range parsed {
		fmt.Printf("  localhost:%s → remote:%s\n", p.local, p.remote)
	}
	fmt.Println()

	// Launch all but the last as background processes
	var bgProcs []*exec.Cmd
	for i := 0; i < len(parsed)-1; i++ {
		p := parsed[i]
		c := buildPortForwardCmd(ctx, instanceID, rec.Region, p.local, p.remote)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] failed to start port forward %s:%s: %v\n", p.local, p.remote, err)
			continue
		}
		bgProcs = append(bgProcs, c)
	}

	// Last port forward runs in foreground (blocks until Ctrl+C)
	last := parsed[len(parsed)-1]
	c := buildPortForwardCmd(ctx, instanceID, rec.Region, last.local, last.remote)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	fgErr := execFn(c)

	// Clean up background processes
	for _, bg := range bgProcs {
		if bg.Process != nil {
			bg.Process.Kill()
		}
	}

	return fgErr
}

type portSpec struct {
	local  string
	remote string
}

// parsePortSpecs parses port specifications like "8080", "8080:80", or comma-separated.
func parsePortSpecs(specs []string) []portSpec {
	var result []portSpec
	for _, spec := range specs {
		// StringSliceVar already splits on comma, but handle nested commas too
		for _, s := range strings.Split(spec, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			parts := strings.SplitN(s, ":", 2)
			if len(parts) == 1 {
				result = append(result, portSpec{local: parts[0], remote: parts[0]})
			} else {
				result = append(result, portSpec{local: parts[0], remote: parts[1]})
			}
		}
	}
	return result
}

// buildPortForwardCmd constructs the AWS SSM port forwarding command.
func buildPortForwardCmd(ctx context.Context, instanceID, region, localPort, remotePort string) *exec.Cmd {
	return exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID,
		"--region", region,
		"--profile", "klanker-terraform",
		"--document-name", "AWS-StartPortForwardingSession",
		"--parameters", fmt.Sprintf(`{"portNumber":["%s"],"localPortNumber":["%s"]}`, remotePort, localPort))
}

// execDockerShell builds and runs: docker exec -it [(-u root)] km-{sandboxID}-main /bin/bash.
// The container name is derived from the sandbox ID using the fixed naming convention
// set in the docker-compose.yml template: container_name: km-{sandboxID}-main.
func execDockerShell(ctx context.Context, sandboxID string, root bool, execFn ShellExecFunc) error {
	containerName := fmt.Sprintf("km-%s-main", sandboxID)
	args := []string{"exec", "-it"}
	if root {
		args = append(args, "-u", "root")
	} else {
		args = append(args, "-u", "sandbox")
	}
	// Use login shell so /etc/profile.d/ scripts run (env vars, shutdown hooks).
	args = append(args, containerName, "bash", "--login")
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// extractResourceID finds an ARN containing pattern and extracts the resource ID
// portion after the last "/". Example: "arn:....:instance/i-0abc123" -> "i-0abc123".
func extractResourceID(resources []string, pattern string) (string, error) {
	arn, err := findResourceARN(resources, pattern)
	if err != nil {
		return "", err
	}
	parts := strings.Split(arn, "/")
	return parts[len(parts)-1], nil
}

// findResourceARN returns the first ARN in resources that contains pattern.
func findResourceARN(resources []string, pattern string) (string, error) {
	for _, arn := range resources {
		if strings.Contains(arn, pattern) {
			return arn, nil
		}
	}
	return "", fmt.Errorf("no resource matching %q found in %v", pattern, resources)
}
