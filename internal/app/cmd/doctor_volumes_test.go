package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func volEvent(kind, mp, reason string, at time.Time) cwltypes.FilteredLogEvent {
	ts := at.UnixMilli()
	msg := `{"event_type":"` + kind + `","source":"km-volumes","detail":{"mountpoint":"` + mp + `","volume_id":"vol-1","reason":"` + reason + `"}}`
	return cwltypes.FilteredLogEvent{Message: awssdk.String(msg), Timestamp: &ts}
}

func TestDoctor_AdditionalVolumes_LatestEventPerMountpointWins(t *testing.T) {
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	cw := &fakeCWLogsFilter{events: []cwltypes.FilteredLogEvent{
		volEvent("volume_mount_refused", "/repos", "size: live 30 GiB, expected 10 GiB", now.Add(-2*time.Hour)),
		volEvent("volume_mounted", "/repos", "", now.Add(-1*time.Hour)), // a later resume healed it
		volEvent("volume_mounted", "/data", "", now.Add(-3*time.Hour)),
		volEvent("volume_mount_refused", "/data", "filesystem UUID mismatch", now.Add(-30*time.Minute)), // still broken
	}}
	lister := &fakeRunningSandboxLister{ids: []string{"sb-vol"}}
	got := checkAdditionalVolumes(context.Background(), cw, lister, "/km/sandboxes/", now)
	if got.Status != CheckWarn {
		t.Fatalf("want WARN, got %+v", got)
	}
	if !strings.Contains(got.Message, "sb-vol /data") || strings.Contains(got.Message, "/repos") {
		t.Errorf("only /data is still refused; got %q", got.Message)
	}
	if !strings.Contains(cw.gotFilterPattern, "volume_mount_refused") || !strings.Contains(cw.gotFilterPattern, "volume_mounted") {
		t.Errorf("filter must ask for both event kinds: %q", cw.gotFilterPattern)
	}
	if cw.gotLogGroup != "/km/sandboxes/sb-vol/" {
		t.Errorf("log group = %q", cw.gotLogGroup)
	}
}

func TestDoctor_AdditionalVolumes_OKAndSkip(t *testing.T) {
	now := time.Now()
	lister := &fakeRunningSandboxLister{ids: []string{"sb-a", "sb-b"}}
	cw := &fakeCWLogsFilter{events: []cwltypes.FilteredLogEvent{volEvent("volume_mounted", "/repos", "", now)}}
	if got := checkAdditionalVolumes(context.Background(), cw, lister, "/km/sandboxes/", now); got.Status != CheckOK {
		t.Errorf("all mounted must be OK, got %+v", got)
	}
	if got := checkAdditionalVolumes(context.Background(), &fakeCWLogsFilter{}, lister, "/km/sandboxes/", now); got.Status != CheckOK {
		t.Errorf("no km-volumes events at all must be OK, got %+v", got)
	}
	if got := checkAdditionalVolumes(context.Background(), nil, lister, "/km/sandboxes/", now); got.Status != CheckSkipped {
		t.Errorf("nil client must SKIP, got %+v", got)
	}
}
