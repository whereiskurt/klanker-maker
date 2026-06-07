// Command km-github is the sandbox-side Phase 97 GitHub API client.
// It reads the per-sandbox GitHub installation token from SSM
// (/{resource_prefix}/sandbox/{id}/github-token) and posts comments or
// reviews back to GitHub via the REST API.
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
  comment  Post a comment to a pull request or issue (issues:write).
           --repo owner/repo --number N --body "..."
  review   Submit a pull request review (pull_requests:write).
           --repo owner/repo --number N --event APPROVE|COMMENT|REQUEST_CHANGES
           --body "..." [--commit-id <sha>]`)
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

	return runCommentWith(repo, number, body, token, stderr)
}

// runCommentWith is the testable inner entry point for the comment subcommand.
// Tests inject a token and point GitHubAPIBaseURL at an httptest server.
func runCommentWith(repo string, number int, body, token string, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

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

	return runReviewWith(repo, number, event, body, commitID, token, stderr)
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
func runReviewWith(repo string, number int, event, body, commitID, token string, stderr io.Writer) int {
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
