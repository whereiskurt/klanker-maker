package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3RunAPI is a narrow interface for S3 operations required by MLflow run logging.
// It accepts PutObject for writing run metadata and GetObject for reading existing metadata
// during finalize (read-modify-write). Use the real *s3.Client or mockS3RunAPI in tests.
type S3RunAPI interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// MLflowRun holds all params and metrics for a single sandbox session recorded as an
// MLflow run. It is serialized as a single JSON file (meta.json) stored in S3.
// Compatible with manual inspection and importable into an MLflow tracking server later.
type MLflowRun struct {
	// Params — set at create time
	SandboxID   string    `json:"sandbox_id"`
	ProfileName string    `json:"profile_name"`
	Substrate   string    `json:"substrate"`  // ec2 | ecs
	Region      string    `json:"region"`
	TTL         string    `json:"ttl"`        // duration string e.g. "2h"
	StartTime   time.Time `json:"start_time"`
	Experiment  string    `json:"experiment"` // default "klankrmkr"

	// Metrics — set at finalize time
	EndTime          *time.Time `json:"end_time,omitempty"`
	DurationSeconds  float64    `json:"duration_seconds,omitempty"`
	ExitStatus       *int       `json:"exit_status,omitempty"` // pointer so exit_status=0 (success) is preserved
	CommandsExecuted int64      `json:"commands_executed,omitempty"`
	BytesEgressed    int64      `json:"bytes_egressed,omitempty"`
}

// MLflowMetrics holds the finalization metrics for a completed sandbox session.
type MLflowMetrics struct {
	DurationSeconds  float64
	ExitStatus       int
	CommandsExecuted int64
	BytesEgressed    int64
}

// mlflowKey returns the S3 object key for a given experiment and sandbox ID.
// Format: mlflow/<experiment>/<sandbox-id>/meta.json
func mlflowKey(experiment, sandboxID string) string {
	return fmt.Sprintf("mlflow/%s/%s/meta.json", experiment, sandboxID)
}

// WriteMLflowRun writes an MLflowRun as JSON to S3 at:
//
//	s3://<bucket>/mlflow/<experiment>/<sandbox-id>/meta.json
//
// This is called at sandbox create time to record params. Metrics fields are
// omitted (zero values) and added later by FinalizeMLflowRun.
func WriteMLflowRun(ctx context.Context, client S3RunAPI, bucket string, run MLflowRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal MLflow run: %w", err)
	}

	key := mlflowKey(run.Experiment, run.SandboxID)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        strings.NewReader(string(data)),
		ContentType: awssdk.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("write MLflow run to s3://%s/%s: %w", bucket, key, err)
	}

	return nil
}

// FinalizeMLflowRun reads the existing meta.json for a sandbox run from S3,
// updates the metric fields (duration, exit_status, commands_executed, bytes_egressed)
// and end_time, then writes the updated record back.
//
// This is called at sandbox destroy time to close out the run record.
func FinalizeMLflowRun(ctx context.Context, client S3RunAPI, bucket, sandboxID, experiment string, metrics MLflowMetrics) error {
	key := mlflowKey(experiment, sandboxID)

	// Read existing run record
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return fmt.Errorf("read MLflow run from s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read MLflow run body: %w", err)
	}

	var run MLflowRun
	if err := json.Unmarshal(data, &run); err != nil {
		return fmt.Errorf("unmarshal MLflow run: %w", err)
	}

	// Apply finalization metrics
	now := time.Now().UTC()
	exitStatus := metrics.ExitStatus
	run.EndTime = &now
	run.DurationSeconds = metrics.DurationSeconds
	run.ExitStatus = &exitStatus
	run.CommandsExecuted = metrics.CommandsExecuted
	run.BytesEgressed = metrics.BytesEgressed

	updated, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal finalized MLflow run: %w", err)
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        strings.NewReader(string(updated)),
		ContentType: awssdk.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("write finalized MLflow run to s3://%s/%s: %w", bucket, key, err)
	}

	return nil
}
