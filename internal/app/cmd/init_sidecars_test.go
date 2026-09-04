package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// Every Phase 133 sandbox-side binary. Named once so a fourth is added in one
// place rather than two, and so neither guard below can drift from the other.
// A name is added here in the SAME change that adds its userdata download —
// uploading a binary nothing fetches is harmless, but listing one here before
// the download exists turns this guard red for a reason it was not built to
// report.
var secretsBinaries = []string{"km-secretsd", "km-env", "km-creds"}

// A sidecar the userdata downloads but km init never uploads 404s the gated
// download and aborts bootstrap. This pairs the two mechanically.
func TestSidecarBuilds_CoversEverySecretsBinary(t *testing.T) {
	have := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		have[n] = true
	}
	for _, want := range secretsBinaries {
		if !have[want] {
			t.Errorf("sidecarBuilds() omits %s: userdata downloads it and boot would 404", want)
		}
	}
}

func TestUserdataDownloadsMatchSidecarBuilds(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "compiler", "userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	built := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		built[n] = true
	}
	for _, n := range secretsBinaries {
		if !strings.Contains(src, "sidecars/"+n) {
			t.Errorf("userdata never downloads %s", n)
		}
		if !built[n] {
			t.Errorf("userdata downloads %s but km init never uploads it", n)
		}
	}
}
