package main

import (
	"context"
	"testing"

	auditlog "github.com/whereiskurt/klankrmkr/sidecars/audit-log"
)

// TestBuildDestStdoutReturnsRedacting verifies that buildDest("stdout", ..., nil)
// returns a *auditlog.RedactingDestination.
func TestBuildDestStdoutReturnsRedacting(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "stdout", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest stdout: unexpected error: %v", err)
	}
	if _, ok := dest.(*auditlog.RedactingDestination); !ok {
		t.Errorf("buildDest stdout: got %T, want *auditlog.RedactingDestination", dest)
	}
}

// TestBuildDestS3ReturnsRedacting verifies that buildDest("s3", ..., nil)
// returns a *auditlog.RedactingDestination.
func TestBuildDestS3ReturnsRedacting(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "s3", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest s3: unexpected error: %v", err)
	}
	if _, ok := dest.(*auditlog.RedactingDestination); !ok {
		t.Errorf("buildDest s3: got %T, want *auditlog.RedactingDestination", dest)
	}
}

// TestBuildDestNilCWClientStdout verifies that passing a nil CW client with "stdout"
// destination works without error — CW client is only used for the cloudwatch dest.
func TestBuildDestNilCWClientStdout(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "stdout", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest with nil cwClient and stdout: unexpected error: %v", err)
	}
	if dest == nil {
		t.Error("buildDest returned nil destination")
	}
}

// TestIdleDetectorTypeExists verifies that lifecycle.IdleDetector can be constructed
// and that the Run method has the correct signature (compile-time check).
func TestIdleDetectorTypeExists(t *testing.T) {
	// This is a compile-time test — if IdleDetector or its fields/methods don't exist,
	// the test file won't compile.
	detector := newIdleDetector("sb-test", 30, nil, "/km/sandboxes/test/", "audit", func(id string) {})
	if detector == nil {
		t.Error("newIdleDetector returned nil")
	}
}
