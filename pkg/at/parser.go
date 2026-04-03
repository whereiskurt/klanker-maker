// Package at provides natural language time expression parsing for EventBridge Scheduler.
package at

import (
	"errors"
	"time"
)

// ScheduleSpec holds a parsed schedule expression for EventBridge Scheduler.
type ScheduleSpec struct {
	// Expression is the EventBridge Scheduler expression, e.g. "at(2026-04-10T22:00:00)",
	// "cron(0 15 ? * 5 *)", or "rate(2 hours)".
	Expression string

	// IsRecurring is true for cron() and rate() expressions.
	IsRecurring bool

	// HumanExpr is the original human-readable input.
	HumanExpr string
}

// Parse converts a human-readable time expression into an EventBridge Scheduler ScheduleSpec.
// Stub — not yet implemented.
func Parse(expr string, now time.Time) (ScheduleSpec, error) {
	return ScheduleSpec{}, errors.New("not implemented")
}

// ValidateCron validates an EventBridge cron() expression string.
// Stub — not yet implemented.
func ValidateCron(expr string) error {
	return errors.New("not implemented")
}

// SanitizeScheduleName sanitizes a schedule name to EventBridge constraints.
// Stub — not yet implemented.
func SanitizeScheduleName(name string) (string, error) {
	return "", errors.New("not implemented")
}

// GenerateScheduleName builds a sanitized EventBridge-compatible schedule name.
// Stub — not yet implemented.
func GenerateScheduleName(command, sandboxID, timeExpr string) string {
	return ""
}
