// commit.go — the km-github commit subcommand.
//
// Creates a VERIFIED, bot-attributed commit through GitHub's GraphQL
// createCommitOnBranch mutation. This is the only GitHub commit path that
// auto-signs: a local `git commit` on the sandbox is unsigned, and the low-level
// REST `POST /git/commits` is bot-attributed but `verified:false, reason:unsigned`.
// createCommitOnBranch signs the commit with GitHub's key and attributes it to the
// token's own identity (klanker-maker[bot], committer GitHub) — no hardcoded bot
// email, so it is portable across installs.
//
// Usage:
//
//	km-github commit --repo owner/repo --branch BR [--parent SHA] \
//	                 --message-file MSG -- <repo-relative files...>
//	  → prints the new commit OID to stdout; the verification line to stderr.
//
// After a commit, sync the local worktree with:
//
//	git fetch origin && git reset --hard origin/<BR>
//
// ⚠ --parent force-resets the branch to SHA before committing. If a PR is already
// open and the head is reset to exactly the base SHA, GitHub auto-closes the PR
// ("no commits between base and head") — `gh pr reopen`, or create signed commits
// BEFORE opening the PR.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

// createCommitMutation is the GraphQL mutation that creates a signed commit.
const createCommitMutation = `mutation($input: CreateCommitOnBranchInput!){createCommitOnBranch(input:$input){commit{oid}}}`

// commitFile is a single file to add to the commit: the repo-relative path plus
// the raw (un-encoded) contents. runCommitWith base64-encodes the contents.
type commitFile struct {
	Path    string
	Content []byte
}

// runCommit is the commit subcommand entry point. It validates flags, reads the
// message file and each positional file from disk, loads the per-sandbox token
// from SSM, and calls runCommitWith.
func runCommit(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repo, branch, parent, msgFile string
	fs.StringVar(&repo, "repo", "", "Repository in owner/repo format (required)")
	fs.StringVar(&branch, "branch", "", "Branch to commit onto (required)")
	fs.StringVar(&parent, "parent", "", "Force-reset the branch to this SHA before committing (optional)")
	fs.StringVar(&msgFile, "message-file", "", "File whose first line is the headline, rest the body (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := fs.Args()
	if repo == "" || branch == "" || msgFile == "" || len(files) == 0 {
		fmt.Fprintln(stderr, "km-github commit: --repo, --branch, --message-file, and at least one file are required")
		return 2
	}

	rawMsg, err := os.ReadFile(msgFile)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: read message file %s: %v\n", msgFile, err)
		return 1
	}
	headline, body := splitCommitMessage(string(rawMsg))
	if headline == "" {
		fmt.Fprintf(stderr, "km-github commit: message file %s has an empty headline\n", msgFile)
		return 1
	}

	cfiles := make([]commitFile, 0, len(files))
	for _, f := range files {
		content, readErr := os.ReadFile(f)
		if readErr != nil {
			fmt.Fprintf(stderr, "km-github commit: read file %s: %v\n", f, readErr)
			return 1
		}
		cfiles = append(cfiles, commitFile{Path: f, Content: content})
	}

	token, err := loadToken(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: load token: %v\n", err)
		return 1
	}

	return runCommitWith(repo, branch, parent, headline, body, token, cfiles, stderr, os.Stdout)
}

// splitCommitMessage splits a raw commit message into a headline (first line) and
// a body (the remaining lines with leading blank lines stripped and trailing
// whitespace trimmed). Mirrors the bash `head -n1` / `tail -n+2 | sed '/./,$!d'`.
func splitCommitMessage(raw string) (headline, body string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 {
		headline = strings.TrimRight(lines[0], " \t")
	}
	rest := ""
	if len(lines) > 1 {
		rest = strings.Join(lines[1:], "\n")
	}
	// Strip leading blank lines, then trailing whitespace.
	rest = strings.TrimLeft(rest, "\n")
	body = strings.TrimRight(rest, " \t\n")
	return headline, body
}

// commitFileAddition is a GraphQL FileAddition: a repo-relative path and the
// base64-encoded file contents.
type commitFileAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// runCommitWith is the testable inner entry point. Tests inject a token, point
// github.GitHubAPIBaseURL at an httptest server, and capture stdout/stderr.
//
// Flow: (1) optional --parent force-reset PATCH of the branch ref; (2) GET the
// branch's head OID (expectedHeadOid, GraphQL's optimistic-concurrency guard);
// (3) POST the createCommitOnBranch mutation with base64 file additions; (4) GET
// the new commit and surface its verification status to stderr; (5) print the new
// OID to stdout.
func runCommitWith(repo, branch, parent, headline, body, token string, files []commitFile, stderr io.Writer, stdout io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	// (1) Optional force-reset the branch to --parent before committing.
	if parent != "" {
		if code := forceResetBranch(ctx, repo, branch, parent, token, stderr); code != 0 {
			return code
		}
	}

	// (2) Fetch the current head OID for expectedHeadOid.
	headOID, code := fetchHeadOID(ctx, repo, branch, token, stderr)
	if code != 0 {
		return code
	}

	// (3) Build the GraphQL input and POST the mutation.
	additions := make([]commitFileAddition, 0, len(files))
	for _, f := range files {
		additions = append(additions, commitFileAddition{
			Path:     f.Path,
			Contents: base64.StdEncoding.EncodeToString(f.Content),
		})
	}
	input := map[string]any{
		"branch": map[string]string{
			"repositoryNameWithOwner": repo,
			"branchName":              branch,
		},
		"message":         map[string]string{"headline": headline, "body": body},
		"expectedHeadOid": headOID,
		"fileChanges": map[string]any{
			"additions": additions,
			"deletions": []any{},
		},
	}
	newOID, code := createSignedCommit(ctx, input, token, stderr)
	if code != 0 {
		return code
	}

	// (4) Best-effort: surface the verification status. A failed read here must
	// not fail the command — the commit already exists.
	printCommitVerification(ctx, repo, newOID, token, stderr)

	// (5) The new commit OID is the machine-readable result.
	fmt.Fprintln(stdout, newOID)
	return 0
}

// forceResetBranch PATCHes the branch ref to the given SHA with force=true.
func forceResetBranch(ctx context.Context, repo, branch, sha, token string, stderr io.Writer) int {
	url := fmt.Sprintf("%s/repos/%s/git/refs/heads/%s", github.GitHubAPIBaseURL, repo, branch)
	payload, _ := json.Marshal(map[string]any{"sha": sha, "force": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: build force-reset request: %v\n", err)
		return 1
	}
	addGitHubHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: force-reset request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github commit: force-reset returned HTTP %d: %s\n", resp.StatusCode, string(b))
		return 1
	}
	return 0
}

// fetchHeadOID GETs the branch ref and returns its object SHA.
func fetchHeadOID(ctx context.Context, repo, branch, token string, stderr io.Writer) (string, int) {
	url := fmt.Sprintf("%s/repos/%s/git/ref/heads/%s", github.GitHubAPIBaseURL, repo, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: build ref request: %v\n", err)
		return "", 1
	}
	addGitHubHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: ref request: %v\n", err)
		return "", 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github commit: ref GET returned HTTP %d: %s\n", resp.StatusCode, string(b))
		return "", 1
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		fmt.Fprintf(stderr, "km-github commit: decode ref: %v\n", err)
		return "", 1
	}
	if ref.Object.SHA == "" {
		fmt.Fprintln(stderr, "km-github commit: branch ref has no head SHA")
		return "", 1
	}
	return ref.Object.SHA, 0
}

// createSignedCommit POSTs the createCommitOnBranch mutation and returns the new
// commit OID.
func createSignedCommit(ctx context.Context, input map[string]any, token string, stderr io.Writer) (string, int) {
	reqBody, err := json.Marshal(map[string]any{
		"query":     createCommitMutation,
		"variables": map[string]any{"input": input},
	})
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: marshal mutation: %v\n", err)
		return "", 1
	}
	url := github.GitHubAPIBaseURL + "/graphql"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: build mutation request: %v\n", err)
		return "", 1
	}
	addGitHubHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: mutation request: %v\n", err)
		return "", 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "km-github commit: GraphQL returned HTTP %d: %s\n", resp.StatusCode, string(b))
		return "", 1
	}
	var result struct {
		Data struct {
			CreateCommitOnBranch struct {
				Commit struct {
					OID string `json:"oid"`
				} `json:"commit"`
			} `json:"createCommitOnBranch"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(stderr, "km-github commit: decode mutation response: %v\n", err)
		return "", 1
	}
	if len(result.Errors) > 0 {
		msgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			msgs = append(msgs, e.Message)
		}
		fmt.Fprintf(stderr, "km-github commit: GraphQL errors: %s\n", strings.Join(msgs, "; "))
		return "", 1
	}
	oid := result.Data.CreateCommitOnBranch.Commit.OID
	if oid == "" {
		fmt.Fprintln(stderr, "km-github commit: mutation returned no commit OID")
		return "", 1
	}
	return oid, 0
}

// printCommitVerification GETs the new commit and prints its signature
// verification + authorship to stderr. Best-effort: any error is logged, never
// fatal (the commit already exists).
func printCommitVerification(ctx context.Context, repo, oid, token string, stderr io.Writer) {
	url := fmt.Sprintf("%s/repos/%s/git/commits/%s", github.GitHubAPIBaseURL, repo, oid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: (verify) build request: %v\n", err)
		return
	}
	addGitHubHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "km-github commit: (verify) request: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "km-github commit: (verify) HTTP %d\n", resp.StatusCode)
		return
	}
	var c struct {
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
		Author    struct{ Name string `json:"name"` } `json:"author"`
		Committer struct{ Name string `json:"name"` } `json:"committer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		fmt.Fprintf(stderr, "km-github commit: (verify) decode: %v\n", err)
		return
	}
	fmt.Fprintf(stderr, "verified=%t reason=%s author=%s committer=%s\n",
		c.Verification.Verified, c.Verification.Reason, c.Author.Name, c.Committer.Name)
}
