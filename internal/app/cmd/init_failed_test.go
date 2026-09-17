package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type initFailedFetcher struct{ rec *kmaws.SandboxRecord }

func (f initFailedFetcher) FetchSandbox(context.Context, string) (*kmaws.SandboxRecord, error) {
	return f.rec, nil
}

// The exact message the bootstrap's printf emits (pkg/compiler/userdata.go,
// 7.5 block); pkg/compiler's TestUserdataInitBlock_FailureIsReportedNotSwallowed
// proves the block produces this shape, and this file proves km reads it.
const initFailedEvent = `{"timestamp":"2026-09-17T04:15:02Z","sandbox_id":"always-f51f581b","event_type":"init_failed","source":"bootstrap","detail":{"exit_code":"1","step":"3","total":"13","skipped":"10","command":"npm install -g @anthropic-ai/claude-code@2.1.171"}}`

func TestLookupInitFailure_ParsesTheBootstrapEvent(t *testing.T) {
	ts := time.Date(2026, 9, 17, 4, 15, 2, 0, time.UTC).UnixMilli()
	cw := &fakeCWLogsFilter{events: []cwltypes.FilteredLogEvent{{Message: awssdk.String(initFailedEvent), Timestamp: &ts}}}
	f, err := lookupInitFailure(context.Background(), cw, "/km/sandboxes/always-f51f581b/")
	if err != nil || f == nil {
		t.Fatalf("lookup: f=%v err=%v", f, err)
	}
	if cw.gotLogGroup != "/km/sandboxes/always-f51f581b/" {
		t.Errorf("log group = %q", cw.gotLogGroup)
	}
	if cw.gotFilterPattern != `{ $.event_type = "init_failed" }` {
		t.Errorf("filter pattern must be CW JSON syntax, got %q", cw.gotFilterPattern)
	}
	want := "FAILED (exit 1) at step 3/13: npm install -g @anthropic-ai/claude-code@2.1.171 — 10 later initCommand(s) not run"
	if got := f.String(); got != want {
		t.Errorf("String() =\n%s\nwant\n%s", got, want)
	}
	if !f.At.Equal(time.UnixMilli(ts)) {
		t.Errorf("At = %v", f.At)
	}
}

func TestLookupInitFailure_NoEventMeansNil(t *testing.T) {
	f, err := lookupInitFailure(context.Background(), &fakeCWLogsFilter{}, "/km/sandboxes/x/")
	if err != nil || f != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", f, err)
	}
}

func TestDoctor_InitFailed_WarnsAndNamesTheSandbox(t *testing.T) {
	cw := &fakeCWLogsFilter{events: []cwltypes.FilteredLogEvent{{Message: awssdk.String(initFailedEvent)}}}
	lister := &fakeRunningSandboxLister{ids: []string{"always-f51f581b"}}
	got := checkInitFailed(context.Background(), cw, lister, "/km/sandboxes/")
	if got.Status != CheckWarn {
		t.Fatalf("status = %s, want WARN (the box is up; part of its profile is not)", got.Status)
	}
	if !strings.Contains(got.Message, "always-f51f581b: FAILED (exit 1) at step 3/13") {
		t.Errorf("message must name the sandbox and the step: %s", got.Message)
	}
	if !strings.Contains(got.Remediation, "km destroy && km create") {
		t.Errorf("remediation must say how to recover: %s", got.Remediation)
	}
}

func TestDoctor_InitFailed_OKWhenNoEventsAndSkippedWhenNoDeps(t *testing.T) {
	lister := &fakeRunningSandboxLister{ids: []string{"a", "b"}}
	if got := checkInitFailed(context.Background(), &fakeCWLogsFilter{}, lister, "/km/sandboxes/"); got.Status != CheckOK {
		t.Errorf("no events: status = %s, want OK", got.Status)
	}
	if got := checkInitFailed(context.Background(), nil, lister, "/km/sandboxes/"); got.Status != CheckSkipped {
		t.Errorf("nil client: status = %s, want SKIPPED", got.Status)
	}
	// A stream that cannot be read is not a failure to report — a pre-fix
	// sandbox never emitted the event and must not be blamed for it.
	cw := &fakeCWLogsFilter{err: context.DeadlineExceeded}
	if got := checkInitFailed(context.Background(), cw, lister, "/km/sandboxes/"); got.Status != CheckOK {
		t.Errorf("unreadable stream: status = %s, want OK", got.Status)
	}
}

// km status prints the Init line only when the lookup returns a failure, and
// never for failed/nocap rows (those have their own Failure line).
func TestStatus_PrintsInitFailureLine(t *testing.T) {
	orig := statusInitFailureLookup
	t.Cleanup(func() { statusInitFailureLookup = orig })
	statusInitFailureLookup = func(ctx context.Context, sandboxID, resourcePrefix string) *initFailure {
		return &initFailure{ExitCode: "1", Step: "3", Total: "13", Skipped: "10", Command: "npm install -g x@1"}
	}
	rec := &kmaws.SandboxRecord{SandboxID: "always-f51f581b", Profile: "always.ir", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: time.Now()}
	run := func() string {
		root := &cobra.Command{Use: "km"}
		root.AddCommand(NewStatusCmdWithFetcher(&config.Config{}, initFailedFetcher{rec}))
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"status", "always-f51f581b"})
		if err := root.Execute(); err != nil {
			t.Fatalf("status: %v\n%s", err, buf.String())
		}
		return buf.String()
	}

	out := run()
	if !strings.Contains(out, "Init:        FAILED (exit 1) at step 3/13: npm install -g x@1 — 10 later initCommand(s) not run") {
		t.Errorf("status output missing the Init line:\n%s", out)
	}

	rec.Status = "failed"
	out = run()
	if strings.Contains(out, "Init:") {
		t.Errorf("failed rows have their own Failure line; Init must not be printed:\n%s", out)
	}
}
