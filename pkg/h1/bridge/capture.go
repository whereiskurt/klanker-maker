package bridge

// capture.go — S3RawCapturer, the h1.debug_capture implementation of RawCapturer.
//
// One object per delivery at s3://<artifacts>/h1-captures/<X-H1-Delivery>.json.
// The GUID is the key so a HackerOne redelivery overwrites rather than
// accumulates; a delivery with no GUID header falls back to a nanosecond
// timestamp. The record embeds the body as raw JSON when it parses and as a
// string otherwise, so a non-JSON delivery is still captured verbatim.
//
// Captures contain the full report body (vulnerability details): the prefix is as
// sensitive as captures/. Off by default; the operator turns it off once the real
// payload shape is pinned.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3PutObjectAPI is the narrow S3 surface S3RawCapturer needs. Satisfied by *s3.Client.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3RawCapturer writes raw deliveries under h1-captures/ in Bucket.
type S3RawCapturer struct {
	Client S3PutObjectAPI
	Bucket string
	// Now is a test seam; nil ⇒ time.Now.
	Now func() time.Time
}

// captureRecord is the persisted shape.
type captureRecord struct {
	ReceivedAt string            `json:"received_at"`
	Headers    map[string]string `json:"headers"`
	Body       any               `json:"body"`
}

// Capture implements RawCapturer.
func (c *S3RawCapturer) Capture(ctx context.Context, deliveryGUID string, headers map[string]string, rawBody []byte) error {
	if c.Bucket == "" {
		return errors.New("h1-bridge: S3RawCapturer: empty bucket")
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().UTC()

	name := deliveryGUID
	if name == "" {
		name = strconv.FormatInt(t.UnixNano(), 10)
	}
	key := "h1-captures/" + name + ".json"

	rec := captureRecord{ReceivedAt: t.Format(time.RFC3339), Headers: headers}
	if json.Valid(rawBody) {
		rec.Body = json.RawMessage(rawBody)
	} else {
		rec.Body = string(rawBody)
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("h1-bridge: marshal capture record: %w", err)
	}

	_, err = c.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(c.Bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(payload), // re-readable for SDK retries
		ContentType: awssdk.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: put capture %s: %w", key, err)
	}
	return nil
}
