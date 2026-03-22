package aws_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Mock ----

type mockS3RunAPI struct {
	putInput  *s3.PutObjectInput
	putErr    error
	getOutput *s3.GetObjectOutput
	getErr    error
}

func (m *mockS3RunAPI) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	m.putInput = input
	return &s3.PutObjectOutput{}, m.putErr
}

func (m *mockS3RunAPI) GetObject(
	_ context.Context,
	_ *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	return m.getOutput, m.getErr
}

// ---- Tests ----

func TestWriteMLflowRun_PutsCorrectKey(t *testing.T) {
	mock := &mockS3RunAPI{}

	run := kmaws.MLflowRun{
		SandboxID:   "sb-123",
		ProfileName: "test-profile",
		Substrate:   "ec2",
		Region:      "us-east-1",
		TTL:         "2h",
		StartTime:   time.Now(),
		Experiment:  "klankrmkr",
	}

	err := kmaws.WriteMLflowRun(context.Background(), mock, "my-bucket", run)
	if err != nil {
		t.Fatalf("WriteMLflowRun returned error: %v", err)
	}

	if mock.putInput == nil {
		t.Fatal("PutObject was not called")
	}

	expectedKey := "mlflow/klankrmkr/sb-123/meta.json"
	if *mock.putInput.Key != expectedKey {
		t.Errorf("S3 key = %q, want %q", *mock.putInput.Key, expectedKey)
	}

	if *mock.putInput.Bucket != "my-bucket" {
		t.Errorf("Bucket = %q, want %q", *mock.putInput.Bucket, "my-bucket")
	}
}

func TestWriteMLflowRun_JSONContainsParams(t *testing.T) {
	mock := &mockS3RunAPI{}

	startTime := time.Date(2026, 3, 22, 0, 0, 0, 0, time.UTC)
	run := kmaws.MLflowRun{
		SandboxID:   "sb-abc",
		ProfileName: "dev-profile",
		Substrate:   "ecs",
		Region:      "us-west-2",
		TTL:         "4h",
		StartTime:   startTime,
		Experiment:  "klankrmkr",
	}

	err := kmaws.WriteMLflowRun(context.Background(), mock, "bucket", run)
	if err != nil {
		t.Fatalf("WriteMLflowRun returned error: %v", err)
	}

	body, err := io.ReadAll(mock.putInput.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	requiredFields := []string{"sandbox_id", "profile_name", "substrate", "start_time"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("JSON missing field %q", field)
		}
	}

	if parsed["sandbox_id"] != "sb-abc" {
		t.Errorf("sandbox_id = %q, want %q", parsed["sandbox_id"], "sb-abc")
	}
	if parsed["profile_name"] != "dev-profile" {
		t.Errorf("profile_name = %q, want %q", parsed["profile_name"], "dev-profile")
	}
}

func TestFinalizeMLflowRun_UpdatesMetrics(t *testing.T) {
	// Seed existing run JSON to return from GetObject
	existing := kmaws.MLflowRun{
		SandboxID:   "sb-999",
		ProfileName: "test",
		Substrate:   "ec2",
		Region:      "us-east-1",
		TTL:         "1h",
		StartTime:   time.Now().Add(-5 * time.Minute),
		Experiment:  "klankrmkr",
	}
	existingJSON, _ := json.Marshal(existing)

	mock := &mockS3RunAPI{
		getOutput: &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewReader(existingJSON)),
		},
	}

	metrics := kmaws.MLflowMetrics{
		DurationSeconds:  300,
		ExitStatus:       0,
		CommandsExecuted: 42,
		BytesEgressed:    1024,
	}

	err := kmaws.FinalizeMLflowRun(context.Background(), mock, "bucket", "sb-999", "klankrmkr", metrics)
	if err != nil {
		t.Fatalf("FinalizeMLflowRun returned error: %v", err)
	}

	if mock.putInput == nil {
		t.Fatal("PutObject was not called")
	}

	body, err := io.ReadAll(mock.putInput.Body)
	if err != nil {
		t.Fatalf("failed to read put body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("put body is not valid JSON: %v", err)
	}

	if result["duration_seconds"] != float64(300) {
		t.Errorf("duration_seconds = %v, want 300", result["duration_seconds"])
	}
	if result["exit_status"] != float64(0) {
		t.Errorf("exit_status = %v, want 0", result["exit_status"])
	}
	if result["end_time"] == nil {
		t.Error("end_time should be set after finalize")
	}
}

func TestWriteMLflowRun_S3Error(t *testing.T) {
	expectedErr := errors.New("s3 write failure")
	mock := &mockS3RunAPI{putErr: expectedErr}

	run := kmaws.MLflowRun{
		SandboxID:  "sb-err",
		Experiment: "klankrmkr",
		StartTime:  time.Now(),
	}

	err := kmaws.WriteMLflowRun(context.Background(), mock, "bucket", run)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "s3 write failure") {
		t.Errorf("error %q does not contain original message", err.Error())
	}
}
