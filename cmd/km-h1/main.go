// Command km-h1 is the sandbox-side HackerOne customer-API client (Phase 103).
//
// It is the HackerOne analog of cmd/km-github: the bridge dispatches a sandbox
// agent turn; the agent reads the report and posts back to HackerOne through
// this helper. Unlike GitHub (App-JWT + per-sandbox installation token), the
// HackerOne customer API uses plain HTTP Basic Auth (API username + API token),
// so there is no token-refresh dance — the creds are read once from SSM (or the
// KM_H1_API_USER / KM_H1_API_TOKEN env exported by the poller).
//
// Subcommands:
//
//	km-h1 comment --report N --body @file [--reply-to-researcher]
//	  → POST /reports/{N}/activities  {"data":{"type":"activity-comment",
//	      "attributes":{"message":"...","internal":<true unless --reply-to-researcher>}}}
//	  internal defaults TRUE — the researcher-visible path must be explicit
//	  (--reply-to-researcher). This is safety layer 4 (CONTEXT § Reply path).
//
//	km-h1 read --report N
//	  → GET /reports/{N}  — prints the JSON report to stdout.
//
//	km-h1 state --report N --to <state>
//	  → POST /reports/{N}/state_changes  (best-effort; see Task 2 / OQ2 note).
//
// Required env: AWS_REGION (or AWS_DEFAULT_REGION) when creds come from SSM;
// KM_RESOURCE_PREFIX defaults to "km". Basic-Auth creds are loaded from
// KM_H1_API_USER / KM_H1_API_TOKEN if set, else from SSM at
// /{prefix}/config/h1/api-username and /{prefix}/config/h1/api-token.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const defaultTimeout = 30 * time.Second

// defaultH1APIBaseURL is the HackerOne customer API base URL. The v1 segment is
// part of the base so the per-verb paths stay /reports/{id}/... .
const defaultH1APIBaseURL = "https://api.hackerone.com/v1"

// runConfig carries the per-invocation knobs that tests override (base URL,
// creds, stdout sink, backoff schedule). Production main() passes none of these
// and the loaders fill them from env/SSM.
type runConfig struct {
	baseURL string
	user    string
	token   string
	credsOK bool
	stdout  io.Writer
	backoff []time.Duration
}

// option mutates a runConfig. Tests inject options; production passes nothing.
type option func(*runConfig)

func withBaseURL(u string) option { return func(c *runConfig) { c.baseURL = u } }
func withCreds(user, token string) option {
	return func(c *runConfig) { c.user, c.token, c.credsOK = user, token, true }
}
func withStdout(w io.Writer) option        { return func(c *runConfig) { c.stdout = w } }
func withBackoff(b []time.Duration) option { return func(c *runConfig) { c.backoff = b } }

// defaultBackoff is the 429/5xx retry ladder (mirrors km-github's 1s/2s/4s).
var defaultBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// noBackoff is the zero-delay ladder used by tests so a 429-retry path does not
// sleep real seconds.
var noBackoff = []time.Duration{0, 0, 0}

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stderr))
}

// dispatch routes a subcommand argument vector to the matching implementation.
// Extracted from main() so tests drive the dispatch table without os.Args and
// inject base URL / creds / stdout / backoff via options.
func dispatch(args []string, stderr io.Writer, opts ...option) int {
	cfg := &runConfig{
		baseURL: defaultH1APIBaseURL,
		stdout:  os.Stdout,
		backoff: defaultBackoff,
	}
	for _, o := range opts {
		o(cfg)
	}

	if len(args) < 1 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "comment":
		return runComment(args[1:], stderr, cfg)
	case "read":
		return runRead(args[1:], stderr, cfg)
	case "state":
		return runState(args[1:], stderr, cfg)
	case "-h", "--help", "help":
		usage(stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: km-h1 <subcommand> [args]
Subcommands:
  comment  Post a comment to a HackerOne report.
           --report N --body @file [--reply-to-researcher]
           Posts an INTERNAL comment by default (safety layer); pass
           --reply-to-researcher to post a researcher-visible (external) reply.
  read     Fetch a report and print its JSON.
           --report N
  state    Change a report's state (best-effort).
           --report N --to <state>`)
}

// resolveCreds returns the Basic-Auth user/token, preferring test-injected
// creds, then KM_H1_API_USER/KM_H1_API_TOKEN env, then SSM.
func resolveCreds(cfg *runConfig, stderr io.Writer) (string, string, error) {
	if cfg.credsOK {
		return cfg.user, cfg.token, nil
	}
	if u, t := os.Getenv("KM_H1_API_USER"), os.Getenv("KM_H1_API_TOKEN"); u != "" && t != "" {
		return u, t, nil
	}
	return loadCredsFromSSM(stderr)
}

// runComment is the comment subcommand entry point. internal defaults TRUE; the
// researcher-visible path requires the explicit --reply-to-researcher flag.
func runComment(args []string, stderr io.Writer, cfg *runConfig) int {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var bodyArg string
	var report int
	var replyToResearcher bool
	// --internal defaults true and is the safety default; it exists so an
	// operator can be explicit. --reply-to-researcher overrides it to false.
	internal := true
	fs.IntVar(&report, "report", 0, "HackerOne report ID (required)")
	fs.StringVar(&bodyArg, "body", "", "Comment body as @file (required)")
	fs.BoolVar(&internal, "internal", true, "Post an internal (non-researcher-visible) comment (default true)")
	fs.BoolVar(&replyToResearcher, "reply-to-researcher", false, "Post a researcher-visible (external) comment — must be explicit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if report == 0 || bodyArg == "" {
		fmt.Fprintln(stderr, "km-h1 comment: --report and --body are required")
		return 2
	}

	body, err := readBodyArg(bodyArg)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 comment: read body: %v\n", err)
		return 1
	}

	// internal-by-default at the JSON-marshalling layer: only an explicit
	// --reply-to-researcher flips it to false. The default flag value is also
	// true, so there is no path that silently posts external.
	postInternal := internal
	if replyToResearcher {
		postInternal = false
	}

	user, token, err := resolveCreds(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 comment: load creds: %v\n", err)
		return 1
	}

	return runCommentWith(report, body, postInternal, user, token, cfg, stderr)
}

// activityAttributes is the attributes block of a HackerOne activity-comment.
type activityAttributes struct {
	Message  string `json:"message"`
	Internal bool   `json:"internal"`
}

// activityData is the data block of the activities POST body.
type activityData struct {
	Type       string             `json:"type"`
	Attributes activityAttributes `json:"attributes"`
}

// activityRequest is the full POST /reports/{id}/activities body.
type activityRequest struct {
	Data activityData `json:"data"`
}

// runCommentWith is the testable inner entry point for the comment subcommand.
func runCommentWith(report int, body string, internal bool, user, token string, cfg *runConfig, stderr io.Writer) int {
	url := fmt.Sprintf("%s/reports/%d/activities", cfg.baseURL, report)
	payload := activityRequest{
		Data: activityData{
			Type: "activity-comment",
			Attributes: activityAttributes{
				Message:  body,
				Internal: internal,
			},
		},
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 comment: marshal body: %v\n", err)
		return 1
	}
	return doWithRetry(http.MethodPost, url, reqBody, user, token, cfg, stderr, nil)
}

// runRead is the read subcommand entry point.
func runRead(args []string, stderr io.Writer, cfg *runConfig) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var report int
	fs.IntVar(&report, "report", 0, "HackerOne report ID (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if report == 0 {
		fmt.Fprintln(stderr, "km-h1 read: --report is required")
		return 2
	}

	user, token, err := resolveCreds(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 read: load creds: %v\n", err)
		return 1
	}

	url := fmt.Sprintf("%s/reports/%d", cfg.baseURL, report)
	return doWithRetry(http.MethodGet, url, nil, user, token, cfg, stderr, cfg.stdout)
}

// runState is the state subcommand entry point.
//
// OQ2 note (103-CAPTURE/field-paths.md): the state-change endpoint is
// LOW-confidence — it could not be pinned from a webhook capture (it is an
// outbound customer-API call). This implements the most-likely candidate,
// POST /reports/{id}/state_changes, and keeps the verb thin so a fast-follow
// can correct the endpoint/body against the live HackerOne Sandbox program in
// Plan 10. state is the least-critical verb (read + comment are the core path).
func runState(args []string, stderr io.Writer, cfg *runConfig) int {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var report int
	var to string
	fs.IntVar(&report, "report", 0, "HackerOne report ID (required)")
	fs.StringVar(&to, "to", "", "Target state, e.g. triaged (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if report == 0 || to == "" {
		fmt.Fprintln(stderr, "km-h1 state: --report and --to are required")
		return 2
	}

	user, token, err := resolveCreds(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 state: load creds: %v\n", err)
		return 1
	}

	return runStateWith(report, to, user, token, cfg, stderr)
}

// stateChangeRequest is the POST /reports/{id}/state_changes body (best-effort
// candidate per OQ2 — confirm against the live Sandbox program in Plan 10).
type stateChangeRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			State string `json:"state"`
		} `json:"attributes"`
	} `json:"data"`
}

// stateEndpointPath returns the state-change path for a report. It defaults to
// the OQ2 best-effort candidate /reports/{id}/state_changes but honors a
// KM_H1_STATE_ENDPOINT override (a printf template taking the report id, e.g.
// "/reports/%d") so a fast-follow can repoint the endpoint against the live
// HackerOne Sandbox program WITHOUT rebuilding the helper. See OQ2.
func stateEndpointPath(report int) string {
	if tmpl := os.Getenv("KM_H1_STATE_ENDPOINT"); tmpl != "" && strings.Contains(tmpl, "%d") {
		return fmt.Sprintf(tmpl, report)
	}
	return fmt.Sprintf("/reports/%d/state_changes", report)
}

func runStateWith(report int, to, user, token string, cfg *runConfig, stderr io.Writer) int {
	url := cfg.baseURL + stateEndpointPath(report)
	var payload stateChangeRequest
	payload.Data.Type = "state-change"
	payload.Data.Attributes.State = to
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-h1 state: marshal body: %v\n", err)
		return 1
	}
	return doWithRetry(http.MethodPost, url, reqBody, user, token, cfg, stderr, nil)
}

// doWithRetry performs the HTTP request with Basic Auth and a 429/5xx backoff
// ladder. On a 2xx, if printTo is non-nil the response body is written there.
func doWithRetry(method, url string, reqBody []byte, user, token string, cfg *runConfig, stderr io.Writer, printTo io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	// attempts = 1 initial + len(backoff) retries.
	backoff := cfg.backoff
	if backoff == nil {
		backoff = defaultBackoff
	}

	var lastStatus int
	var lastRespBody []byte
	for attempt := 0; attempt <= len(backoff); attempt++ {
		var reader io.Reader
		if reqBody != nil {
			reader = bytes.NewReader(reqBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			fmt.Fprintf(stderr, "km-h1: build request: %v\n", err)
			return 1
		}
		req.SetBasicAuth(user, token)
		req.Header.Set("Accept", "application/json")
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(stderr, "km-h1: request: %v\n", err)
			return 1
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastStatus = resp.StatusCode
		lastRespBody = respBody

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if printTo != nil {
				fmt.Fprintln(printTo, strings.TrimRight(string(respBody), "\n"))
			}
			return 0
		}

		// Retry on 429 and 5xx with backoff; other 4xx are terminal.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt < len(backoff) {
				select {
				case <-ctx.Done():
					fmt.Fprintf(stderr, "km-h1: context cancelled during backoff\n")
					return 1
				case <-time.After(backoff[attempt]):
				}
				continue
			}
		}
		break
	}

	fmt.Fprintf(stderr, "km-h1: HackerOne API returned HTTP %d: %s\n", lastStatus, string(lastRespBody))
	return 1
}

// readBodyArg reads a comment body. Per the CLAUDE.md OpenSSL/stdin constraint
// the body is supplied as @file (mirrors km-github's file-based body handling);
// a leading '@' is required so the value is never an inline shell-expanded
// string. A bare value (no '@') is treated as a literal for convenience.
func readBodyArg(arg string) (string, error) {
	if strings.HasPrefix(arg, "@") {
		path := strings.TrimPrefix(arg, "@")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return arg, nil
}

// loadCredsFromSSM reads the HackerOne Basic-Auth username + token from SSM at
// /{prefix}/config/h1/api-username and /{prefix}/config/h1/api-token.
func loadCredsFromSSM(stderr io.Writer) (string, string, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return "", "", fmt.Errorf("AWS_REGION (or AWS_DEFAULT_REGION) not set")
	}
	prefix := os.Getenv("KM_RESOURCE_PREFIX")
	if prefix == "" {
		prefix = "km"
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", "", fmt.Errorf("aws config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)

	get := func(name string) (string, error) {
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           awssdk.String(name),
			WithDecryption: awssdk.Bool(true),
		})
		if err != nil {
			return "", fmt.Errorf("ssm GetParameter %s: %w", name, err)
		}
		if out.Parameter == nil || out.Parameter.Value == nil {
			return "", fmt.Errorf("ssm parameter %s missing value", name)
		}
		return *out.Parameter.Value, nil
	}

	user, err := get(fmt.Sprintf("/%s/config/h1/api-username", prefix))
	if err != nil {
		return "", "", err
	}
	token, err := get(fmt.Sprintf("/%s/config/h1/api-token", prefix))
	if err != nil {
		return "", "", err
	}
	return user, token, nil
}
