// Package cmd — slack_manifest.go
// Implements the "km slack manifest" command that renders a deployment-specific
// Slack App manifest to stdout. Operators pipe the output to a file and paste
// it into the Slack admin "From manifest" UI to install the app.
//
//	km slack manifest > app.json
//	# Then: Slack Admin → Apps → Build → New App → From manifest → paste app.json
package cmd

import (
	_ "embed"

	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

//go:embed slack_manifest_template.json
var slackManifestTemplate string

// SlackManifestData carries the parameters injected into the manifest template.
// Keep field names stable — the template references them as
// {{.AppName}} and {{.EventsURL}}.
// Exported so tests can inject known values directly.
type SlackManifestData struct {
	AppName   string
	EventsURL string
}

// RenderSlackManifest renders the embedded Slack App manifest template to w with
// the given data. Exported for golden-file testing and external tooling.
//
// The output is a complete, valid Slack App manifest in JSON format.
// Operators pipe the output to a file and paste into Slack admin "From manifest" UI:
//
//	km slack manifest > app.json
//	# Then: Slack Admin → Apps → Build → New App → From manifest → paste app.json
func RenderSlackManifest(w io.Writer, data SlackManifestData) error {
	t, err := template.New("slack-manifest").Parse(slackManifestTemplate)
	if err != nil {
		return fmt.Errorf("parse manifest template: %w", err)
	}
	return t.Execute(w, data)
}

// SlackManifestOpts collects RunSlackManifest options parsed from cobra flags.
type SlackManifestOpts struct {
	// AppName is the --app-name override. When empty, defaults to
	// "KlankerMaker-{resource_prefix}".
	AppName string
}

// RunSlackManifest is the exported handler (testable via dependency injection).
// It reads the bridge URL from SSM, derives the events URL, then renders the
// manifest template to w.
func RunSlackManifest(ctx context.Context, d *SlackCmdDeps, cfg *config.Config, opts SlackManifestOpts, w io.Writer) error {
	appName := opts.AppName
	if appName == "" {
		appName = fmt.Sprintf("KlankerMaker-%s", cfg.GetResourcePrefix())
	}
	if len(appName) > 35 {
		return fmt.Errorf("app name %q exceeds Slack's 35-char limit (use --app-name to override)", appName)
	}

	bridgeURL, err := d.SSM.Get(ctx, d.SsmPrefix+"slack/bridge-url", false)
	if err != nil {
		return fmt.Errorf("read SSM %sslack/bridge-url: %w (run `km slack init` first)", d.SsmPrefix, err)
	}
	if bridgeURL == "" {
		return fmt.Errorf("SSM %sslack/bridge-url is empty (run `km slack init` first)", d.SsmPrefix)
	}
	eventsURL := strings.TrimRight(bridgeURL, "/") + "/events"

	// Banner to stderr — stdout stays pure JSON and pipeable.
	fmt.Fprintf(os.Stderr, "# Slack App manifest for resource_prefix=%s\n", cfg.GetResourcePrefix())
	fmt.Fprintf(os.Stderr, "# Bridge URL: %s\n", bridgeURL)
	fmt.Fprintf(os.Stderr, "# To install: paste output into Slack admin → Apps → Build → New App → From manifest\n")

	return RenderSlackManifest(w, SlackManifestData{
		AppName:   appName,
		EventsURL: eventsURL,
	})
}

func newSlackManifestCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var appName string
	c := &cobra.Command{
		Use:          "manifest",
		Short:        "Render a deployment-specific Slack App manifest (JSON) to stdout",
		Long:         "Generates a Slack App manifest with the bridge URL, app name, and full scope set\n(including users:read.email and files:read) wired in.\n\nPipe to a file and paste into Slack admin 'From manifest' UI to install:\n\n  km slack manifest > app.json\n  # Slack Admin → Apps → Build → New App → From manifest → paste app.json",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}
			return RunSlackManifest(ctx, deps, cfg, SlackManifestOpts{AppName: appName}, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&appName, "app-name", "", "Override the auto-derived app name (default: KlankerMaker-{resource_prefix}; max 35 chars)")
	return c
}
