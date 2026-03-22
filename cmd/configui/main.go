// Command configui is the KlankerMaker web dashboard for managing sandbox profiles
// and monitoring live sandboxes. It serves an HTML dashboard with HTMX polling
// at :8080 (default) or the address specified by CONFIGUI_ADDR.
//
// Usage:
//
//	configui [--bucket <s3-bucket>] [--profiles-dir <path>]
//
// Environment variables:
//
//	CONFIGUI_ADDR   — listen address (default ":8080")
//	KM_BUCKET       — S3 state bucket name (default "tf-km")
package main

import (
	"context"
	"embed"
	"flag"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// templateFuncs defines custom functions available in HTML templates.
var templateFuncs = template.FuncMap{
	// truncateID shortens a long sandbox ID to the last 12 chars with "…" prefix for display.
	"truncateID": func(id string) string {
		if len(id) <= 16 {
			return id
		}
		// Keep prefix up to first '-', then last 8 chars
		parts := strings.SplitN(id, "-", 2)
		if len(parts) == 2 && len(parts[0]) <= 4 {
			return parts[0] + "-…" + id[len(id)-8:]
		}
		return "…" + id[len(id)-12:]
	},
}

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func main() {
	// Configure zerolog with console output for development legibility.
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// CLI flags and environment variable defaults.
	bucketDefault := envOrDefault("KM_BUCKET", "tf-km")
	addrDefault := envOrDefault("CONFIGUI_ADDR", ":8080")
	profilesDirDefault := envOrDefault("KM_PROFILES_DIR", "profiles")

	bucket := flag.String("bucket", bucketDefault, "S3 state bucket name")
	addr := flag.String("addr", addrDefault, "HTTP listen address")
	profilesDir := flag.String("profiles-dir", profilesDirDefault, "Path to profiles directory on disk")
	flag.Parse()

	// Parse embedded templates — glob all .html files under templates/.
	tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(templatesFS,
		"templates/*.html",
		"templates/partials/*.html",
	)
	if err != nil {
		log.Fatal().Err(err).Msg("configui: failed to parse templates")
	}

	// Load AWS config from environment / shared credentials / instance metadata.
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("configui: failed to load AWS config")
	}

	// Build AWS clients.
	s3Client := s3.NewFromConfig(awsCfg)
	tagClient := resourcegroupstaggingapi.NewFromConfig(awsCfg)
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	ssmClient := ssm.NewFromConfig(awsCfg)

	// Wrap real AWS clients in adapters satisfying the narrow Handler interfaces.
	listerAdapter := &s3ListerAdapter{client: s3Client}
	finderAdapter := &tagFinderAdapter{client: tagClient}

	handler := &Handler{
		tmpl:        tmpl,
		lister:      listerAdapter,
		finder:      finderAdapter,
		cwClient:    cwClient,
		ssmClient:   ssmClient,
		profilesDir: *profilesDir,
		bucket:      *bucket,
	}

	// Static file server from embedded FS (strip "static" prefix).
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal().Err(err).Msg("configui: failed to create static sub-FS")
	}

	// Register routes using Go 1.22+ method+path patterns.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handler.handleDashboard)
	mux.HandleFunc("GET /api/sandboxes", handler.handleSandboxesPartial)
	mux.HandleFunc("GET /api/sandboxes/{id}", handler.handleSandboxDetail)
	mux.HandleFunc("GET /api/sandboxes/{id}/logs", handler.handleSandboxLogs)
	mux.HandleFunc("GET /api/schema", handler.handleSchema)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Editor routes (Plan 02).
	mux.HandleFunc("GET /editor", handler.handleEditorPage)
	mux.HandleFunc("POST /api/validate", handler.handleValidate)
	mux.HandleFunc("GET /api/profiles", handler.handleProfileList)
	mux.HandleFunc("GET /api/profiles/{name}", handler.handleProfileGet)
	mux.HandleFunc("PUT /api/profiles/{name}", handler.handleProfileSave)
	// Destroy action stub — full implementation TBD.
	mux.HandleFunc("POST /api/sandboxes/{id}/destroy", stubHandler("destroy action coming soon"))

	// Secrets routes (Plan 03).
	mux.HandleFunc("GET /secrets", handler.handleSecretsPage)
	mux.HandleFunc("GET /api/secrets", handler.handleSecretsList)
	mux.HandleFunc("GET /api/secrets/{name...}", handler.handleSecretDecrypt)
	mux.HandleFunc("PUT /api/secrets/{name...}", handler.handleSecretPut)
	mux.HandleFunc("DELETE /api/secrets/{name...}", handler.handleSecretDelete)

	// Wrap with request logging middleware.
	srv := &http.Server{
		Addr:         *addr,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info().Str("addr", *addr).Str("bucket", *bucket).Msg("configui: starting server")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal().Err(err).Msg("configui: server error")
	}
}

// loggingMiddleware logs each request using zerolog.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Dur("duration", time.Since(start)).
			Msg("configui: request")
	})
}

// stubHandler returns a 501 Not Implemented handler with a descriptive message.
func stubHandler(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, msg, http.StatusNotImplemented)
	}
}

// envOrDefault returns the value of the environment variable or the default.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// --- AWS adapter types ---

// s3ListerAdapter wraps *s3.Client to satisfy SandboxLister.
type s3ListerAdapter struct {
	client *s3.Client
}

func (a *s3ListerAdapter) ListSandboxes(ctx context.Context, bucket string) ([]kmaws.SandboxRecord, error) {
	return kmaws.ListAllSandboxesByS3(ctx, a.client, bucket)
}

// tagFinderAdapter wraps *resourcegroupstaggingapi.Client to satisfy SandboxFinder.
type tagFinderAdapter struct {
	client *resourcegroupstaggingapi.Client
}

func (a *tagFinderAdapter) FindSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxLocation, error) {
	return kmaws.FindSandboxByID(ctx, a.client, sandboxID)
}
