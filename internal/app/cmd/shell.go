package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
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

// NOTE: NewAgentCmd has been moved to agent.go with support for the "run" subcommand.

// NewShellCmdWithFetcher builds the shell command with an optional custom fetcher and
// exec function. Pass nil for real AWS-backed clients. Used in tests for DI.
func NewShellCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	var asRoot bool
	var noBedrock bool
	var ports []string
	var learn bool
	var learnOutput string

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
			// If --no-bedrock not explicitly set, check profile cli.noBedrock default
			if !cmd.Flags().Changed("no-bedrock") {
				if cliNB := loadProfileCLINoBedrock(ctx, cfg, sandboxID); cliNB {
					noBedrock = true
				}
			}
			// Run the shell (blocks until user exits).
			_ = runShell(cmd, cfg, fetcher, execFn, sandboxID, asRoot, noBedrock)

			// --learn post-exit: generate profile from observed traffic.
			if learn {
				return runLearnPostExit(ctx, cfg, fetcher, sandboxID, learnOutput)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "Connect as root instead of the restricted sandbox user")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false, "Unset Bedrock env vars (use direct Anthropic API)")
	cmd.Flags().StringSliceVar(&ports, "ports", nil, "Port forwards: 8080, 8080:80, or comma-separated list")
	cmd.Flags().BoolVar(&learn, "learn", false, "Run in learning mode: observe traffic and generate profile on exit")
	cmd.Flags().StringVar(&learnOutput, "learn-output", "observed-profile.yaml", "Path to write the generated SandboxProfile YAML (default: observed-profile.yaml in CWD)")

	return cmd
}

// runShell is the command RunE logic for km shell.
func runShell(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, flags ...bool) error {
	root := len(flags) > 0 && flags[0]
	noBedrock := len(flags) > 1 && flags[1]
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
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string { t := cfg.SandboxTableName; if t == "" { t = "km-sandboxes" }; return t }())
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
	case "ec2", "ec2spot", "ec2demand":
		instanceID, err := extractResourceID(rec.Resources, ":instance/")
		if err != nil {
			return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
		}
		return execSSMSession(ctx, instanceID, rec.Region, root, noBedrock, execFn)
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

// NOTE: runAgent has been moved to agent.go.

// execSSMSession builds and runs an SSM session.
// When root is false, it runs: sudo -u sandbox -i (restricted non-root user).
// When root is true, it starts a standard root SSM session.
func execSSMSession(ctx context.Context, instanceID, region string, root, noBedrock bool, execFn ShellExecFunc) error {
	if root {
		c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
			"--target", instanceID, "--region", region, "--profile", "klanker-terraform")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return execFn(c)
	}

	// Non-root: use SSM document to start session as 'sandbox' user
	if noBedrock {
		// Deploy a profile.d script that unsets Bedrock vars (runs last due to zz- prefix).
		// Uses SSM SendCommand then starts the interactive session. Cleaned up on exit.
		awsCfg, ssmErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if ssmErr == nil {
			ssmClient := ssm.NewFromConfig(awsCfg)
			_, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters: map[string][]string{
					"commands": {
						`echo 'unset CLAUDE_CODE_USE_BEDROCK ANTHROPIC_BASE_URL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL' > /etc/profile.d/zz-km-no-bedrock.sh`,
						`chmod 644 /etc/profile.d/zz-km-no-bedrock.sh`,
					},
				},
			})
			time.Sleep(2 * time.Second)

			// Clean up after session exits
			defer func() {
				_, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
					InstanceIds:  []string{instanceID},
					DocumentName: awssdk.String("AWS-RunShellScript"),
					Parameters:   map[string][]string{"commands": {"rm -f /etc/profile.d/zz-km-no-bedrock.sh"}},
				})
			}()
		}
	}

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
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string { t := cfg.SandboxTableName; if t == "" { t = "km-sandboxes" }; return t }())
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

// learnObservedState is the JSON format shared between ebpf-attach --observe
// (EC2) and collectDockerObservations (Docker). Both produce this structure
// which is then consumed by GenerateProfileFromJSON to generate a SandboxProfile.
type learnObservedState struct {
	DNS   []string `json:"dns"`
	Hosts []string `json:"hosts"`
	Repos []string `json:"repos"`
}

// GenerateProfileFromJSON parses an observed-state JSON blob and returns
// a SandboxProfile YAML. It is exported so tests can call it directly without
// AWS credentials or Docker.
//
// base is an optional profile name for the Extends field (pass "" to omit).
func GenerateProfileFromJSON(data []byte, base string) ([]byte, error) {
	var state learnObservedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse observed state: %w", err)
	}
	rec := allowlistgen.NewRecorder()
	for _, d := range state.DNS {
		rec.RecordDNSQuery(d)
	}
	for _, h := range state.Hosts {
		rec.RecordHost(h)
	}
	for _, r := range state.Repos {
		rec.RecordRepo(r)
	}
	return rec.GenerateAnnotatedYAML(base)
}

// CollectDockerObservations reads zerolog JSON from DNS and HTTP proxy log
// readers (e.g. from docker logs), feeds them into an allowlistgen.Recorder,
// and returns the observed state as JSON. Either reader may be nil.
// Exported for testing without requiring a running Docker daemon.
func CollectDockerObservations(sandboxID string, dnsLogs, httpLogs io.Reader) ([]byte, error) {
	rec := allowlistgen.NewRecorder()
	if err := allowlistgen.ParseProxyLogs(dnsLogs, httpLogs, rec); err != nil {
		return nil, fmt.Errorf("parse proxy logs for sandbox %s: %w", sandboxID, err)
	}
	state := learnObservedState{
		DNS:   rec.DNSDomains(),
		Hosts: rec.Hosts(),
		Repos: rec.Repos(),
	}
	return json.MarshalIndent(state, "", "  ")
}

// runLearnPostExit is called after the shell exits when --learn is active.
// It fetches observed traffic data, generates a SandboxProfile YAML, writes it
// to learnOutput, and uploads the raw observed JSON to S3 for future aggregation.
func runLearnPostExit(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, sandboxID, learnOutput string) error {
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string {
			t := cfg.SandboxTableName
			if t == "" {
				t = "km-sandboxes"
			}
			return t
		}())
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox for learn: %w", err)
	}

	var observedJSON []byte

	switch rec.Substrate {
	case "ec2", "ec2spot", "ec2demand":
		// Trigger SIGUSR1 on the eBPF enforcer to flush observations to disk + S3.
		fmt.Fprintln(os.Stderr, "Flushing eBPF observations...")
		if flushErr := flushEC2Observations(ctx, cfg, sandboxID); flushErr != nil {
			log.Warn().Err(flushErr).Msg("learn: flush via SIGUSR1 failed (will try S3 anyway)")
		}
		observedJSON, err = fetchEC2ObservedJSON(ctx, cfg, sandboxID)
		if err != nil {
			return err
		}

	case "docker":
		dnsContainer := fmt.Sprintf("km-%s-dns-proxy", sandboxID)
		httpContainer := fmt.Sprintf("km-%s-http-proxy", sandboxID)

		dnsBuf := &bytes.Buffer{}
		httpBuf := &bytes.Buffer{}

		if dnsOut, dnsErr := exec.CommandContext(ctx, "docker", "logs", dnsContainer).Output(); dnsErr == nil {
			dnsBuf = bytes.NewBuffer(dnsOut)
		} else {
			log.Warn().Err(dnsErr).Str("container", dnsContainer).Msg("learn: failed to get DNS proxy logs (non-fatal)")
		}
		if httpOut, httpErr := exec.CommandContext(ctx, "docker", "logs", httpContainer).Output(); httpErr == nil {
			httpBuf = bytes.NewBuffer(httpOut)
		} else {
			log.Warn().Err(httpErr).Str("container", httpContainer).Msg("learn: failed to get HTTP proxy logs (non-fatal)")
		}

		observedJSON, err = CollectDockerObservations(sandboxID, dnsBuf, httpBuf)
		if err != nil {
			return fmt.Errorf("collect docker observations: %w", err)
		}

		// Upload observed JSON to S3 for future aggregation.
		uploadLearnSession(ctx, cfg, sandboxID, observedJSON)

	case "ecs":
		fmt.Fprintln(os.Stderr, "Learning mode is not yet supported on ECS substrate. Use EC2 or Docker.")
		return nil

	default:
		return fmt.Errorf("unsupported substrate %q for --learn", rec.Substrate)
	}

	yamlBytes, err := GenerateProfileFromJSON(observedJSON, "")
	if err != nil {
		return fmt.Errorf("generate profile: %w", err)
	}

	if err := os.WriteFile(learnOutput, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write profile to %s: %w", learnOutput, err)
	}

	fmt.Fprintf(os.Stderr, "\nGenerated SandboxProfile: %s\nReview and apply with: km validate %s\n", learnOutput, learnOutput)
	return nil
}

// flushEC2Observations sends SIGUSR1 to the eBPF enforcer on the instance,
// triggering it to write observed state to disk and upload to S3.
// We wait briefly for the S3 upload to complete.
func flushEC2Observations(ctx context.Context, cfg *config.Config, sandboxID string) error {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	// Look up instance ID from DynamoDB record.
	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	fetcher := newRealFetcher(awsCfg, cfg.StateBucket, tableName)
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find instance: %w", err)
	}

	ssmClient := ssm.NewFromConfig(awsCfg)
	cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {"pkill -USR1 -f 'km ebpf-attach' && sleep 3"},
		},
	})
	if err != nil {
		return fmt.Errorf("send SIGUSR1 via SSM: %w", err)
	}

	// Wait for the command to complete.
	waiter := ssm.NewCommandExecutedWaiter(ssmClient)
	if waitErr := waiter.Wait(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  cmdOut.Command.CommandId,
		InstanceId: awssdk.String(instanceID),
	}, 30*time.Second); waitErr != nil {
		return fmt.Errorf("wait for flush command: %w", waitErr)
	}

	return nil
}

// fetchEC2ObservedJSON fetches the observed JSON from S3 (primary) or SSM RunCommand
// (fallback) after the sandbox session exits.
func fetchEC2ObservedJSON(ctx context.Context, cfg *config.Config, sandboxID string) ([]byte, error) {
	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		bucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	if bucket == "" {
		return nil, fmt.Errorf("no artifacts bucket configured (set KM_ARTIFACTS_BUCKET or artifacts_bucket in km-config.yaml)")
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)

	prefix := fmt.Sprintf("learn/%s/", sandboxID)
	listOut, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: awssdk.String(bucket),
		Prefix: awssdk.String(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("list S3 learn sessions (prefix %s): %w", prefix, err)
	}

	if len(listOut.Contents) == 0 {
		return nil, fmt.Errorf("no observation data found. Ensure the sandbox was created with learning mode enabled (--observe flag on ebpf-attach)")
	}

	// Find the most recent key (latest timestamp lexicographically).
	latestKey := ""
	for _, obj := range listOut.Contents {
		if obj.Key != nil && (latestKey == "" || *obj.Key > latestKey) {
			latestKey = *obj.Key
		}
	}

	getOut, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(latestKey),
	})
	if err != nil {
		return nil, fmt.Errorf("download observed JSON from S3 key %s: %w", latestKey, err)
	}
	defer getOut.Body.Close()

	data, err := io.ReadAll(getOut.Body)
	if err != nil {
		return nil, fmt.Errorf("read observed JSON from S3: %w", err)
	}
	return data, nil
}

// uploadLearnSession uploads the observed JSON to S3 at learn/{sandboxID}/{timestamp}.json.
// Failures are logged as warnings but do not abort the profile generation.
func uploadLearnSession(ctx context.Context, cfg *config.Config, sandboxID string, data []byte) {
	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		bucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	if bucket == "" {
		log.Warn().Msg("learn: KM_ARTIFACTS_BUCKET not set, skipping S3 upload of Docker observations")
		return
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		log.Warn().Err(err).Msg("learn: failed to load AWS config for S3 upload")
		return
	}
	s3Client := s3.NewFromConfig(awsCfg)

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	key := fmt.Sprintf("learn/%s/%s.json", sandboxID, timestamp)
	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(data),
		ContentType: awssdk.String("application/json"),
	})
	if putErr != nil {
		log.Warn().Err(putErr).Str("key", key).Msg("learn: S3 upload of Docker observations failed (non-fatal)")
		return
	}
	log.Info().Str("key", key).Msg("learn: uploaded Docker observations to S3")
}
