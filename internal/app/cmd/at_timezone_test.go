package cmd

import (
	"testing"
	"time"

	atpkg "github.com/whereiskurt/klanker-maker/pkg/at"
)

// TestScheduleTimezone_ValidIANA pins that scheduleTimezone never returns Go's
// "Local" sentinel for recurring schedules (which EventBridge rejects with
// "Invalid timezone Local"), and always returns a value EventBridge accepts.
func TestScheduleTimezone_ValidIANA(t *testing.T) {
	if got := scheduleTimezone(atpkg.ScheduleSpec{IsRecurring: false}); got != "UTC" {
		t.Errorf("one-time schedule tz = %q, want UTC", got)
	}

	got := scheduleTimezone(atpkg.ScheduleSpec{IsRecurring: true})
	if got == "" || got == "Local" {
		t.Fatalf("recurring tz = %q must be a real IANA name, not the Local sentinel", got)
	}
	if _, err := time.LoadLocation(got); err != nil {
		t.Fatalf("recurring tz %q is not a loadable IANA timezone (EventBridge would reject it): %v", got, err)
	}
}
