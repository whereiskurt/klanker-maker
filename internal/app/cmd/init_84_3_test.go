package cmd

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 2.
// Tests for closures (c) ENV-CONFIG-DRIFT-WARN, (f) init-side BOOTSTRAP-WORKFLOW-DISCOVERABILITY,
// and CONFIG-DISPLAY-VS-YAML-AUTHORITY accounts-still-exported invariant.
//
// These tests reference production symbols that Plan 04 will create:
//   - ensureArtifactsBucketExists (new func in cmd/init.go — closure f hard-fail)
//
// Existing production symbol exercised (drift WARN behavior is NEW — Plan 04 adds
// the drift-check code to ExportTerragruntEnvVars):
//   - ExportTerragruntEnvVars (init.go — existing func; Plan 04 adds WARN inside it)
//
// RED contract: `go test ./internal/app/cmd/` fails with
//   undefined: ensureArtifactsBucketExists
// Plan 04 makes them GREEN.
// The drift-WARN tests (TestExportTerragruntEnvVars_DriftWarn) will be RED at assertion
// level (no WARN emitted yet) rather than compile-fail, but are included here as the
// Wave 0 contract that Plan 04 must satisfy.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ---- ENV-CONFIG-DRIFT-WARN tests -----------------------------------------------

// TestExportTerragruntEnvVars_DriftWarn verifies that ExportTerragruntEnvVars emits
// a WARN line to stderr when a KM_* env var differs from the corresponding cfg field.
//
// WARN format per CONTEXT.md:
//   WARN: KM_REGION=us-west-2 (env) overrides km-config.yaml region=us-east-1
//
// Plan 04 adds this drift-check code to ExportTerragruntEnvVars in init.go.
func TestExportTerragruntEnvVars_DriftWarn(t *testing.T) {
	// Drift on KM_REGION
	t.Run("KM_REGION drift emits WARN", func(t *testing.T) {
		t.Setenv("KM_REGION", "us-west-2")
		// Clear any prior set so we get a fresh ExportTerragruntEnvVars call.
		os.Unsetenv("KM_ARTIFACTS_BUCKET")
		os.Unsetenv("KM_DOMAIN")
		os.Unsetenv("KM_RESOURCE_PREFIX")
		os.Unsetenv("KM_ROUTE53_ZONE_ID")
		os.Unsetenv("KM_REGION_LABEL")
		os.Unsetenv("KM_EMAIL_SUBDOMAIN")

		cfg := &config.Config{PrimaryRegion: "us-east-1"}

		stderr := captureStderr(t, func() {
			ExportTerragruntEnvVars(cfg)
		})

		if !strings.Contains(stderr, "WARN: KM_REGION=us-west-2") {
			t.Errorf("expected 'WARN: KM_REGION=us-west-2' in stderr; got: %s", stderr)
		}
		if !strings.Contains(stderr, "us-east-1") {
			t.Errorf("expected yaml value 'us-east-1' in drift WARN; got: %s", stderr)
		}

		// env still wins — os.Getenv must still return the env value
		if got := os.Getenv("KM_REGION"); got != "us-west-2" {
			t.Errorf("expected KM_REGION=us-west-2 after ExportTerragruntEnvVars, got %q", got)
		}
	})

	// Drift on KM_ARTIFACTS_BUCKET
	t.Run("KM_ARTIFACTS_BUCKET drift emits WARN", func(t *testing.T) {
		t.Setenv("KM_ARTIFACTS_BUCKET", "env-bucket")
		os.Unsetenv("KM_REGION")
		os.Unsetenv("KM_DOMAIN")
		os.Unsetenv("KM_RESOURCE_PREFIX")
		os.Unsetenv("KM_ROUTE53_ZONE_ID")
		os.Unsetenv("KM_REGION_LABEL")
		os.Unsetenv("KM_EMAIL_SUBDOMAIN")

		cfg := &config.Config{ArtifactsBucket: "yaml-bucket"}

		stderr := captureStderr(t, func() {
			ExportTerragruntEnvVars(cfg)
		})

		if !strings.Contains(stderr, "WARN: KM_ARTIFACTS_BUCKET=env-bucket") {
			t.Errorf("expected 'WARN: KM_ARTIFACTS_BUCKET=env-bucket' in stderr; got: %s", stderr)
		}
		if !strings.Contains(stderr, "yaml-bucket") {
			t.Errorf("expected yaml value 'yaml-bucket' in drift WARN; got: %s", stderr)
		}
	})
}

// TestExportTerragruntEnvVars_NoWarnOnMatch verifies NO WARN when the env var matches
// the cfg field value (no drift).
func TestExportTerragruntEnvVars_NoWarnOnMatch(t *testing.T) {
	t.Setenv("KM_REGION", "us-east-1")
	os.Unsetenv("KM_ARTIFACTS_BUCKET")
	os.Unsetenv("KM_DOMAIN")
	os.Unsetenv("KM_RESOURCE_PREFIX")
	os.Unsetenv("KM_ROUTE53_ZONE_ID")
	os.Unsetenv("KM_REGION_LABEL")
	os.Unsetenv("KM_EMAIL_SUBDOMAIN")

	cfg := &config.Config{PrimaryRegion: "us-east-1"}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	if strings.Contains(stderr, "WARN: KM_REGION") {
		t.Errorf("expected no KM_REGION WARN when env matches cfg; got: %s", stderr)
	}
}

// TestExportTerragruntEnvVars_NoWarnWhenUnset verifies NO WARN when the env var is
// not set at all (the function sets it from cfg — no drift to report).
func TestExportTerragruntEnvVars_NoWarnWhenUnset(t *testing.T) {
	os.Unsetenv("KM_REGION")
	os.Unsetenv("KM_ARTIFACTS_BUCKET")
	os.Unsetenv("KM_DOMAIN")
	os.Unsetenv("KM_RESOURCE_PREFIX")
	os.Unsetenv("KM_ROUTE53_ZONE_ID")
	os.Unsetenv("KM_REGION_LABEL")
	os.Unsetenv("KM_EMAIL_SUBDOMAIN")
	t.Cleanup(func() {
		// Also clean up what ExportTerragruntEnvVars may have set.
		os.Unsetenv("KM_REGION")
		os.Unsetenv("KM_REGION_LABEL")
	})

	cfg := &config.Config{PrimaryRegion: "us-east-1"}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	if strings.Contains(stderr, "WARN: KM_REGION") {
		t.Errorf("expected no KM_REGION WARN when env not set; got: %s", stderr)
	}
}

// TestExportTerragruntEnvVars_AccountsStillExported verifies that ExportTerragruntEnvVars
// still exports KM_ACCOUNTS_* to the environment even after the yaml-authoritative
// change (closure h). Yaml is authoritative for READS; EXPORTS still happen.
func TestExportTerragruntEnvVars_AccountsStillExported(t *testing.T) {
	os.Unsetenv("KM_ACCOUNTS_ORGANIZATION")
	os.Unsetenv("KM_ACCOUNTS_DNS_PARENT")
	os.Unsetenv("KM_ACCOUNTS_APPLICATION")
	os.Unsetenv("KM_REGION")
	os.Unsetenv("KM_ARTIFACTS_BUCKET")
	os.Unsetenv("KM_DOMAIN")
	os.Unsetenv("KM_RESOURCE_PREFIX")
	os.Unsetenv("KM_ROUTE53_ZONE_ID")
	os.Unsetenv("KM_REGION_LABEL")
	os.Unsetenv("KM_EMAIL_SUBDOMAIN")
	t.Cleanup(func() {
		os.Unsetenv("KM_ACCOUNTS_ORGANIZATION")
		os.Unsetenv("KM_ACCOUNTS_DNS_PARENT")
		os.Unsetenv("KM_ACCOUNTS_APPLICATION")
		os.Unsetenv("KM_REGION")
		os.Unsetenv("KM_REGION_LABEL")
	})

	cfg := &config.Config{
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
	}

	ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_ACCOUNTS_ORGANIZATION"); got != "111111111111" {
		t.Errorf("KM_ACCOUNTS_ORGANIZATION = %q, want %q", got, "111111111111")
	}
	if got := os.Getenv("KM_ACCOUNTS_DNS_PARENT"); got != "222222222222" {
		t.Errorf("KM_ACCOUNTS_DNS_PARENT = %q, want %q", got, "222222222222")
	}
	if got := os.Getenv("KM_ACCOUNTS_APPLICATION"); got != "333333333333" {
		t.Errorf("KM_ACCOUNTS_APPLICATION = %q, want %q", got, "333333333333")
	}
}

// ---- BOOTSTRAP-WORKFLOW-DISCOVERABILITY init-side test -------------------------

// TestRunInit_HardFailsMissingArtifactsBucket verifies that ensureArtifactsBucketExists
// returns a hard-fail error (not just a warning) when the artifacts bucket does not
// exist (404). The error must name both `km bootstrap --all` and `km bootstrap --shared-ses`
// so operators using either workflow can recover. It must also contain the bucket name.
//
// Plan 04 creates ensureArtifactsBucketExists in cmd/init.go.
func TestRunInit_HardFailsMissingArtifactsBucket(t *testing.T) {
	// mockS3HeadBucketConfigure is defined in configure_84_3_test.go (same package).
	// Reuse it here for the init-side test.
	call1 := func(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
		return nil, http404Err()
	}
	mock := &mockS3HeadBucketConfigure{
		calls: []func(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error){call1},
	}

	cfg := &config.Config{ArtifactsBucket: "km-artifacts-123456789012"}
	var logBuf bytes.Buffer

	err := ensureArtifactsBucketExists(context.Background(), cfg, &logBuf, mock)
	if err == nil {
		t.Fatal("expected non-nil error when artifacts bucket missing, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' in error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "km bootstrap --all") {
		t.Errorf("expected 'km bootstrap --all' in error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "km bootstrap --shared-ses") {
		t.Errorf("expected 'km bootstrap --shared-ses' in error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "km-artifacts-123456789012") {
		t.Errorf("expected bucket name in error; got: %v", err)
	}
}
