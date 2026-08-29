package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// execsKey is the object key one save lands at.
//
// Scoped under the sandbox's own id, matching the transcripts/ and captures/
// grants, so a compromised sandbox cannot write over another's trace. The
// timestamp means repeat saves accumulate rather than overwrite — the trace
// grows over a box's life and an earlier save may hold records that rotation
// has since discarded.
func execsKey(sandboxID string, now time.Time) string {
	return fmt.Sprintf("execs/%s/execs-%s.jsonl", sandboxID, now.UTC().Format("20060102T150405Z"))
}

// runExecsSave uploads the live store to S3 using the instance role.
//
// Best-effort by construction: it is wired to the unit's ExecStop (Task 8), so
// a failure here must never turn a clean shutdown into a failed one — that
// posture lives in the systemd wiring, not here. The file always stays on
// disk regardless of upload outcome.
func runExecsSave(o opts) error {
	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if bucket == "" {
		return errors.New("KM_ARTIFACTS_BUCKET is not set; cannot save the exec trace")
	}
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if sandboxID == "" {
		return errors.New("KM_SANDBOX_ID is not set; cannot scope the exec trace")
	}

	path := execlog.Path(o.execDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(o.stdout, "nothing to save: no exec trace on disk yet")
			return nil
		}
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		fmt.Fprintln(o.stdout, "nothing to save: the exec trace is empty")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	key := execsKey(sandboxID, time.Now())
	if _, err := s3.NewFromConfig(cfg).PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   f,
	}); err != nil {
		return fmt.Errorf("upload exec trace: %w", err)
	}

	fmt.Fprintf(o.stdout, "saved: s3://%s/%s (%d bytes)\n", bucket, key, fi.Size())
	return nil
}
