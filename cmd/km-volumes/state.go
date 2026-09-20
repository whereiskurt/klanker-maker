package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	statePath   = "/var/lib/km/volumes.state"
	rebootsPath = "/var/lib/km/volumes.reboots"
	markerName  = ".km-mount-refused"
)

// Outcome is the per-volume result of the last verb that touched it.
type Outcome string

const (
	OutcomeMounted     Outcome = "mounted"
	OutcomeRefused     Outcome = "refused"
	OutcomeUnvalidated Outcome = "unvalidated" // pre-manifest box: mounted by BDM letter, unprotected
	OutcomeAbsent      Outcome = "absent"
	OutcomeAmbiguous   Outcome = "ambiguous"
	OutcomeLazy        Outcome = "lazy" // pre-sleep had to umount -l
	OutcomeRebooting   Outcome = "rebooting"
	OutcomeUnmounted   Outcome = "unmounted"
	OutcomeNotMounted  Outcome = "not-mounted"

	// outcomeOK is internal to validateOne: the ladder passed, mount not yet attempted.
	outcomeOK Outcome = "ok"
)

type VolumeState struct {
	Mountpoint string    `json:"mountpoint"`
	VolumeID   string    `json:"volumeId,omitempty"`
	Outcome    Outcome   `json:"outcome"`
	Step       string    `json:"step,omitempty"` // serial | size | fsuuid | superblock | mount
	Expected   string    `json:"expected,omitempty"`
	Actual     string    `json:"actual,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`

	source string // mount source resolved by validateOne; never persisted
}

type State struct {
	UpdatedAt         time.Time     `json:"updatedAt"`
	Volumes           []VolumeState `json:"volumes"`
	RebootedForResume string        `json:"rebootedForResume,omitempty"`
}

func writeState(sys System, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return sys.WriteFile(statePath, append(b, '\n'), 0o644)
}

// readState returns an empty State (not an error) when nothing has run yet.
func readState(sys System) (State, error) {
	var st State
	b, err := sys.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("parse %s: %w", statePath, err)
	}
	return st, nil
}

// anyRefused is true when any entry needs the operator (or the reboot policy).
func anyRefused(st State) bool {
	for _, v := range st.Volumes {
		switch v.Outcome {
		case OutcomeRefused, OutcomeAbsent, OutcomeAmbiguous:
			return true
		}
	}
	return false
}

// rebootedFor / markRebootedFor implement the one-reboot-per-resume guard
// (spec §5.5). The id is the boot id read by post-sleep BEFORE it reboots; a
// reboot mints a new boot id, so the box that comes back cannot match the
// guard and the next resume starts fresh — while a second post-sleep on the
// SAME boot (the guard file persisted) sees its own id and falls back to refuse.
func rebootedFor(sys System, resumeID string) bool {
	b, err := sys.ReadFile(rebootsPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == resumeID {
			return true
		}
	}
	return false
}

func markRebootedFor(sys System, resumeID string) {
	prev, _ := sys.ReadFile(rebootsPath)
	_ = sys.WriteFile(rebootsPath, append(prev, []byte(resumeID+"\n")...), 0o644)
}
