package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	ghpkg "github.com/whereiskurt/klankrmkr/pkg/github"
)

// GithubManifestBaseURL is the base URL for the GitHub manifest exchange API.
// Package-level var enables test injection (follows GitHubAPIBaseURL pattern from pkg/github/token.go).
var GithubManifestBaseURL = "https://api.github.com"

// SSMWriteAPI is a narrow interface for writing SSM parameters.
// The real *ssm.Client satisfies this interface directly.
// Defined here for DI in configure_github and configure_github_test.
type SSMWriteAPI interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// SSMReadWriteAPI combines read and write for the discover flow.
type SSMReadWriteAPI interface {
	SSMWriteAPI
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// NewConfigureGitHubCmd creates the "km configure github" subcommand using real SSM.
func NewConfigureGitHubCmd(cfg *config.Config) *cobra.Command {
	// SSM client is created lazily at RunE time so it does not fail at flag parse.
	var ssmClient SSMWriteAPI
	return newConfigureGitHubCmdCore(cfg, &ssmClient, os.Stdin, os.Stdout)
}

// NewConfigureGitHubCmdWithDeps creates the "km configure github" subcommand with
// injected dependencies for testability. The caller must supply a non-nil ssmClient.
func NewConfigureGitHubCmdWithDeps(cfg *config.Config, ssmClient SSMWriteAPI, in io.Reader, out io.Writer) *cobra.Command {
	// Wrap the caller's client so the core can update the pointer.
	fixed := ssmClient
	return newConfigureGitHubCmdCore(cfg, &fixed, in, out)
}

// newConfigureGitHubCmdCore builds the cobra.Command. ssmClientPtr is a pointer to
// an SSMWriteAPI — for the real path it starts nil and is initialised inside RunE;
// for the test path it is pre-populated by NewConfigureGitHubCmdWithDeps.
func newConfigureGitHubCmdCore(cfg *config.Config, ssmClientPtr *SSMWriteAPI, in io.Reader, out io.Writer) *cobra.Command {
	var (
		nonInteractive bool
		appClientID    string
		privateKeyFile string
		installationID string
		force          bool
		setup          bool
		discover       bool
	)

	cmd := &cobra.Command{
		Use:   "github",
		Short: "Configure GitHub App credentials for sandbox source-access tokens",
		Long: `Interactive wizard to store GitHub App credentials in SSM Parameter Store.

Credentials stored:
  /km/config/github/app-client-id   — GitHub App client ID
  /km/config/github/private-key     — GitHub App private key (SecureString)
  /km/config/github/installation-id — Installation ID for the target org/account

These parameters are read at km create time to generate scoped installation tokens.

Flags:
  --setup  One-click GitHub App creation via the GitHub manifest flow. Opens a
           browser, waits for the callback, exchanges the code for credentials,
           and stores them in SSM automatically — no manual copy-paste required.
           If the browser does not open, copy the printed URL and open it manually.
           After App creation, if no installations exist, you will be prompted to
           install the App first, then run:
             km configure github --installation-id <ID>`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()

			// Initialise real SSM client if not injected (production path).
			if *ssmClientPtr == nil {
				awsProfile := "klanker-terraform"
				awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
				if err != nil {
					return fmt.Errorf("failed to load AWS config: %w", err)
				}
				*ssmClientPtr = ssm.NewFromConfig(awsCfg)
			}

			// Ensure in/out have usable defaults.
			reader := in
			if reader == nil {
				reader = os.Stdin
			}
			writer := out
			if writer == nil {
				writer = os.Stdout
			}

			if setup {
				// --setup: launch the manifest flow
				return runConfigureGitHubSetupInteractive(ctx, *ssmClientPtr, writer, cfg, force)
			}

			if discover {
				// --discover: read credentials from SSM, fetch installations, store ID
				rwClient, ok := (*ssmClientPtr).(SSMReadWriteAPI)
				if !ok {
					return fmt.Errorf("SSM client does not support GetParameter (needed for --discover)")
				}
				return RunDiscoverInstallation(ctx, rwClient, writer, force)
			}

			return runConfigureGitHub(ctx, *ssmClientPtr, writer, reader,
				nonInteractive, appClientID, privateKeyFile, installationID, force, cfg)
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Skip prompts; use flag values directly")
	cmd.Flags().StringVar(&appClientID, "app-client-id", "",
		"GitHub App client ID (e.g. Iv1.abc123)")
	cmd.Flags().StringVar(&privateKeyFile, "private-key-file", "",
		"Path to the GitHub App private key PEM file")
	cmd.Flags().StringVar(&installationID, "installation-id", "",
		"GitHub App installation ID for the target org/user")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite existing SSM parameters (default: refuse if already set)")
	cmd.Flags().BoolVar(&setup, "setup", false,
		"One-click GitHub App creation via manifest flow (opens browser, stores credentials automatically)")
	cmd.Flags().BoolVar(&discover, "discover", false,
		"Auto-discover installation ID from existing App credentials in SSM")

	_ = cfg // reserved for future use (e.g. KMS key from config)

	return cmd
}

// runConfigureGitHub implements the configure github wizard logic.
func runConfigureGitHub(ctx context.Context, ssmClient SSMWriteAPI, out io.Writer, in io.Reader,
	nonInteractive bool, appClientID, privateKeyFile, installationID string, force bool, cfg *config.Config) error {

	if nonInteractive {
		// Validate required flags
		missing := []string{}
		if appClientID == "" {
			missing = append(missing, "--app-client-id")
		}
		if privateKeyFile == "" {
			missing = append(missing, "--private-key-file")
		}
		if installationID == "" {
			missing = append(missing, "--installation-id")
		}
		if len(missing) > 0 {
			return fmt.Errorf("--non-interactive requires: %s", joinStrings(missing))
		}
	} else {
		scanner := bufio.NewScanner(in)
		var err error

		appClientID, err = prompt(out, scanner, "GitHub App Client ID (e.g. Iv1.abc123)", appClientID)
		if err != nil {
			return err
		}
		privateKeyFile, err = prompt(out, scanner, "Path to private key PEM file", privateKeyFile)
		if err != nil {
			return err
		}
		installationID, err = prompt(out, scanner, "Installation ID", installationID)
		if err != nil {
			return err
		}
	}

	// Read PEM from file
	pemBytes, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return fmt.Errorf("reading private key file %q: %w", privateKeyFile, err)
	}

	// Validate PEM can be decoded
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("private key file %q does not contain a valid PEM block", privateKeyFile)
	}

	// Determine KMS key ARN for SecureString encryption.
	// Use platform KMS key from config if available; SSM uses the default service key otherwise.
	kmsKeyID := ""
	_ = cfg // no KMS ARN field on Config yet — SSM default service key is used

	overwrite := force

	// Write /km/config/github/app-client-id
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/app-client-id",
		appClientID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing app-client-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/app-client-id\n")

	// Write /km/config/github/private-key (SecureString)
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/private-key",
		string(pemBytes), ssmtypes.ParameterTypeSecureString, kmsKeyID, overwrite); err != nil {
		return fmt.Errorf("writing private-key to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/private-key (SecureString)\n")

	// Write /km/config/github/installation-id
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/installation-id",
		installationID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing installation-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/installation-id\n")

	fmt.Fprintf(out, "GitHub App credentials stored. Run 'km create' with a profile that has sourceAccess.github to use them.\n")
	return nil
}

// RunDiscoverInstallation reads the App credentials from SSM, fetches installations
// from the GitHub API, and stores the installation ID(s) in SSM.
// Exported for testability.
func RunDiscoverInstallation(ctx context.Context, client SSMReadWriteAPI, out io.Writer, force bool) error {
	// Read app-client-id (used as the App ID / JWT issuer for the installations API).
	clientIDOut, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String("/km/config/github/app-client-id"),
	})
	if err != nil {
		return fmt.Errorf("reading app-client-id from SSM: %w\nRun 'km configure github --setup' first", err)
	}
	appClientID := aws.ToString(clientIDOut.Parameter.Value)

	// Read private key (needed to sign the JWT for the GitHub API).
	privKeyOut, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/km/config/github/private-key"),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("reading private-key from SSM: %w", err)
	}
	pemKey := aws.ToString(privKeyOut.Parameter.Value)

	fmt.Fprintf(out, "Fetching installations for App %s...\n", appClientID)

	installations, err := fetchInstallations(ctx, GithubManifestBaseURL, appClientID, pemKey)
	if err != nil {
		return fmt.Errorf("fetching installations: %w\nMake sure the App is installed on your account/org first", err)
	}

	if len(installations) == 0 {
		return fmt.Errorf("no installations found\nInstall the App on your account/org first, then re-run --discover")
	}

	// Show all installations and use the first one.
	for i, inst := range installations {
		marker := " "
		if i == 0 {
			marker = ">"
		}
		fmt.Fprintf(out, "  %s [%d] %s\n", marker, inst.ID, inst.Account)
	}

	// Write per-account installation keys for ALL installations.
	for _, inst := range installations {
		instID := strconv.FormatInt(inst.ID, 10)
		paramPath := fmt.Sprintf("/km/config/github/installations/%s", inst.Account)
		if err := putSSMParam(ctx, client, paramPath,
			instID, ssmtypes.ParameterTypeString, "", force); err != nil {
			return fmt.Errorf("writing per-account installation key to SSM: %w", err)
		}
		fmt.Fprintf(out, "Written: %s (%s)\n", paramPath, instID)
	}

	// Write legacy /km/config/github/installation-id with the first installation (backward compat).
	installID := strconv.FormatInt(installations[0].ID, 10)
	if err := putSSMParam(ctx, client, "/km/config/github/installation-id",
		installID, ssmtypes.ParameterTypeString, "", force); err != nil {
		return fmt.Errorf("writing installation-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/installation-id (%s)\n", installID)
	fmt.Fprintf(out, "GitHub App configuration complete.\n")
	return nil
}

// putSSMParam writes a single SSM parameter.
// If kmsKeyID is empty, SSM uses the default service key for SecureString.
func putSSMParam(ctx context.Context, client SSMWriteAPI, name, value string,
	paramType ssmtypes.ParameterType, kmsKeyID string, overwrite bool) error {

	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      paramType,
		Overwrite: aws.Bool(overwrite),
	}
	if kmsKeyID != "" {
		input.KeyId = aws.String(kmsKeyID)
	}

	_, err := client.PutParameter(ctx, input)
	return err
}

// joinStrings joins a slice of strings with ", ".
func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}

// ============================================================
// Manifest flow — --setup flag implementation
// ============================================================

// manifestConversionResponse holds the fields returned by GitHub's manifest code exchange API.
type manifestConversionResponse struct {
	ID                 int64  `json:"id"`
	ClientID           string `json:"client_id"`
	PEM                string `json:"pem"`
	WebhookSecret      string `json:"webhook_secret"`
	HTMLURL            string `json:"html_url"`
	InstallationsCount int    `json:"installations_count"`
}

// installationInfo holds minimal installation data returned by /app/installations.
type installationInfo struct {
	ID      int64  `json:"id"`
	Account string // parsed from account.login
}

// BuildManifestJSON returns the JSON body for the GitHub App manifest flow.
// The redirectURL is included in the manifest body so GitHub redirects the browser
// back to the local callback server with the code query parameter.
func BuildManifestJSON(redirectURL string) string {
	manifest := map[string]interface{}{
		"name":   "klanker-maker-sandbox",
		"url":    "https://github.com/whereiskurt/klankrmkr",
		"public": true,
		"default_permissions": map[string]interface{}{
			"contents":      "read",
			"pull_requests": "write",
		},
		"hook_attributes": map[string]interface{}{
			"url":    "https://example.com",
			"active": false,
		},
		"redirect_url": redirectURL,
	}
	b, _ := json.Marshal(manifest)
	return string(b)
}

// openBrowser attempts to open a URL in the system browser. Non-fatal — callers
// print the URL so the operator can copy it if browser open fails.
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// ReceiveManifestCode starts a local HTTP server on a random port, serves
// /github-app-setup, and waits up to timeoutSeconds for GitHub to call back
// with the manifest code. Returns (code, port, error).
func ReceiveManifestCode(ctx context.Context, timeoutSeconds int) (string, int, error) {
	return ReceiveManifestCodeWithPortCb(ctx, timeoutSeconds, func(int) {})
}

// ReceiveManifestCodeWithPortCb is the testable variant of ReceiveManifestCode.
// portCb is called with the bound port immediately after the listener is ready,
// allowing tests to send a request before the timeout fires.
//
// The server serves two routes:
//   - GET /start — renders an HTML page that auto-POSTs the manifest JSON to GitHub's
//     App creation endpoint. GitHub's manifest flow requires a form POST, not a GET
//     with a query parameter.
//   - GET /github-app-setup — receives the redirect callback from GitHub with the
//     authorization code after App creation.
func ReceiveManifestCodeWithPortCb(ctx context.Context, timeoutSeconds int, portCb func(int)) (string, int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", 0, fmt.Errorf("manifest callback: listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	codeCh := make(chan string, 1)

	// Build the manifest JSON with the redirect URL pointing back to this server.
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/github-app-setup", port)
	manifestJSON := BuildManifestJSON(redirectURL)

	mux := http.NewServeMux()

	// /start serves an auto-submitting HTML form that POSTs the manifest to GitHub.
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>Klanker Maker — GitHub App Setup</title></head>
<body>
<p>Redirecting to GitHub to create the App...</p>
<form id="manifest-form" action="https://github.com/settings/apps/new" method="post">
  <input type="hidden" name="manifest" value='%s'>
</form>
<script>document.getElementById('manifest-form').submit();</script>
</body></html>`, manifestJSON)
	})

	// /github-app-setup receives the callback from GitHub with the code.
	mux.HandleFunc("/github-app-setup", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "GitHub App created successfully! You can close this tab and return to your terminal.")
		// Non-blocking send — only the first code matters.
		select {
		case codeCh <- code:
		default:
		}
	})

	srv := &http.Server{Handler: mux}

	// Start serving in background.
	go func() {
		_ = srv.Serve(ln)
	}()

	// Notify caller of port so tests can send a request immediately.
	portCb(port)

	// Wait for code or timeout.
	timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer timer.Stop()

	select {
	case code := <-codeCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return code, port, nil
	case <-timer.C:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return "", port, fmt.Errorf("manifest callback: timed out after %d seconds waiting for GitHub callback", timeoutSeconds)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return "", port, fmt.Errorf("manifest callback: context cancelled: %w", ctx.Err())
	}
}

// ExchangeManifestCode posts to GitHub's manifest code exchange endpoint and returns
// the parsed App credentials. baseURL is injectable for tests.
func ExchangeManifestCode(ctx context.Context, baseURL, code string) (*manifestConversionResponse, error) {
	apiURL := fmt.Sprintf("%s/app-manifests/%s/conversions", baseURL, code)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("manifest exchange: create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("manifest exchange: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("manifest exchange: read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("manifest exchange: GitHub returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result manifestConversionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("manifest exchange: parse response: %w", err)
	}
	return &result, nil
}

// fetchInstallations calls /app/installations with a GitHub App JWT and returns
// the list of installations. baseURL is injectable for tests.
// appIDOrClientID can be the numeric App ID (as string) or the client ID string —
// GitHub accepts both as the JWT issuer.
func fetchInstallations(ctx context.Context, baseURL string, appIDOrClientID string, pemKey string) ([]installationInfo, error) {
	jwt, err := ghpkg.GenerateGitHubAppJWT(appIDOrClientID, []byte(pemKey))
	if err != nil {
		return nil, fmt.Errorf("fetch installations: generate JWT: %w", err)
	}

	apiURL := fmt.Sprintf("%s/app/installations", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch installations: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch installations: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch installations: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch installations: GitHub returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	// GitHub returns an array of installation objects. We parse only the fields we need.
	var raw []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("fetch installations: parse response: %w", err)
	}

	installations := make([]installationInfo, len(raw))
	for i, r := range raw {
		installations[i] = installationInfo{
			ID:      r.ID,
			Account: r.Account.Login,
		}
	}
	return installations, nil
}

// runConfigureGitHubSetupInteractive is the production entry point: it starts a local
// callback server, opens the browser, and waits up to 5 minutes for the code.
func runConfigureGitHubSetupInteractive(ctx context.Context, ssmClient SSMWriteAPI, out io.Writer, cfg *config.Config, force bool) error {
	// Start callback server first to get the port.
	portCh := make(chan int, 1)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		code, _, err := ReceiveManifestCodeWithPortCb(ctx, 5*60, func(p int) {
			portCh <- p
		})
		if err != nil {
			errCh <- err
		} else {
			codeCh <- code
		}
	}()

	// Wait for port
	var port int
	select {
	case p := <-portCh:
		port = p
	case err := <-errCh:
		return fmt.Errorf("manifest setup: start callback server: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	startURL := fmt.Sprintf("http://127.0.0.1:%d/start", port)

	fmt.Fprintf(out, "Opening GitHub App creation page in your browser...\n")
	fmt.Fprintf(out, "If the browser does not open, copy this URL:\n\n  %s\n\n", startURL)
	fmt.Fprintf(out, "Waiting for callback on http://127.0.0.1:%d/github-app-setup ...\n", port)

	if err := openBrowser(startURL); err != nil {
		// Non-fatal — operator can use the printed URL.
		fmt.Fprintf(out, "(Browser open failed: %v — please copy the URL above)\n", err)
	}

	// Wait for the manifest code
	var code string
	select {
	case c := <-codeCh:
		code = c
	case err := <-errCh:
		return fmt.Errorf("manifest setup: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	return RunConfigureGitHubSetup(ctx, ssmClient, out, cfg, force, GithubManifestBaseURL, code)
}

// RunConfigureGitHubSetup is the testable core of the manifest setup flow.
// It exchanges the code for App credentials and stores them in SSM.
// baseURL and code are injected so tests can use an httptest server.
func RunConfigureGitHubSetup(ctx context.Context, ssmClient SSMWriteAPI, out io.Writer, cfg *config.Config, force bool, baseURL, code string) error {
	// Exchange the code for App credentials.
	appCreds, err := ExchangeManifestCode(ctx, baseURL, code)
	if err != nil {
		return fmt.Errorf("manifest setup: exchange code: %w", err)
	}

	fmt.Fprintf(out, "GitHub App created: %s\n", appCreds.HTMLURL)

	kmsKeyID := ""
	_ = cfg // no KMS ARN field on Config yet — SSM default service key is used
	overwrite := force

	// Write App client ID.
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/app-client-id",
		appCreds.ClientID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("manifest setup: write app-client-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/app-client-id (%s)\n", appCreds.ClientID)

	// Write private key.
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/private-key",
		appCreds.PEM, ssmtypes.ParameterTypeSecureString, kmsKeyID, overwrite); err != nil {
		return fmt.Errorf("manifest setup: write private-key to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/private-key (SecureString)\n")

	// Try to fetch installations to get the installation ID.
	installations, err := fetchInstallations(ctx, baseURL, strconv.FormatInt(appCreds.ID, 10), appCreds.PEM)
	if err != nil {
		// Non-fatal — print guidance for operator to install manually.
		fmt.Fprintf(out, "Note: could not fetch installations: %v\n", err)
		installations = nil
	}

	if len(installations) > 0 {
		installID := strconv.FormatInt(installations[0].ID, 10)
		if err := putSSMParam(ctx, ssmClient, "/km/config/github/installation-id",
			installID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
			return fmt.Errorf("manifest setup: write installation-id to SSM: %w", err)
		}
		fmt.Fprintf(out, "Written: /km/config/github/installation-id (%s)\n", installID)
		fmt.Fprintf(out, "GitHub App credentials stored. Run 'km create' with a profile that has sourceAccess.github to use them.\n")
	} else {
		fmt.Fprintf(out, "\nNo installations found. Install the App on your organization or account:\n")
		fmt.Fprintf(out, "  1. Visit %s/installations/new\n", appCreds.HTMLURL)
		fmt.Fprintf(out, "  2. Select the organization or account to install on\n")
		fmt.Fprintf(out, "  3. Note the installation ID from the URL (e.g. github.com/apps/.../installations/<ID>)\n")
		fmt.Fprintf(out, "  4. Run: km configure github --installation-id <ID>\n")
	}

	return nil
}
