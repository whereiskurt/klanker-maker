package aws

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

type stubScheduleGetter struct {
	out       *scheduler.GetScheduleOutput
	err       error
	gotName   string
	gotGroup  string
	callCount int
}

func (s *stubScheduleGetter) GetSchedule(_ context.Context, in *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	s.callCount++
	s.gotName = awssdk.ToString(in.Name)
	s.gotGroup = awssdk.ToString(in.GroupName)
	return s.out, s.err
}

type stubLambdaInvoker struct {
	out        *lambda.InvokeOutput
	err        error
	gotFn      string
	gotType    string
	gotPayload string
	callCount  int
}

func (s *stubLambdaInvoker) Invoke(_ context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	s.callCount++
	s.gotFn = awssdk.ToString(in.FunctionName)
	s.gotType = string(in.InvocationType)
	s.gotPayload = string(in.Payload)
	return s.out, s.err
}

// scheduleWithInput builds a GetScheduleOutput carrying the given target input
// JSON, mirroring what EventBridge Scheduler stores for the per-sandbox
// github-token schedule.
func scheduleWithInput(input string) *scheduler.GetScheduleOutput {
	return &scheduler.GetScheduleOutput{
		Target: &schedulertypes.Target{Input: awssdk.String(input)},
	}
}

const samplePayload = `{"sandbox_id":"sb-abc","resource_prefix":"km","installation_id":"12345","ssm_parameter_name":"/km/sandbox/sb-abc/github-token","kms_key_arn":"arn:aws:kms:us-east-1:1:key/k","allowed_repos":["o/r"],"permissions":{"contents":"write"}}`

func TestForceGitHubTokenRefresh_HappyPath(t *testing.T) {
	sched := &stubScheduleGetter{out: scheduleWithInput(samplePayload)}
	inv := &stubLambdaInvoker{out: &lambda.InvokeOutput{StatusCode: 200}}

	if err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sched.gotName != "km-github-token-sb-abc" {
		t.Errorf("schedule name = %q, want km-github-token-sb-abc", sched.gotName)
	}
	if sched.gotGroup != "default" {
		t.Errorf("schedule group = %q, want default", sched.gotGroup)
	}
	if inv.gotFn != "km-github-token-refresher-sb-abc" {
		t.Errorf("function = %q, want km-github-token-refresher-sb-abc", inv.gotFn)
	}
	// The schedule's stored payload must be forwarded byte-for-byte: it carries
	// installation_id, kms_key_arn, allowed_repos and the compiled permissions
	// map decided at create time. Rebuilding it here would duplicate create-time
	// knowledge — the exact mistake that silently broke this feature once before
	// (see pkg/github/token.go TokenRefreshEvent).
	if inv.gotPayload != samplePayload {
		t.Errorf("payload was not forwarded verbatim:\n got %s\nwant %s", inv.gotPayload, samplePayload)
	}
	// RequestResponse, not Event: the caller needs the refresh to have actually
	// happened (and its error surfaced) before resume reports success.
	if inv.gotType != "RequestResponse" {
		t.Errorf("invocation type = %q, want RequestResponse", inv.gotType)
	}
}

func TestForceGitHubTokenRefresh_NoScheduleReturnsSentinel(t *testing.T) {
	sched := &stubScheduleGetter{err: &schedulertypes.ResourceNotFoundException{}}
	inv := &stubLambdaInvoker{out: &lambda.InvokeOutput{StatusCode: 200}}

	err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc")
	if !errors.Is(err, ErrNoGitHubRefresher) {
		t.Fatalf("expected ErrNoGitHubRefresher, got %v", err)
	}
	if inv.callCount != 0 {
		t.Errorf("Lambda invoked %d times despite missing schedule", inv.callCount)
	}
}

func TestForceGitHubTokenRefresh_ScheduleLookupError(t *testing.T) {
	sched := &stubScheduleGetter{err: errors.New("throttled")}
	inv := &stubLambdaInvoker{out: &lambda.InvokeOutput{StatusCode: 200}}

	err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrNoGitHubRefresher) {
		t.Fatal("a transient lookup error must not be reported as a missing refresher")
	}
	if !strings.Contains(err.Error(), "throttled") {
		t.Errorf("error %q does not wrap the cause", err.Error())
	}
	if inv.callCount != 0 {
		t.Errorf("Lambda invoked %d times despite lookup failure", inv.callCount)
	}
}

func TestForceGitHubTokenRefresh_EmptyTargetInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  *scheduler.GetScheduleOutput
	}{
		{"nil target", &scheduler.GetScheduleOutput{}},
		{"nil input", &scheduler.GetScheduleOutput{Target: &schedulertypes.Target{}}},
		{"empty input", scheduleWithInput("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sched := &stubScheduleGetter{out: tc.out}
			inv := &stubLambdaInvoker{out: &lambda.InvokeOutput{StatusCode: 200}}

			err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc")
			if err == nil {
				t.Fatal("expected error for malformed schedule target")
			}
			if inv.callCount != 0 {
				t.Errorf("Lambda invoked %d times with no payload", inv.callCount)
			}
		})
	}
}

func TestForceGitHubTokenRefresh_FunctionErrorIsSurfaced(t *testing.T) {
	sched := &stubScheduleGetter{out: scheduleWithInput(samplePayload)}
	inv := &stubLambdaInvoker{out: &lambda.InvokeOutput{
		StatusCode:    200,
		FunctionError: awssdk.String("Unhandled"),
		Payload:       []byte(`{"errorMessage":"read SSM: AccessDenied"}`),
	}}

	err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc")
	if err == nil {
		t.Fatal("expected error when the refresher Lambda reports a function error")
	}
	// The whole point of the synchronous invoke is to surface a refresher that
	// has been failing silently on its 45-minute schedule.
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error %q does not carry the Lambda's own message", err.Error())
	}
}

func TestForceGitHubTokenRefresh_InvokeTransportError(t *testing.T) {
	sched := &stubScheduleGetter{out: scheduleWithInput(samplePayload)}
	inv := &stubLambdaInvoker{err: errors.New("connection reset")}

	err := ForceGitHubTokenRefresh(context.Background(), sched, inv, "km", "sb-abc")
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("expected wrapped transport error, got %v", err)
	}
}

func TestGitHubTokenResourceNames_HonourResourcePrefix(t *testing.T) {
	// A non-default install must never touch a sibling install's refresher.
	if got := GitHubTokenScheduleName("kph", "sb-1"); got != "kph-github-token-sb-1" {
		t.Errorf("schedule name = %q", got)
	}
	if got := GitHubTokenRefresherFunctionName("kph", "sb-1"); got != "kph-github-token-refresher-sb-1" {
		t.Errorf("function name = %q", got)
	}
}
