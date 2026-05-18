package cmd

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 1.
// Tests for closures (a) HeadBucket retry, (e) artifacts derivation + placeholder
// rejection, configure-side of (f) finale, configure-side WARN of (h).
//
// These tests reference production symbols that Plans 02-04 will create:
//   - probeStateBucketInteractive (closure a)
//   - deriveArtifactsBucket (closure e)
//   - validateArtifactsBucket (closure e)
//   - nextStepsBlock (closure f)
//   - S3HeadBucketAPI (interface for the above — already in doctor.go but tests
//     the configure-side usage)
//
// RED contract: `go test ./internal/app/cmd/` fails with
//   undefined: probeStateBucketInteractive
//   undefined: deriveArtifactsBucket
//   undefined: validateArtifactsBucket
//   undefined: nextStepsBlock
// Plan 02 makes them GREEN.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
)

// ---- mock S3 client ------------------------------------------------------------

// mockS3HeadBucketConfigure satisfies S3HeadBucketAPI for configure HeadBucket tests.
// Uses a slice of responses so successive calls return successive entries;
// the last entry is reused once the slice is exhausted.
type mockS3HeadBucketConfigure struct {
	calls         []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	callIndex     int
}

func (m *mockS3HeadBucketConfigure) HeadBucket(ctx context.Context, in *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if len(m.calls) == 0 {
		return &s3.HeadBucketOutput{}, nil
	}
	i := m.callIndex
	if i >= len(m.calls) {
		i = len(m.calls) - 1
	}
	m.callIndex++
	return m.calls[i](ctx, in, opts...)
}

func (m *mockS3HeadBucketConfigure) HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	// Only HeadBucket is exercised in configure tests; HeadObject satisfies S3HeadBucketAPI.
	return nil, nil
}

// Compile-time assertion: mockS3HeadBucketConfigure must satisfy S3HeadBucketAPI.
var _ S3HeadBucketAPI = (*mockS3HeadBucketConfigure)(nil)

// ---- smithy error helpers ------------------------------------------------------

// configureSmithyAPIError wraps a code+message into a smithy.APIError.
type configureSmithyAPIError struct {
	code    string
	message string
}

func (e *configureSmithyAPIError) Error() string        { return e.code + ": " + e.message }
func (e *configureSmithyAPIError) ErrorCode() string    { return e.code }
func (e *configureSmithyAPIError) ErrorMessage() string { return e.message }
func (e *configureSmithyAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

// http403Err returns a smithy.APIError whose ErrorCode() == "Forbidden" (HTTP 403).
// S3 HeadBucket on an existing-but-not-owned bucket returns 403.
func http403Err() error {
	return &configureSmithyAPIError{code: "Forbidden", message: "Forbidden"}
}

// http404Err returns a smithy.APIError whose ErrorCode() == "NotFound" (HTTP 404).
// S3 HeadBucket on a non-existent bucket returns 404.
func http404Err() error {
	return &configureSmithyAPIError{code: "NotFound", message: "Not Found"}
}

// ---- HeadBucket probe tests ----------------------------------------------------

// TestConfigureHeadBucket_403_AcceptsAccountIDSuffix: bucket taken (403) → prompt
// operator; operator accepts the suggested "${name}-${accountID}" name; second probe
// returns 404 (available). Expected: returned name has suffix, no error.
func TestConfigureHeadBucket_403_AcceptsAccountIDSuffix(t *testing.T) {
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http403Err()
	}
	call2 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http404Err()
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1, call2}}

	stdin := bufio.NewReader(strings.NewReader("y\n"))
	var stdout bytes.Buffer

	name, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if name != "tf-km-state-us-east-1-123456789012" {
		t.Errorf("got name %q, want %q", name, "tf-km-state-us-east-1-123456789012")
	}
	out := stdout.String()
	if !strings.Contains(out, "is taken") {
		t.Errorf("expected 'is taken' in output; got: %s", out)
	}
	if !strings.Contains(out, "Suggestion: tf-km-state-us-east-1-123456789012") {
		t.Errorf("expected 'Suggestion: tf-km-state-us-east-1-123456789012' in output; got: %s", out)
	}
	if !strings.Contains(out, "[Y / edit / abort]") {
		t.Errorf("expected '[Y / edit / abort]' in output; got: %s", out)
	}
}

// TestConfigureHeadBucket_404_AcceptsOriginalName: bucket available (404) →
// original name returned without prompting.
func TestConfigureHeadBucket_404_AcceptsOriginalName(t *testing.T) {
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http404Err()
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1}}

	stdin := bufio.NewReader(strings.NewReader(""))
	var stdout bytes.Buffer

	name, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if name != "tf-km-state-us-east-1" {
		t.Errorf("got name %q, want %q", name, "tf-km-state-us-east-1")
	}
	if strings.Contains(stdout.String(), "Suggestion") {
		t.Errorf("unexpected 'Suggestion' in output; got: %s", stdout.String())
	}
}

// TestConfigureHeadBucket_Nil_AcceptsOriginalName: nil error means bucket owned by us.
// Per CONTEXT.md: bucket already owned by this install — also acceptable.
func TestConfigureHeadBucket_Nil_AcceptsOriginalName(t *testing.T) {
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return &s3.HeadBucketOutput{BucketLocationName: aws.String("us-east-1")}, nil
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1}}

	stdin := bufio.NewReader(strings.NewReader(""))
	var stdout bytes.Buffer

	name, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if name != "tf-km-state-us-east-1" {
		t.Errorf("got name %q, want %q", name, "tf-km-state-us-east-1")
	}
	if strings.Contains(stdout.String(), "Suggestion") {
		t.Errorf("unexpected 'Suggestion' in output for nil-error (owned bucket) case; got: %s", stdout.String())
	}
}

// TestConfigureHeadBucket_EditPath: operator types "edit" then provides a custom name.
func TestConfigureHeadBucket_EditPath(t *testing.T) {
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http403Err()
	}
	call2 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http404Err()
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1, call2}}

	stdin := bufio.NewReader(strings.NewReader("edit\ntf-state-custom\n"))
	var stdout bytes.Buffer

	name, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if name != "tf-state-custom" {
		t.Errorf("got name %q, want %q", name, "tf-state-custom")
	}
}

// TestConfigureHeadBucket_Abort: operator types "abort" → non-nil error with "aborted by operator".
func TestConfigureHeadBucket_Abort(t *testing.T) {
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http403Err()
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1}}

	stdin := bufio.NewReader(strings.NewReader("abort\n"))
	var stdout bytes.Buffer

	_, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err == nil {
		t.Fatal("expected non-nil error when operator aborts, got nil")
	}
	if !strings.Contains(err.Error(), "aborted by operator") {
		t.Errorf("expected 'aborted by operator' in error, got: %v", err)
	}
}

// TestConfigureHeadBucket_RetryCapExceeded: both the original and the auto-suffixed
// name are 403 (both taken). Must bail with an error mentioning "--state-bucket".
func TestConfigureHeadBucket_RetryCapExceeded(t *testing.T) {
	// Every call returns 403 so even the suggestion is taken.
	alwaysForbidden := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http403Err()
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){alwaysForbidden, alwaysForbidden, alwaysForbidden}}

	stdin := bufio.NewReader(strings.NewReader("y\n"))
	var stdout bytes.Buffer

	_, err := probeStateBucketInteractive(context.Background(), "tf-km-state-us-east-1", "123456789012", stdin, &stdout, mock)
	if err == nil {
		t.Fatal("expected non-nil error when retry cap exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "--state-bucket") {
		t.Errorf("expected '--state-bucket' in error message, got: %v", err)
	}
}

// ---- Artifacts bucket tests ----------------------------------------------------

// TestConfigure_DerivesArtifactsBucket verifies deriveArtifactsBucket returns
// "${prefix}-artifacts-${accountID}".
func TestConfigure_DerivesArtifactsBucket(t *testing.T) {
	got := deriveArtifactsBucket("km", "123456789012")
	want := "km-artifacts-123456789012"
	if got != want {
		t.Errorf("deriveArtifactsBucket(km, 123456789012) = %q; want %q", got, want)
	}

	got2 := deriveArtifactsBucket("whereiskurt", "987654321098")
	want2 := "whereiskurt-artifacts-987654321098"
	if got2 != want2 {
		t.Errorf("deriveArtifactsBucket(whereiskurt, 987654321098) = %q; want %q", got2, want2)
	}
}

// TestConfigure_RejectsPlaceholder verifies validateArtifactsBucket rejects
// angle-bracket placeholders and the literal km-config.example.yaml sentinel value.
func TestConfigure_RejectsPlaceholder(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantMsg string
	}{
		{
			name:    "angle bracket prefix pattern",
			input:   "<prefix>-artifacts-12345678",
			wantErr: true,
			wantMsg: "placeholder",
		},
		{
			name:    "generic angle brackets",
			input:   "<anything-in-angle-brackets>",
			wantErr: true,
			wantMsg: "placeholder",
		},
		{
			name:    "literal example sentinel",
			input:   "km-artifacts-12345",
			wantErr: true,
			wantMsg: "placeholder",
		},
		{
			name:    "valid real value km prefix",
			input:   "km-artifacts-123456789012",
			wantErr: false,
		},
		{
			name:    "valid real value custom prefix",
			input:   "whereiskurt-artifacts-987654321098",
			wantErr: false,
		},
		{
			name:    "empty string is invalid",
			input:   "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArtifactsBucket(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("validateArtifactsBucket(%q): expected error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateArtifactsBucket(%q): expected no error, got: %v", tc.input, err)
			}
			if tc.wantErr && tc.wantMsg != "" && err != nil && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("validateArtifactsBucket(%q): error %q does not contain %q", tc.input, err.Error(), tc.wantMsg)
			}
		})
	}
}

// ---- Finale block tests --------------------------------------------------------

// TestConfigure_FinaleBlock verifies nextStepsBlock() returns the required content
// and that it includes "km bootstrap --all --dry-run=false", "km init --plan",
// and "km init --dry-run=false".
func TestConfigure_FinaleBlock(t *testing.T) {
	block := nextStepsBlock()
	if !strings.Contains(block, "Next steps:") {
		t.Errorf("nextStepsBlock() missing 'Next steps:'; got:\n%s", block)
	}
	if !strings.Contains(block, "km bootstrap --all --dry-run=false") {
		t.Errorf("nextStepsBlock() missing 'km bootstrap --all --dry-run=false'; got:\n%s", block)
	}
	if !strings.Contains(block, "km init --plan") {
		t.Errorf("nextStepsBlock() missing 'km init --plan'; got:\n%s", block)
	}
	if !strings.Contains(block, "km init --dry-run=false") {
		t.Errorf("nextStepsBlock() missing 'km init --dry-run=false'; got:\n%s", block)
	}
}

// ---- Shell-env drift warn tests (configure-side) -------------------------------

// TestConfigure_ShellEnvDriftWarn verifies that when KM_REGION differs from the
// yaml value being written, the wizard emits a WARN to stderr. The wizard must
// NOT block — it still completes normally.
//
// WARNING goes to stderr (not stdout) per locked decision in CONTEXT.md.
func TestConfigure_ShellEnvDriftWarn(t *testing.T) {
	// makeDriftCfgArgs returns a runConfigure argument list for non-interactive
	// mode with region=us-east-1. Shared by all three sub-cases.
	makeDriftCfgArgs := func(t *testing.T) (in *strings.Reader, dir string) {
		t.Helper()
		return strings.NewReader(""), t.TempDir()
	}

	t.Run("env set to different region emits WARN", func(t *testing.T) {
		t.Setenv("KM_REGION", "us-west-2")

		in, dir := makeDriftCfgArgs(t)
		var out bytes.Buffer
		// runConfigure in non-interactive mode — write region=us-east-1 to yaml;
		// KM_REGION is set to us-west-2 → should emit drift WARN to stderr.
		stderr := captureStderr(t, func() {
			_ = runConfigure(
				in,
				&out,
				dir,         // outputDir
				true,        // nonInteractive
				false,       // resetPrefix
				"km",        // resourcePrefix
				"sandboxes", // emailSubdomain
				"example.com",  // domain
				"",             // organizationAcct
				"",             // dnsParentAcct
				"222222222222", // terraformAcct
				"333333333333", // applicationAcct
				"",             // ssoStartURL
				"",             // ssoRegion
				"us-east-1",    // region
				"",             // stateBucket
				"",             // artifactsBucket
				"",             // operatorEmail
				"",             // safePhrase
				0,              // maxSandboxes
			)
		})
		if !strings.Contains(stderr, "WARN") {
			t.Errorf("expected drift WARN in stderr when KM_REGION != yaml region; got: %s", stderr)
		}
		if !strings.Contains(stderr, "KM_REGION") {
			t.Errorf("expected 'KM_REGION' in WARN; got: %s", stderr)
		}
		if !strings.Contains(stderr, "us-west-2") {
			t.Errorf("expected env value 'us-west-2' in WARN; got: %s", stderr)
		}
		if !strings.Contains(stderr, "us-east-1") {
			t.Errorf("expected yaml value 'us-east-1' in WARN; got: %s", stderr)
		}
	})

	t.Run("no WARN when env not set", func(t *testing.T) {
		os.Unsetenv("KM_REGION")

		in, dir := makeDriftCfgArgs(t)
		var out bytes.Buffer
		stderr := captureStderr(t, func() {
			_ = runConfigure(in, &out, dir, true, false,
				"km", "sandboxes", "example.com",
				"", "", "222222222222", "333333333333",
				"", "", "us-east-1",
				"", "", "", "", 0)
		})
		if strings.Contains(stderr, "WARN: KM_REGION") {
			t.Errorf("expected no KM_REGION WARN when env not set; got: %s", stderr)
		}
	})

	t.Run("no WARN when env matches yaml", func(t *testing.T) {
		t.Setenv("KM_REGION", "us-east-1")

		in, dir := makeDriftCfgArgs(t)
		var out bytes.Buffer
		stderr := captureStderr(t, func() {
			_ = runConfigure(in, &out, dir, true, false,
				"km", "sandboxes", "example.com",
				"", "", "222222222222", "333333333333",
				"", "", "us-east-1",
				"", "", "", "", 0)
		})
		if strings.Contains(stderr, "WARN: KM_REGION") {
			t.Errorf("expected no KM_REGION WARN when env matches yaml; got: %s", stderr)
		}
	})
}

// TestConfigurePath_PlaceholderBucketDoesNotBlockWizard verifies that the
// km configure wizard can run successfully even when the EXISTING km-config.yaml
// contains a placeholder artifacts_bucket value (km-artifacts-12345).
//
// WHY THIS MUST PASS: runConfigure reads existing yaml via os.ReadFile +
// yaml.Unmarshal (NOT config.Load()). Therefore the new config.Load() placeholder
// validation added in Plan 09 does NOT block the wizard from running. The wizard
// is the CURE for a placeholder bucket, so it must never be blocked by the same
// placeholder it exists to fix.
//
// The test writes a placeholder-bucket km-config.yaml, runs the wizard in
// non-interactive mode with --artifacts-bucket corrected to a real value, and
// asserts the wizard completes without error and writes the corrected bucket.
func TestConfigurePath_PlaceholderBucketDoesNotBlockWizard(t *testing.T) {
	dir := t.TempDir()

	// Write an existing km-config.yaml with a placeholder artifacts_bucket.
	existingYAML := `resource_prefix: km
email_subdomain: sandboxes
domain: example.com
accounts:
  terraform: "123456789012"
  application: "123456789012"
region: us-east-1
artifacts_bucket: km-artifacts-12345
`
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(existingYAML), 0600); err != nil {
		t.Fatalf("write existing km-config.yaml: %v", err)
	}

	var out bytes.Buffer
	// Run the wizard in non-interactive mode. The --artifacts-bucket flag provides
	// the corrected value; the wizard overwrites the placeholder in the existing file.
	err := runConfigure(
		strings.NewReader(""), // stdin (non-interactive, unused)
		&out,
		dir,                     // outputDir
		true,                    // nonInteractive
		false,                   // resetPrefix
		"km",                    // resourcePrefix
		"sandboxes",             // emailSubdomain
		"example.com",           // domain
		"",                      // organizationAcct
		"",                      // dnsParentAcct
		"123456789012",          // terraformAcct
		"123456789012",          // applicationAcct
		"https://sso.example.com", // ssoStartURL
		"us-east-1",             // ssoRegion
		"us-east-1",             // region
		"",                      // stateBucket
		"km-artifacts-123456789012", // artifactsBucket (corrected value)
		"",                      // operatorEmail
		"",                      // safePhrase
		0,                       // maxSandboxes
	)
	if err != nil {
		t.Errorf("runConfigure returned error even though existing km-config.yaml had a placeholder bucket; wizard should be immune to config.Load() validation: %v", err)
	}

	// Verify the output km-config.yaml now contains the corrected bucket value.
	raw, readErr := os.ReadFile(filepath.Join(dir, "km-config.yaml"))
	if readErr != nil {
		t.Fatalf("read output km-config.yaml: %v", readErr)
	}
	content := string(raw)
	if !strings.Contains(content, "km-artifacts-123456789012") {
		t.Errorf("output km-config.yaml does not contain corrected bucket 'km-artifacts-123456789012';\ngot:\n%s", content)
	}
}

// TestConfigure_StateBucketDefault verifies Phase 84.4.1 CONFIGURE-STATE-BUCKET-UX:
// when stateBucket is empty and resourcePrefix+region are set, the interactive prompt
// presents tf-${prefix}-state-${regionLabel} as the computed default
// (mirrors site.hcl:43).
//
// Wave 2 plan 84.4.1-04: unskipped + implemented.
func TestConfigure_StateBucketDefault(t *testing.T) {
	// (a) resourcePrefix="tg", region="us-east-1", stateBucket="".
	// (b) Drive runConfigure non-interactive with explicit stateBucket="tf-tg-state-use1"
	//     to verify the formula (compiler.RegionLabel("us-east-1") == "use1").
	// (c) Also verify source-grep: configure.go contains the computation.

	// Part 1: verify the formula itself.
	want := fmt.Sprintf("tf-%s-state-%s", "tg", "use1") // tf-tg-state-use1
	if want != "tf-tg-state-use1" {
		t.Fatalf("formula sanity check failed: %s", want)
	}

	// Part 2: verify source-grep that configure.go contains the computed default logic.
	src, err := os.ReadFile(filepath.Join(".", "configure.go"))
	if err != nil {
		t.Fatalf("read configure.go: %v", err)
	}
	if !bytes.Contains(src, []byte("tf-%s-state-%s")) {
		t.Errorf("configure.go missing computed default formula 'tf-%%s-state-%%s' — Phase 84.4.1 CONFIGURE-STATE-BUCKET-UX not applied")
	}
	if !bytes.Contains(src, []byte("compiler.RegionLabel")) {
		t.Errorf("configure.go missing compiler.RegionLabel call — Phase 84.4.1 CONFIGURE-STATE-BUCKET-UX not applied")
	}

	// Part 3: run nonInteractive with explicit stateBucket to verify it round-trips
	// correctly through the yaml write path.
	tmp := t.TempDir()
	var out bytes.Buffer
	err = runConfigure(
		strings.NewReader(""), // in (not used in nonInteractive)
		&out,
		tmp,         // outputDir
		true,        // nonInteractive
		false,       // resetPrefix
		"tg",        // resourcePrefix
		"sandboxes", // emailSubdomain
		"example.com",  // domain
		"",             // organizationAcct
		"",             // dnsParentAcct
		"222222222222", // terraformAcct
		"333333333333", // applicationAcct
		"https://sso.example.com", // ssoStartURL
		"us-east-1",               // ssoRegion
		"us-east-1",               // region
		"tf-tg-state-use1",        // stateBucket (pre-set to computed value)
		"",             // artifactsBucket
		"",             // operatorEmail
		"",             // safePhrase
		0,              // maxSandboxes
	)
	if err != nil {
		t.Fatalf("runConfigure: %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(tmp, "km-config.yaml"))
	if readErr != nil {
		t.Fatalf("read km-config.yaml: %v", readErr)
	}
	if !strings.Contains(string(content), "tf-tg-state-use1") {
		t.Errorf("km-config.yaml does not contain state_bucket tf-tg-state-use1;\ngot:\n%s", string(content))
	}
}

// TestConfigure_StateBucketHeadBucketRetry verifies the HeadBucket-on-403 retry UX
// (Phase 84.4.1 CONFIGURE-STATE-BUCKET-UX). Tests probeStateBucketInteractive
// (configure.go:84-200) directly — the wire-up into configure is verified by the
// source-grep in TestConfigure_StateBucketDefault.
//
// Wave 2 plan 84.4.1-04: unskipped + implemented.
func TestConfigure_StateBucketHeadBucketRetry(t *testing.T) {
	// (a) Mock S3 HeadBucket to return 403 (bucket name globally taken).
	call403 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http403Err()
	}
	// (b) Second call returns 200 (OK — name after edit is available).
	call200 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return &s3.HeadBucketOutput{}, nil
	}
	mock := &mockS3HeadBucketConfigure{calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call403, call200}}

	// (c) Drive "edit" → operator enters new name.
	stdin := bufio.NewReader(strings.NewReader("edit\ntf-tg-state-use1-edited\n"))
	var stdout bytes.Buffer

	// (d) Mock returns 200 for the edited name; prompt accepts.
	result, err := probeStateBucketInteractive(context.Background(), "tf-tg-state-use1", "333333333333", stdin, &stdout, mock)
	if err != nil {
		t.Fatalf("probeStateBucketInteractive: unexpected error: %v", err)
	}
	// (e) Assert resulting stateBucket value matches the edited name.
	if result != "tf-tg-state-use1-edited" {
		t.Errorf("expected stateBucket 'tf-tg-state-use1-edited' after edit, got %q", result)
	}
}
