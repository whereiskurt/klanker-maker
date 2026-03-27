// Package version provides build-time version information injected via ldflags.
//
// Usage in Makefile:
//
//	go build -ldflags "-X github.com/whereiskurt/klankrmkr/pkg/version.Number=v0.0.42
//	  -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=abc1234"
package version

import "fmt"

// Number is the semantic version (e.g. "v0.0.42"), set at build time via ldflags.
var Number = "v0.0.0-dev"

// GitCommit is the short git commit hash, set at build time via ldflags.
var GitCommit = "unknown"

// String returns the full version string: "v0.0.42 (abc1234)".
func String() string {
	return fmt.Sprintf("%s (%s)", Number, GitCommit)
}

// Header returns a version line suitable for email footers and log headers.
func Header() string {
	return fmt.Sprintf("km %s", String())
}
