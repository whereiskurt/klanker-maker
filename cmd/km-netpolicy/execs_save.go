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

// execsKey is the object key one generation of one save lands at.
//
// Scoped under the sandbox's own id, matching the transcripts/ and captures/
// grants, so a compromised sandbox cannot write over another's trace. The
// timestamp means repeat saves accumulate rather than overwrite — the trace
// grows over a box's life and an earlier save may hold records that rotation
// has since discarded.
//
// gen distinguishes the live generation ("") from the retained rotated one
// (".1"): reader.go reads both files, and a save that silently uploaded only
// the live one would drop the retained generation on the floor the moment a
// box has rotated once — the exact defect this function exists to close. The
// two are never concatenated into one object: the retained generation is a
// separate, older time window, and merging them would misrepresent both
// (a mixed file with no boundary marker, sorted by nothing).
func execsKey(sandboxID string, now time.Time, gen string) string {
	return fmt.Sprintf("execs/%s/execs-%s%s.jsonl", sandboxID, now.UTC().Format("20060102T150405Z"), gen)
}

// runExecsSave uploads the exec store to S3 using the instance role — both
// the live generation and, if the store has rotated at least once, the
// retained ".1" generation as its own separate object.
//
// Best-effort by construction: it is wired to the unit's ExecStop (Task 8), so
// a failure here must never turn a clean shutdown into a failed one — that
// posture lives in the systemd wiring, not here. The file always stays on
// disk regardless of upload outcome.
func runExecsSave(o opts) error {
	bucket := o.artifactsBucket
	if bucket == "" {
		err := errors.New("KM_ARTIFACTS_BUCKET is not set; cannot save the exec trace")
		fmt.Fprintf(o.stderr, "%s execs save: %v\n", prog, err)
		return err
	}
	sandboxID := o.sandboxID
	if sandboxID == "" {
		err := errors.New("KM_SANDBOX_ID is not set; cannot scope the exec trace")
		fmt.Fprintf(o.stderr, "%s execs save: %v\n", prog, err)
		return err
	}

	livePath := execlog.Path(o.execDir)
	liveFile, err := os.Open(livePath)
	switch {
	case err == nil:
		defer liveFile.Close()
	case os.IsNotExist(err):
		fmt.Fprintln(o.stdout, "nothing to save: no exec trace on disk yet")
		liveFile = nil
	default:
		fmt.Fprintf(o.stderr, "%s execs save: cannot read exec store %s: %v\n", prog, livePath, err)
		return err
	}

	var liveSize int64
	if liveFile != nil {
		fi, err := liveFile.Stat()
		if err != nil {
			fmt.Fprintf(o.stderr, "%s execs save: cannot stat exec store %s: %v\n", prog, livePath, err)
			return err
		}
		liveSize = fi.Size()
		if liveSize == 0 {
			fmt.Fprintln(o.stdout, "nothing to save: the exec trace is empty")
		}
	}

	// The retained rotated generation. Its absence is the ordinary case — a
	// box that has never rotated, or a first save — and is not reported at
	// all; a genuine read error reading it is, but does not abort the live
	// generation's own upload below.
	rotatedPath := livePath + ".1"
	rotatedFile, rotatedSize, err := openIfPresent(rotatedPath)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s execs save: cannot read retained exec store %s: %v\n", prog, rotatedPath, err)
	}
	if rotatedFile != nil {
		defer rotatedFile.Close()
	}

	if liveSize == 0 && rotatedSize == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// SSM's `AWS-RunShellScript` document runs this verb outside any login
	// shell, so the ambient environment carries no region even when a root
	// login on the same box would see one from /etc/profile.d. WithRegion is
	// explicit here rather than left to the SDK's own env lookup so o.region
	// (opts, not os.Getenv) is what actually governs — same reasoning as the
	// bucket/sandbox id above, and empty is safely ignored by the SDK.
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(o.region))
	if err != nil {
		fmt.Fprintf(o.stderr, "%s execs save: load aws config: %v (trace kept locally at %s)\n", prog, err, livePath)
		return fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	now := time.Now()

	var firstErr error
	if liveSize > 0 {
		if err := uploadGeneration(ctx, o, client, bucket, sandboxID, now, "", livePath, liveFile, liveSize); err != nil {
			firstErr = err
		}
	}
	if rotatedSize > 0 {
		if err := uploadGeneration(ctx, o, client, bucket, sandboxID, now, ".1", rotatedPath, rotatedFile, rotatedSize); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// openIfPresent opens path for upload, treating "does not exist" as the
// normal absent-generation case (nil, 0, nil) rather than an error.
func openIfPresent(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

// uploadGeneration uploads one generation of the store and reports the
// outcome on stdout/stderr, matching the single-generation messages this
// verb has always printed.
func uploadGeneration(ctx context.Context, o opts, client *s3.Client, bucket, sandboxID string, now time.Time, gen, path string, f *os.File, size int64) error {
	key := execsKey(sandboxID, now, gen)
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        f,
		ContentType: aws.String("application/x-ndjson"),
	}); err != nil {
		fmt.Fprintf(o.stderr, "%s execs save: upload failed, trace kept locally at %s: %v\n", prog, path, err)
		return fmt.Errorf("upload exec trace %s: %w", path, err)
	}
	fmt.Fprintf(o.stdout, "saved: s3://%s/%s (%d bytes)\n", bucket, key, size)
	return nil
}
