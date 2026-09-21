package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// km-volumes state, as read from a sandbox. The sidecar (cmd/km-volumes)
// validates every additional EBS volume by LIVE NVMe identity before mounting
// it — on every boot and after every hibernate/resume — and writes the outcome
// to /var/lib/km/volumes.state. km status, km resume and km doctor surface it
// so a refused /repos is never mistaken for a healthy empty directory.
// Design: docs/superpowers/specs/2026-09-20-hibernate-volume-validation-design.md

// volumeState mirrors cmd/km-volumes VolumeState.
type volumeState struct {
	Mountpoint string    `json:"mountpoint"`
	VolumeID   string    `json:"volumeId,omitempty"`
	Outcome    string    `json:"outcome"` // mounted | refused | unvalidated | absent | ambiguous | lazy | rebooting | not-mounted | unmounted
	Step       string    `json:"step,omitempty"`
	Expected   string    `json:"expected,omitempty"`
	Actual     string    `json:"actual,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`
}

// volumesState mirrors cmd/km-volumes State.
type volumesState struct {
	UpdatedAt time.Time     `json:"updatedAt"`
	Volumes   []volumeState `json:"volumes"`
}

// NeedsAttention reports whether any volume is in a state an operator must act on.
func (s *volumesState) NeedsAttention() bool {
	if s == nil {
		return false
	}
	for _, v := range s.Volumes {
		switch v.Outcome {
		case "refused", "absent", "ambiguous", "lazy", "rebooting":
			return true
		}
	}
	return false
}

// volumeStateProbe is the one-round-trip SSM script. A box without km-volumes
// (no additional volumes, or created before the sidecar shipped) prints the
// sentinel instead of failing, so callers can tell "nothing to show" from
// "could not ask".
const volumeStateProbe = `test -x /opt/km/bin/km-volumes && /opt/km/bin/km-volumes status 2>/dev/null || echo KM_NO_VOLUMES`

// fetchVolumeState asks the box for its km-volumes state. (nil, nil) means the
// box has no km-volumes; an error means the probe itself failed.
func fetchVolumeState(ctx context.Context, ssmClient SSMSendAPI, instanceID string) (*volumesState, error) {
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, volumeStateProbe)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" || strings.HasPrefix(out, "KM_NO_VOLUMES") {
		return nil, nil
	}
	var st volumesState
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return nil, fmt.Errorf("decode km-volumes status: %w", err)
	}
	return &st, nil
}

// renderVolumeLines prints the Volumes: section. Nothing at all for a nil
// state — no empty header on boxes that have no additional volumes.
func renderVolumeLines(w io.Writer, st *volumesState, sandboxID string) {
	if st == nil || len(st.Volumes) == 0 {
		return
	}
	fmt.Fprintf(w, "Volumes:\n")
	for _, v := range st.Volumes {
		switch v.Outcome {
		case "mounted":
			fmt.Fprintf(w, "  ✓ %-10s %s\n", v.Mountpoint, v.VolumeID)
		case "unvalidated":
			fmt.Fprintf(w, "  ? %-10s unvalidated (%s)\n", v.Mountpoint, v.Reason)
		case "rebooting":
			fmt.Fprintf(w, "  ⟳ %-10s REBOOTING to re-enumerate (%s)\n", v.Mountpoint, v.Reason)
		default: // refused, absent, ambiguous, lazy
			fmt.Fprintf(w, "  ✗ %-10s %s: %s\n", v.Mountpoint, strings.ToUpper(v.Outcome), v.Reason)
			fmt.Fprintf(w, "               fix: reboot the box to re-enumerate, or km shell --root %s then km-volumes repair %s; see docs/hibernate-volumes.md\n", sandboxID, v.Mountpoint)
		}
	}
}

// Seams for the two blocking waits; the test binary shrinks them. The budget
// is sized for a HIBERNATION resume: the SSM agent only answers once the RAM
// image is restored, measured live at ~150 s on a t3.medium, and the
// post-resume unit itself may then take up to ~2 min to settle and re-bind.
// It is the one moment the operator is watching, so waiting is worth it;
// every path out is best-effort and the resume itself has already succeeded.
var (
	resumeVolumePollInterval = 5 * time.Second
	resumeVolumePollBudget   = 240 * time.Second
)

// resumeVolumeStateReport polls the freshly started box for its km-volumes
// state and prints the Volumes: lines. SSM comes online well after
// StartInstances returns, so a probe error is "not yet" and we keep going
// until the budget runs out; a nil state (no km-volumes on the box) prints
// nothing. Never fails the resume.
func resumeVolumeStateReport(ctx context.Context, ssmClient SSMSendAPI, instanceID, sandboxID string, w io.Writer) {
	if instanceID == "" {
		return
	}
	deadline := time.Now().Add(resumeVolumePollBudget)
	waited := false
	for {
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		st, err := fetchVolumeState(pctx, ssmClient, instanceID)
		cancel()
		if err == nil {
			if st != nil {
				fmt.Fprintf(w, "  ✓ km-volumes reported\n")
				renderVolumeLines(w, st, sandboxID)
			}
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			fmt.Fprintf(w, "  [info] additional-volume state not readable yet (%v); check later with km status %s\n", err, sandboxID)
			return
		}
		if !waited {
			fmt.Fprintf(w, "  waiting for the box to report its additional volumes (up to %s)...\n", resumeVolumePollBudget)
			waited = true
		}
		time.Sleep(resumeVolumePollInterval)
	}
}

// statusVolumeStateLookup is km status's read of the box. Seam: the test
// binary replaces it with a nil-returning func. Only running boxes are asked
// (a stopped box cannot answer, and its last state is moot).
var statusVolumeStateLookup = func(ctx context.Context, rec *kmaws.SandboxRecord) *volumesState {
	if rec == nil || rec.Status != "running" {
		return nil
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return nil
	}
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := fetchVolumeState(pctx, ssm.NewFromConfig(awsCfg), instanceID)
	if err != nil {
		return nil
	}
	return st
}
