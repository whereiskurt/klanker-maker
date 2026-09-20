package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const volumesStateJSON = `{"updatedAt":"2026-09-20T22:57:00Z","volumes":[
 {"mountpoint":"/data","volumeId":"vol-04d0b727020553414","outcome":"mounted","at":"2026-09-20T22:57:00Z"},
 {"mountpoint":"/repos","volumeId":"vol-0b914cbacf49fa119","outcome":"refused","step":"size","expected":"10737418240","actual":"live=32212254720 kernel=32212254720","reason":"size: live 30 GiB, kernel 30 GiB, expected 10 GiB","at":"2026-09-20T22:57:00Z"},
 {"mountpoint":"/models","outcome":"unvalidated","reason":"no manifest — created before km-volumes; recreate to protect","at":"2026-09-20T22:57:00Z"}
]}`

func TestFetchVolumeState_DecodesAndHandlesAbsence(t *testing.T) {
	r := &recordingSSM{stdout: volumesStateJSON}
	st, err := fetchVolumeState(context.Background(), r, "i-0abc")
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || len(st.Volumes) != 3 || st.Volumes[1].Outcome != "refused" || st.Volumes[1].Step != "size" {
		t.Errorf("decoded %+v", st)
	}
	if !strings.Contains(r.sent[0], "km-volumes status") {
		t.Errorf("probe must call km-volumes status; sent %q", r.sent[0])
	}

	// A box without km-volumes (older sandbox, or no additional volumes) is nil, not an error.
	r = &recordingSSM{stdout: "KM_NO_VOLUMES\n"}
	st, err = fetchVolumeState(context.Background(), r, "i-0abc")
	if err != nil || st != nil {
		t.Errorf("no-volumes box: got (%+v, %v), want (nil, nil)", st, err)
	}
}

func TestRenderVolumeLines(t *testing.T) {
	r := &recordingSSM{stdout: volumesStateJSON}
	st, _ := fetchVolumeState(context.Background(), r, "i-0abc")
	var out bytes.Buffer
	renderVolumeLines(&out, st, "sb-1")
	got := out.String()
	for _, want := range []string{
		"Volumes:",
		"✓ /data",
		"vol-04d0b727020553414",
		"✗ /repos",
		"REFUSED",
		"size: live 30 GiB, kernel 30 GiB, expected 10 GiB",
		"km-volumes repair /repos",
		"reboot",
		"? /models",
		"unvalidated",
		"docs/hibernate-volumes.md",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q:\n%s", want, got)
		}
	}
	// nil state renders nothing at all — no empty "Volumes:" header.
	out.Reset()
	renderVolumeLines(&out, nil, "sb-1")
	if out.Len() != 0 {
		t.Errorf("nil state must render nothing, got %q", out.String())
	}
}

func TestVolumeStateNeedsAttention(t *testing.T) {
	r := &recordingSSM{stdout: volumesStateJSON}
	st, _ := fetchVolumeState(context.Background(), r, "i-0abc")
	if !st.NeedsAttention() {
		t.Error("a refused volume needs attention")
	}
	st.Volumes = st.Volumes[:1]
	if st.NeedsAttention() {
		t.Error("all mounted must not need attention")
	}
}

// flakySSM fails the first n probes (SSM not online yet after StartInstances),
// then answers.
type flakySSM struct {
	recordingSSM
	failFirst int
	calls     int
}

func (f *flakySSM) SendCommand(ctx context.Context, in *ssm.SendCommandInput, o ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.calls++
	if f.calls <= f.failFirst {
		return nil, errors.New("InvalidInstanceId: Instances not in a valid state")
	}
	return f.recordingSSM.SendCommand(ctx, in, o...)
}

func TestResumeVolumeStateReport_WaitsThenPrints(t *testing.T) {
	f := &flakySSM{recordingSSM: recordingSSM{stdout: volumesStateJSON}, failFirst: 2}
	var out bytes.Buffer
	resumeVolumeStateReport(context.Background(), f, "i-0abc", "sb-1", &out)
	if f.calls != 3 {
		t.Errorf("expected 3 probes (2 not-yet + 1 answer), got %d", f.calls)
	}
	if !strings.Contains(out.String(), "✗ /repos") {
		t.Errorf("state not rendered: %q", out.String())
	}
}

func TestResumeVolumeStateReport_NoVolumesPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	resumeVolumeStateReport(context.Background(), &recordingSSM{stdout: "KM_NO_VOLUMES"}, "i-0abc", "sb-1", &out)
	if out.Len() != 0 {
		t.Errorf("no-volumes box must print nothing, got %q", out.String())
	}
	out.Reset()
	resumeVolumeStateReport(context.Background(), &recordingSSM{}, "", "sb-1", &out)
	if out.Len() != 0 {
		t.Errorf("no instance id must print nothing, got %q", out.String())
	}
}

func TestResumeVolumeStateReport_GivesUpAfterBudget(t *testing.T) {
	f := &flakySSM{failFirst: 1 << 20}
	var out bytes.Buffer
	resumeVolumeStateReport(context.Background(), f, "i-0abc", "sb-1", &out)
	if !strings.Contains(out.String(), "not readable yet") {
		t.Errorf("must say it gave up: %q", out.String())
	}
}
