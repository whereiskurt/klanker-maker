package main

import (
	"encoding/json"
	"os"
	"syscall"
	"time"
)

// auditPipePath is the FIFO km-audit-log reads; the bootstrap's init_failed
// event is the shape we copy. Detail keys are snake_case throughout because
// km doctor reads the stream and keys on them (mountpoint, volume_id, step,
// expected, actual, reason, policy).
var auditPipePath = "/run/km/audit-pipe"

// emitAudit is the one sink for audit events; a var so tests can capture what
// would have reached the FIFO.
var emitAudit = emitAuditToPipe

// emitAuditToPipe writes one JSON line to the audit FIFO and gives up silently
// on any error. The open is O_NONBLOCK so a missing reader returns ENXIO
// instead of hanging a verb that must always finish (pre-/post-sleep especially).
func emitAuditToPipe(eventType string, detail map[string]string) {
	line, err := json.Marshal(map[string]any{
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"sandbox_id": os.Getenv("KM_SANDBOX_ID"),
		"event_type": eventType,
		"source":     "km-volumes",
		"detail":     detail,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(auditPipePath, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// emitAuditMounted marks a successful mount so a reader of the stream can
// tell "refused, still broken" from "refused earlier, mounted on the last
// resume" by the LATEST event per mountpoint.
func emitAuditMounted(v Volume) {
	emitAudit("volume_mounted", map[string]string{
		"mountpoint": v.Mountpoint,
		"volume_id":  v.VolumeID,
	})
}

func emitAuditRefused(v Volume, vs VolumeState, policy string) {
	emitAudit("volume_mount_refused", map[string]string{
		"mountpoint": v.Mountpoint,
		"volume_id":  v.VolumeID,
		"outcome":    string(vs.Outcome),
		"step":       vs.Step,
		"expected":   vs.Expected,
		"actual":     vs.Actual,
		"reason":     vs.Reason,
		"policy":     policy,
	})
}

func emitAuditReboot(st State, resumeID string) {
	var mps string
	for _, v := range st.Volumes {
		if v.Outcome == OutcomeRebooting {
			if mps != "" {
				mps += ","
			}
			mps += v.Mountpoint
		}
	}
	emitAudit("volume_mismatch_reboot", map[string]string{
		"mountpoints": mps,
		"resume_id":   resumeID,
		"policy":      "reboot",
	})
}
