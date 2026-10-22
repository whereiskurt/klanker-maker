package cmd

import (
	"os"
	"regexp"
	"testing"
)

// Every path that applies lambda-slack-bridge must prepare the channel routes
// right beside the default route, or the bridge would apply with a stale (or
// missing) table and hash. Source-level guard: each prepareSlackDefaultRoute
// call site is followed within 20 lines by a channel-routes preparation call.
func TestEveryBridgeApplyPathPreparesChannelRoutes(t *testing.T) {
	site := regexp.MustCompile(`^\s*if err := prepareSlackDefaultRoute\(`)
	prep := regexp.MustCompile(`prepareSlackChannelRoutes(WithAWS)?\(|exportSlackRoutesHashFromSSM\(`)
	for _, file := range []string{"init.go", "slack.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := regexp.MustCompile(`\n`).Split(string(src), -1)
		for i, l := range lines {
			if !site.MatchString(l) {
				continue
			}
			found := false
			for j := i; j < i+20 && j < len(lines); j++ {
				if prep.MatchString(lines[j]) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s:%d: prepareSlackDefaultRoute without a channel-routes preparation nearby", file, i+1)
			}
		}
	}
}
