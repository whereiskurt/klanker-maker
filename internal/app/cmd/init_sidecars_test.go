package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// A sidecar the userdata downloads but km init never uploads 404s the gated
// download and aborts bootstrap. This pairs the two mechanically.
func TestSidecarBuilds_CoversEverySecretsBinary(t *testing.T) {
	have := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		have[n] = true
	}
	for _, want := range []string{"km-secretsd", "km-env"} {
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
	for _, n := range []string{"km-secretsd", "km-env"} {
		if !strings.Contains(src, "sidecars/"+n) {
			t.Errorf("userdata never downloads %s", n)
		}
		if !built[n] {
			t.Errorf("userdata downloads %s but km init never uploads it", n)
		}
	}
}
