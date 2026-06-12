// Command km-github is the sandbox-side Phase 97/98 GitHub API client.
// It reads the per-sandbox GitHub installation token from SSM
// (/{resource_prefix}/sandbox/{id}/github-token) and posts comments,
// reviews, check runs, or opens pull requests back to GitHub via the REST API.
//
// Subcommands:
//
//	km-github comment --repo owner/repo --number N --body "..."
//	  → POST /repos/{owner}/{repo}/issues/{N}/comments
//
//	km-github review  --repo owner/repo --number N --event APPROVE|COMMENT|REQUEST_CHANGES \
//	                  --body "..." [--commit-id <sha>]
//	  → POST /repos/{owner}/{repo}/pulls/{N}/reviews
//
//	km-github check   --repo owner/repo --name "check-name" --conclusion success|failure|neutral \
//	                  --summary "..." --head-sha <sha> [--number N]
//	  → POST /repos/{owner}/{repo}/check-runs
//
//	km-github pr create --repo owner/repo --title "..." --head <branch> --base <branch> [--body "..."]
//	  → POST /repos/{owner}/{repo}/pulls — prints html_url to stdout
//
// Required env: KM_SANDBOX_ID, AWS_REGION (or AWS_DEFAULT_REGION).
// KM_RESOURCE_PREFIX defaults to "km" (matches the km operator prefix).
//
// The per-sandbox token is fetched from SSM at
// /{KM_RESOURCE_PREFIX}/sandbox/{KM_SANDBOX_ID}/github-token.
// This is the write-scoped installation token written by the km create flow
// (Phase 97 Plan 02). The token expiry is handled by the token refresher Lambda;
// if the token is expired the API call will return 401 and the error is logged.
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

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

const defaultTimeout = 30 * time.Second

// validReviewEvents is the set of GitHub Pull Request review event types.
// COMMENT and REQUEST_CHANGES require a non-empty body; APPROVE does not.
var validReviewEvents = map[string]bool{
	"APPROVE":          true,
	"COMMENT":          true,
	"REQUEST_CHANGES":  true,
}

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stderr))
}

// dispatch routes a subcommand argument vector to the matching implementation.
// Extracted from main() so tests can drive the dispatch table without os.Args.
func dispatch(args []string, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "comment":
		return runComment(args[1:], stderr)
	case "review":
		return runReview(args[1:], stderr)
	case "check":
		return runCheck(args[1:], stderr)
	case "pr":
		if len(args) < 2 {
			usage(stderr)
			return 2
		}
		switch args[1] {
		case "create":
			return runPRCreate(args[2:], stderr)
		default:
			fmt.Fprintf(stderr, "unknown pr subcommand: %q\n", args[1])
			usage(stderr)
			return 2
		}
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
	fmt.Fprintln(w, `usage: km-github <subcommand> [args]
Subcommands:
  comment    Post a comment to a pull request or issue (issues:write).
             --repo owner/repo --number N --body "..."
  review     Submit a pull request review (pull_requests:write).
             --repo owner/repo --number N --event APPROVE|COMMENT|REQUEST_CHANGES
             --body "..." [--commit-id <sha>]
  check      Post a CI check run (checks:write).
             --repo owner/repo --name "check-name" --conclusion success|failure|neutral
             --summary "..." --head-sha <sha> [--number N]
  pr create  Open a new pull request (pull_requests:write).
             --repo owner/repo --title "..." --head <branch> --base <branch> [--body "..."]
             Prints the html_url of the created PR to stdout.`)
}

// runComment is the comment subcommand entry point. It validates flags, loads
// the per-sandbox token from SSM, and calls runCommentWith.
func runComment(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repo, body string
	var number int
	fs.StringVar(&repo, "repo", "", "Repository in owner/repo format (required)")
	fs.StringVar(&body, "body", "", "Comment body text (required)")
	fs.IntVar(&number, "number", 0, "Issue/PR number (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if repo == "" || number == 0 || body == "" {
		fmt.Fprintln(stderr, "km-github comment: --repo, --number, and --body are required")
		return 2
	}

	token, err := loadToken(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-github comment: load token: %v\n", err)
		return 1
	}

	body = attributionFooter(body, os.Getenv(replyAgentEnv))
	return runCommentWith(repo, number, body, token, os.Getenv(turnIDEnv), stderr)
}

// replyAgentEnv is set by the GitHub inbound poller (exported inline into the
// agent's dispatch shell) to the EFFECTIVE_AGENT for the turn — "claude" or
// "codex". It is the signal that a comment/review is an agent-dispatched reply.
const replyAgentEnv = "KM_GITHUB_REPLY_AGENT"

// turnIDEnv is set by the GitHub inbound poller (exported inline into the agent's
// dispatch shell) to the poller's per-turn RUN_ID. It keys the per-turn
// idempotency marker that suppresses duplicate posts when the agent invokes
// km-github comment/review twice in one turn (github-bridge double-post fix).
// Empty for manual km-github invocations → marker + duplicate-check disabled.
const turnIDEnv = "KM_GITHUB_TURN_ID"

// attributionFooter appends a "via <Agent>" footer so a reader can tell whether
// Claude or Codex produced an agent-dispatched reply (Phase 102 follow-up).
// agent is the KM_GITHUB_REPLY_AGENT value; an empty or unknown value leaves the
// body byte-identical (manual km-github invocations are never decorated).
func attributionFooter(body, agent string) string {
	var label string
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "codex":
		label = "Codex"
	case "claude":
		label = "Claude"
	default:
		return body
	}
	return body + "\n\n<sub>🤖 via " + label + "</sub>"
}

// appendTurnMarker appends the hidden per-turn idempotency marker to body. An
// empty body (e.g. a bodyless APPROVE review) becomes the marker alone so even
// that post is idempotent; a non-empty body keeps a blank-line separator so the
// marker stays invisible in rendered markdown.
func appendTurnMarker(body, marker string) string {
	if body == "" {
		return marker
	}
	return body + "\n\n" + marker
}

// runCommentWith is the testable inner entry point for the comment subcommand.
// Tests inject a token and point GitHubAPIBaseURL at an httptest server.
//
// turnID is the per-turn idempotency key (the poller's RUN_ID via
// KM_GITHUB_TURN_ID). When non-empty, the helper scans the issue's existing
// comments for this turn's hidden marker and no-ops if it is already present
// (the agent posted the same body earlier this turn), then appends the marker to
// the body it posts. Empty turnID disables the guard (manual km-github use).
func runCommentWith(repo string, number int, body, token, turnID string, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	if marker := github.TurnMarker(turnID); marker != "" {
		exists, err := github.CommentMarkerExists(ctx, repo, number, token, marker)
		switch {
		case err != nil:
			// Fail open: a failed read must not strand a legitimate reply.
			fmt.Fprintf(stderr, "km-github comment: duplicate-check failed, posting anyway: %v\n", err)
		case exists:
			fmt.Fprintf(stderr, "km-github comment: duplicate suppressed (km-turn:%s already posted)\n", turnID)
			return 0
		}
		body = appendTurnMarker(body, marker)
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", github.GitHubAPIBaseURL, repo, number)
	payload := map[string]string{"body": body}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-github comment: marshal body: %v\n", err)
		return 1
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(stderr, "km-github comment: build request: %v\n", err)
		return 1
	}
	addGitHubHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github comment: request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github comment: GitHub API returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}
	return 0
}

// runReview is the review subcommand entry point. It validates flags, loads
// the per-sandbox token from SSM, and calls runReviewWith.
func runReview(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repo, event, body, commitID string
	var number int
	fs.StringVar(&repo, "repo", "", "Repository in owner/repo format (required)")
	fs.StringVar(&event, "event", "", "Review event: APPROVE|COMMENT|REQUEST_CHANGES (required)")
	fs.StringVar(&body, "body", "", "Review body text (required for COMMENT and REQUEST_CHANGES)")
	fs.StringVar(&commitID, "commit-id", "", "Commit SHA to attach the review to (optional)")
	fs.IntVar(&number, "number", 0, "Pull request number (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if repo == "" || number == 0 || event == "" {
		fmt.Fprintln(stderr, "km-github review: --repo, --number, and --event are required")
		return 2
	}

	token, err := loadToken(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-github review: load token: %v\n", err)
		return 1
	}

	// Only decorate a non-empty review body — a bodyless APPROVE stays bodyless.
	if body != "" {
		body = attributionFooter(body, os.Getenv(replyAgentEnv))
	}
	return runReviewWith(repo, number, event, body, commitID, token, os.Getenv(turnIDEnv), stderr)
}

// reviewPayload is the JSON body for the GitHub Pull Request review API.
// commit_id is intentionally omitted when empty to avoid sending a null value.
type reviewPayload struct {
	Event    string `json:"event"`
	Body     string `json:"body,omitempty"`
	CommitID string `json:"commit_id,omitempty"`
}

// runReviewWith is the testable inner entry point for the review subcommand.
// Tests inject a token and point GitHubAPIBaseURL at an httptest server.
//
// turnID drives the same per-turn idempotency guard as runCommentWith: when
// non-empty the helper scans the PR's existing reviews for this turn's marker,
// no-ops if present, and appends the marker to the posted body. Empty disables it.
func runReviewWith(repo string, number int, event, body, commitID, token, turnID string, stderr io.Writer) int {
	// Validate event.
	if !validReviewEvents[event] {
		fmt.Fprintf(stderr, "km-github review: invalid --event %q; valid values: APPROVE, COMMENT, REQUEST_CHANGES\n", event)
		return 1
	}

	// COMMENT and REQUEST_CHANGES require a non-empty body.
	if (event == "COMMENT" || event == "REQUEST_CHANGES") && body == "" {
		fmt.Fprintf(stderr, "km-github review: --body is required when --event is %s\n", event)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	if marker := github.TurnMarker(turnID); marker != "" {
		exists, err := github.ReviewMarkerExists(ctx, repo, number, token, marker)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "km-github review: duplicate-check failed, posting anyway: %v\n", err)
		case exists:
			fmt.Fprintf(stderr, "km-github review: duplicate suppressed (km-turn:%s already posted)\n", turnID)
			return 0
		}
		body = appendTurnMarker(body, marker)
	}

	url := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", github.GitHubAPIBaseURL, repo, number)
	payload := reviewPayload{
		Event:    event,
		Body:     body,
		CommitID: commitID,
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-github review: marshal body: %v\n", err)
		return 1
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(stderr, "km-github review: build request: %v\n", err)
		return 1
	}
	addGitHubHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github review: request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github review: GitHub API returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}
	return 0
}

// validCheckConclusions is the set of GitHub Check Run conclusion values supported
// by km-github check. GitHub allows more (e.g. "skipped", "timed_out"), but
// the sandbox agent only needs pass/fail/neutral.
var validCheckConclusions = map[string]bool{
	"success": true,
	"failure": true,
	"neutral": true,
}

// runCheck is the check subcommand entry point. It validates flags, loads
// the per-sandbox token from SSM, and calls runCheckWith.
func runCheck(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repo, name, conclusion, summary, headSHA string
	var number int
	fs.StringVar(&repo, "repo", "", "Repository in owner/repo format (required)")
	fs.StringVar(&name, "name", "", "Check run name (required)")
	fs.StringVar(&conclusion, "conclusion", "", "Conclusion: success|failure|neutral (required)")
	fs.StringVar(&summary, "summary", "", "Summary text for the check run output block")
	fs.StringVar(&headSHA, "head-sha", "", "Commit SHA the check run is associated with (required)")
	fs.IntVar(&number, "number", 0, "Pull request number (optional, for context only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if repo == "" || name == "" || conclusion == "" {
		fmt.Fprintln(stderr, "km-github check: --repo, --name, and --conclusion are required")
		return 2
	}

	token, err := loadToken(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-github check: load token: %v\n", err)
		return 1
	}

	return runCheckWith(repo, number, name, conclusion, summary, headSHA, token, stderr)
}

// checkRunOutput is the nested output block in the GitHub Check Runs API payload.
type checkRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// checkRunPayload is the JSON body for the GitHub Check Runs API.
type checkRunPayload struct {
	Name       string         `json:"name"`
	HeadSHA    string         `json:"head_sha"`
	Status     string         `json:"status"`
	Conclusion string         `json:"conclusion"`
	Output     checkRunOutput `json:"output"`
}

// runCheckWith is the testable inner entry point for the check subcommand.
// Tests inject a token and point GitHubAPIBaseURL at an httptest server.
func runCheckWith(repo string, number int, name, conclusion, summary, headSHA, token string, stderr io.Writer) int {
	// Validate conclusion before making any HTTP call.
	if !validCheckConclusions[conclusion] {
		fmt.Fprintf(stderr, "km-github check: invalid --conclusion %q; valid values: success, failure, neutral\n", conclusion)
		return 1
	}
	// Validate head_sha — required by the GitHub Checks API (returns 422 otherwise).
	if headSHA == "" {
		fmt.Fprintf(stderr, "km-github check: --head-sha is required (GitHub API returns 422 without a commit SHA)\n")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	url := fmt.Sprintf("%s/repos/%s/check-runs", github.GitHubAPIBaseURL, repo)
	payload := checkRunPayload{
		Name:       name,
		HeadSHA:    headSHA,
		Status:     "completed",
		Conclusion: conclusion,
		Output: checkRunOutput{
			Title:   name, // use check name as the output title
			Summary: summary,
		},
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-github check: marshal body: %v\n", err)
		return 1
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(stderr, "km-github check: build request: %v\n", err)
		return 1
	}
	addGitHubHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github check: request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github check: GitHub API returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}
	return 0
}

// runPRCreate is the pr create subcommand entry point. It validates flags, loads
// the per-sandbox token from SSM, and calls runPRCreateWith.
func runPRCreate(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("pr create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repo, title, head, base, body string
	fs.StringVar(&repo, "repo", "", "Repository in owner/repo format (required)")
	fs.StringVar(&title, "title", "", "Pull request title (required)")
	fs.StringVar(&head, "head", "", "Branch to merge (required)")
	fs.StringVar(&base, "base", "", "Target branch (required)")
	fs.StringVar(&body, "body", "", "Pull request body text (optional)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if repo == "" || title == "" || head == "" || base == "" {
		fmt.Fprintln(stderr, "km-github pr create: --repo, --title, --head, and --base are required")
		return 2
	}

	token, err := loadToken(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-github pr create: load token: %v\n", err)
		return 1
	}

	return runPRCreateWith(repo, title, head, base, body, token, stderr, os.Stdout)
}

// prCreatePayload is the JSON body for the GitHub Pull Requests API.
type prCreatePayload struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body,omitempty"`
}

// prCreateResponse captures the fields we care about from the PR creation response.
type prCreateResponse struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

// runPRCreateWith is the testable inner entry point for the pr create subcommand.
// Tests inject a token, point GitHubAPIBaseURL at an httptest server, and capture
// stdout via the stdout writer. The html_url is written to stdout so the agent
// can read the created PR's URL.
func runPRCreateWith(repo, title, head, base, body, token string, stderr io.Writer, stdout io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	url := fmt.Sprintf("%s/repos/%s/pulls", github.GitHubAPIBaseURL, repo)
	payload := prCreatePayload{
		Title: title,
		Head:  head,
		Base:  base,
		Body:  body,
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(stderr, "km-github pr create: marshal body: %v\n", err)
		return 1
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(stderr, "km-github pr create: build request: %v\n", err)
		return 1
	}
	addGitHubHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github pr create: request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github pr create: GitHub API returned HTTP %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}

	// Decode the response and print the PR URL to stdout for the agent to read.
	var result prCreateResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
		fmt.Fprintf(stderr, "km-github pr create: decode response: %v\n", decodeErr)
		return 1
	}
	fmt.Fprintln(stdout, result.HTMLURL)
	return 0
}

// addGitHubHeaders sets the standard GitHub API headers on the request.
func addGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
}

// loadToken reads the per-sandbox GitHub installation token from SSM.
// The token is stored at /{resource_prefix}/sandbox/{sandbox_id}/github-token
// (written by the km create flow at Phase 97 Plan 02 time).
func loadToken(stderr io.Writer) (string, error) {
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if sandboxID == "" {
		return "", fmt.Errorf("KM_SANDBOX_ID not set")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return "", fmt.Errorf("AWS_REGION (or AWS_DEFAULT_REGION) not set")
	}
	resourcePrefix := os.Getenv("KM_RESOURCE_PREFIX")
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", fmt.Errorf("aws config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)

	paramName := fmt.Sprintf("/%s/sandbox/%s/github-token", resourcePrefix, sandboxID)
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(paramName),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm GetParameter %s: %w", paramName, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("ssm parameter %s missing value", paramName)
	}
	return *out.Parameter.Value, nil
}
