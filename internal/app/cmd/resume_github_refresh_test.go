package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

type fakeScheduleGetter struct {
	out *scheduler.GetScheduleOutput
	err error
}

func (f *fakeScheduleGetter) GetSchedule(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return f.out, f.err
}

type fakeLambdaInvoker struct {
	out *lambda.InvokeOutput
	err error
}

func (f *fakeLambdaInvoker) Invoke(_ context.Context, _ *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	return f.out, f.err
}

func okSchedule() *scheduler.GetScheduleOutput {
	return &scheduler.GetScheduleOutput{
		Target: &schedulertypes.Target{Input: awssdk.String(`{"sandbox_id":"sb-1"}`)},
	}
}

// TestRefreshGitHubTokenOnResume_ReportsOutcome pins the operator-visible
// contract of the resume-time re-credential step: it never fails the resume,
// stays silent for a sandbox that has no GitHub access at all, and prints a
// warning naming the log group when the refresher itself is broken.
func TestRefreshGitHubTokenOnResume_ReportsOutcome(t *testing.T) {
	tests := []struct {
		name       string
		sched      *fakeScheduleGetter
		invoker    *fakeLambdaInvoker
		wantOutput []string
		wantSilent bool
	}{
		{
			name:       "success is confirmed",
			sched:      &fakeScheduleGetter{out: okSchedule()},
			invoker:    &fakeLambdaInvoker{out: &lambda.InvokeOutput{StatusCode: 200}},
			wantOutput: []string{"GitHub token refreshed"},
		},
		{
			// A profile without sourceAccess.github has no refresher. Warning on
			// every resume of every non-GitHub sandbox would train operators to
			// ignore the line that actually matters.
			name:       "no refresher is silent",
			sched:      &fakeScheduleGetter{err: &schedulertypes.ResourceNotFoundException{}},
			invoker:    &fakeLambdaInvoker{},
			wantSilent: true,
		},
		{
			name:  "broken refresher is surfaced with its own message",
			sched: &fakeScheduleGetter{out: okSchedule()},
			invoker: &fakeLambdaInvoker{out: &lambda.InvokeOutput{
				StatusCode:    200,
				FunctionError: awssdk.String("Unhandled"),
				Payload:       []byte(`{"errorMessage":"read SSM: AccessDenied"}`),
			}},
			wantOutput: []string{"[warn]", "AccessDenied", "km-github-token-refresher-sb-1"},
		},
		{
			name:       "transport failure is surfaced",
			sched:      &fakeScheduleGetter{out: okSchedule()},
			invoker:    &fakeLambdaInvoker{err: errors.New("connection reset")},
			wantOutput: []string{"[warn]", "connection reset"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(func() {
				// Must not panic and must not exit: resume has already started
				// the instance by the time this runs.
				refreshGitHubTokenOnResume(context.Background(), tt.sched, tt.invoker, "km", "sb-1")
			})

			if tt.wantSilent {
				if strings.TrimSpace(out) != "" {
					t.Fatalf("expected no output, got %q", out)
				}
				return
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(out, want) {
					t.Errorf("output %q does not contain %q", out, want)
				}
			}
		})
	}
}
