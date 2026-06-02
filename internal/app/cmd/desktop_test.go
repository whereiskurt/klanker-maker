// Package cmd — desktop_test.go
// Tests for km desktop start / km desktop status.
// Mirrors the structure of vscode_test.go (same package, same mock helpers).
// Wave 3 Plans 93-04/93-05 activate these tests (t.Skip removed) and wire the mocks.
package cmd

import (
	"testing"
)

// ---- Tests ----

// TestDesktopStart is the Wave 0 stub for DSK-09-CLI-START.
// Wave 3 (93-05): port-in-use err, missing credential err, healthy prints
// URL+credential, port-forward args contain AWS-StartPortForwardingSession + 8444.
func TestDesktopStart(t *testing.T) {
	t.Skip("Wave 3 (93-05): port-in-use err, missing credential err, healthy prints URL+credential, port-forward args contain AWS-StartPortForwardingSession + 8444")
}

// TestDesktopStatus is the Wave 0 stub for DSK-10-CLI-STATUS.
// Wave 3 (93-05): healthy prints KasmVNC ready; inactive → error with desktop.enabled hint.
func TestDesktopStatus(t *testing.T) {
	t.Skip("Wave 3 (93-05): healthy prints KasmVNC ready; inactive → error with desktop.enabled hint")
}

// TestDesktopCredential is the Wave 0 stub for DSK-08-CREDENTIAL.
// Wave 3 (93-04): km create writes ~/.km/desktop/<id> user:pass mode 0600; threaded into NetworkConfig.
func TestDesktopCredential(t *testing.T) {
	t.Skip("Wave 3 (93-04): km create writes ~/.km/desktop/<id> user:pass mode 0600; threaded into NetworkConfig")
}
