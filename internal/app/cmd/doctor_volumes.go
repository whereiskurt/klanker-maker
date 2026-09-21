package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// km doctor: additional EBS volumes that km-volumes refused to mount.
//
// km-volumes writes volume_mount_refused / volume_mounted events to the
// sandbox's audit stream on every boot and resume. A refusal is only news if
// nothing mounted that mountpoint SINCE — a resume that re-probed and mounted
// clears it — so the check takes the latest event per mountpoint over a
// window, the same shape as the presence and init-failed checks it sits next
// to. Read-only.

// volumeEventWindow bounds the FilterLogEvents read. A refusal older than this
// on a box that is still running would have been re-evaluated by a later
// boot or resume, which emits its own events.
const volumeEventWindow = 7 * 24 * time.Hour

const volumeEventFilterPattern = `{ ($.event_type = "volume_mount_refused") || ($.event_type = "volume_mounted") || ($.event_type = "volume_reboot") }`

type volumeAuditEvent struct {
	EventType string `json:"event_type"`
	Detail    struct {
		Mountpoint string `json:"mountpoint"`
		VolumeID   string `json:"volume_id"`
		Step       string `json:"step"`
		Reason     string `json:"reason"`
	} `json:"detail"`
	at time.Time
}

// latestVolumeEvents returns the newest event per mountpoint in the window.
func latestVolumeEvents(ctx context.Context, cw CWLogsFilterAPI, logGroup string, now time.Time) (map[string]volumeAuditEvent, error) {
	latest := map[string]volumeAuditEvent{}
	var next *string
	for {
		out, err := cw.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  awssdk.String(logGroup),
			FilterPattern: awssdk.String(volumeEventFilterPattern),
			StartTime:     awssdk.Int64(now.Add(-volumeEventWindow).UnixMilli()),
			NextToken:     next,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range out.Events {
			if e.Message == nil {
				continue
			}
			var ev volumeAuditEvent
			if json.Unmarshal([]byte(*e.Message), &ev) != nil || ev.Detail.Mountpoint == "" {
				continue
			}
			if e.Timestamp != nil {
				ev.at = time.UnixMilli(*e.Timestamp)
			}
			if prev, ok := latest[ev.Detail.Mountpoint]; !ok || !ev.at.Before(prev.at) {
				latest[ev.Detail.Mountpoint] = ev
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		next = out.NextToken
	}
	return latest, nil
}

// checkAdditionalVolumes WARNs naming every running sandbox with a mountpoint
// whose latest km-volumes event is a refusal. Skipped with zero AWS calls when
// either dependency is nil.
func checkAdditionalVolumes(ctx context.Context, cw CWLogsFilterAPI, lister runningSandboxLister, logGroupPrefix string, now time.Time) CheckResult {
	const name = "Additional volumes"
	if cw == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "no CloudWatch client or sandbox lister"}
	}
	ids, err := lister.ListRunningSandboxIDs(ctx)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list running sandboxes: %v", err)}
	}
	if len(ids) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no running sandboxes to check"}
	}
	var refused []string
	checked := 0
	for _, id := range ids {
		latest, lookErr := latestVolumeEvents(ctx, cw, logGroupPrefix+id+"/", now)
		if lookErr != nil || len(latest) == 0 {
			continue // unreadable stream, or a box with no km-volumes: nothing to report
		}
		checked++
		mps := make([]string, 0, len(latest))
		for mp := range latest {
			mps = append(mps, mp)
		}
		sort.Strings(mps)
		for _, mp := range mps {
			if ev := latest[mp]; ev.EventType == "volume_mount_refused" {
				refused = append(refused, fmt.Sprintf("%s %s: %s (%s)", id, mp, ev.Detail.Reason, ev.at.UTC().Format(time.RFC3339)))
			}
		}
	}
	if len(refused) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("%d sandbox(es) with km-volumes, no refused mounts", checked)}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d additional volume(s) refused by km-volumes and not mounted since:\n    %s", len(refused), strings.Join(refused, "\n    ")),
		Remediation: "The mountpoint is empty on purpose — the device behind it did not match the volume the box recorded at first boot. Reboot the sandbox to re-enumerate, or km shell --root <id> and run km-volumes repair <mountpoint>. See docs/hibernate-volumes.md.",
		Details:     refused,
	}
}
