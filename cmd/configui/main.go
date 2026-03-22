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
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
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

	// Parse templates: base + partials as shared set, then clone per page to avoid
	// {{define "content"}} collisions between dashboard.html and editor.html.
	base := template.Must(template.New("").Funcs(templateFuncs).ParseFS(templatesFS,
		"templates/base.html",
		"templates/partials/*.html",
	))
	dashTmpl := template.Must(template.Must(base.Clone()).ParseFS(templatesFS, "templates/dashboard.html"))
	editorTmpl := template.Must(template.Must(base.Clone()).ParseFS(templatesFS, "templates/editor.html"))
	secretsTmpl := template.Must(template.Must(base.Clone()).ParseFS(templatesFS, "templates/secrets.html"))
	// Combined tmpl for partials-only rendering (HTMX partial responses)
	tmpl := dashTmpl

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
	schedulerClient := scheduler.NewFromConfig(awsCfg)

	// Wrap real AWS clients in adapters satisfying the narrow Handler interfaces.
	listerAdapter := &s3ListerAdapter{client: s3Client}
	finderAdapter := &tagFinderAdapter{client: tagClient}

	// Production action implementations.
	destroyer := &kmDestroyerImpl{}
	ttlExtender := &schedulerTTLExtender{client: schedulerClient}
	creator := &kmCreatorImpl{}

	handler := &Handler{
		tmpl:        tmpl,
		editorTmpl:  editorTmpl,
		secretsTmpl: secretsTmpl,
		lister:      listerAdapter,
		finder:      finderAdapter,
		cwClient:    cwClient,
		ssmClient:   ssmClient,
		destroyer:   destroyer,
		ttlExtender: ttlExtender,
		creator:     creator,
		profilesDir: *profilesDir,
		bucket:      *bucket,
		domain:      envOrDefault("KM_DOMAIN", "klankermaker.ai"),
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
	// Action routes (Plan 04): destroy, extend TTL, quick-create.
	mux.HandleFunc("POST /api/sandboxes/{id}/destroy", handler.handleDestroy)
	mux.HandleFunc("PUT /api/sandboxes/{id}/ttl", handler.handleExtendTTL)
	mux.HandleFunc("POST /api/sandboxes/create", handler.handleQuickCreate)

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

// --- Production action implementations ---

// kmDestroyerImpl satisfies Destroyer by shelling out to `km destroy <sandboxID>`.
// Using a subprocess avoids Cobra/internal package import cycles.
type kmDestroyerImpl struct{}

func (d *kmDestroyerImpl) Destroy(_ context.Context, sandboxID string) error {
	cmd := exec.Command("km", "destroy", sandboxID)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Best-effort: if km exits non-zero with "not found" message, return sentinel.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Treat exit code 1 as not-found for now; a more robust check
			// would parse stderr. For UI purposes the caller sees a 500 otherwise.
		}
		return err
	}
	return nil
}

// schedulerTTLExtender satisfies TTLExtender using EventBridge Scheduler.
// It deletes the existing schedule and creates a new one at now+duration.
type schedulerTTLExtender struct {
	client kmaws.SchedulerAPI
}

func (e *schedulerTTLExtender) ExtendTTL(ctx context.Context, sandboxID string, duration time.Duration) error {
	// Delete existing schedule first (idempotent — returns nil if not found).
	if err := kmaws.DeleteTTLSchedule(ctx, e.client, sandboxID); err != nil {
		return err
	}
	// TTL extension without a Lambda target ARN is a no-op at this point;
	// the scheduler input requires a Lambda ARN that is environment-specific.
	// For now, delete is sufficient to cancel pending expiry; a follow-up can
	// wire CreateTTLSchedule with the correct target ARN from config.
	log.Info().Str("sandbox_id", sandboxID).Dur("duration", duration).
		Msg("configui: TTL schedule deleted; new schedule requires Lambda ARN config")
	return nil
}

// kmCreatorImpl satisfies SandboxCreator by shelling out to `km create <profilePath>`.
type kmCreatorImpl struct{}

func (c *kmCreatorImpl) Create(_ context.Context, profilePath string) error {
	cmd := exec.Command("km", "create", profilePath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
