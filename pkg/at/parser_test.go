package at_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/at"
)

// fixedNow returns a fixed reference time for deterministic tests.
// 2026-04-10 12:00:00 UTC (a Friday)
func fixedNow() time.Time {
	return time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
}

// --- Parse: one-time expressions ---

func TestParse_10pmTomorrow(t *testing.T) {
	now := fixedNow() // Friday 2026-04-10 12:00 UTC
	spec, err := at.Parse("10pm tomorrow", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.IsRecurring {
		t.Errorf("expected IsRecurring=false, got true")
	}
	// Should be 2026-04-11T22:00:00
	want := "at(2026-04-11T22:00:00)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
	if spec.HumanExpr != "10pm tomorrow" {
		t.Errorf("HumanExpr: got %q, want %q", spec.HumanExpr, "10pm tomorrow")
	}
}

func TestParse_NextTuesdayAt2pm(t *testing.T) {
	now := fixedNow() // Friday 2026-04-10 12:00 UTC
	spec, err := at.Parse("next tuesday at 2pm", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.IsRecurring {
		t.Errorf("expected IsRecurring=false, got true")
	}
	// Next Tuesday from Friday 2026-04-10 is 2026-04-14T14:00:00
	want := "at(2026-04-14T14:00:00)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_In30Minutes(t *testing.T) {
	now := fixedNow() // 2026-04-10 12:00 UTC
	spec, err := at.Parse("in 30 minutes", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.IsRecurring {
		t.Errorf("expected IsRecurring=false, got true")
	}
	want := "at(2026-04-10T12:30:00)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_TonightAt11pm(t *testing.T) {
	now := fixedNow() // 2026-04-10 12:00 UTC
	spec, err := at.Parse("tonight at 11pm", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.IsRecurring {
		t.Errorf("expected IsRecurring=false, got true")
	}
	want := "at(2026-04-10T23:00:00)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

// --- Parse: recurring expressions ---

func TestParse_EveryThursdayAt3pm(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every thursday at 3pm", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "cron(0 15 ? * 5 *)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_EveryDayAt9am(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every day at 9am", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "cron(0 9 * * ? *)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_DailyAt9am(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("daily at 9am", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "cron(0 9 * * ? *)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_EveryMondayAt830am(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every monday at 8:30am", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "cron(30 8 ? * 2 *)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_EveryFridayAt5pm(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every friday at 5pm", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "cron(0 17 ? * 6 *)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_Every2Hours(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every 2 hours", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "rate(2 hours)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

func TestParse_Every30Minutes(t *testing.T) {
	now := fixedNow()
	spec, err := at.Parse("every 30 minutes", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !spec.IsRecurring {
		t.Errorf("expected IsRecurring=true, got false")
	}
	want := "rate(30 minutes)"
	if spec.Expression != want {
		t.Errorf("expression: got %q, want %q", spec.Expression, want)
	}
}

// --- Parse: error cases ---

func TestParse_GibberishReturnsError(t *testing.T) {
	now := fixedNow()
	_, err := at.Parse("gibberish nonsense xyz", now)
	if err == nil {
		t.Error("expected error for unparseable input, got nil")
	}
}

func TestParse_EmptyReturnsError(t *testing.T) {
	now := fixedNow()
	_, err := at.Parse("", now)
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

// --- ValidateCron ---

func TestValidateCron_ValidExpression(t *testing.T) {
	err := at.ValidateCron("cron(0 15 ? * 5 *)")
	if err != nil {
		t.Errorf("expected nil error for valid cron, got: %v", err)
	}
}

func TestValidateCron_UnixCronReturnsError(t *testing.T) {
	err := at.ValidateCron("0 15 * * 5")
	if err == nil {
		t.Error("expected error for unix cron format (missing cron() wrapper), got nil")
	}
}

func TestValidateCron_BothDOMAndDOWSetReturnsError(t *testing.T) {
	// Both day-of-month (15) and day-of-week (THU) are set — one must be ?
	err := at.ValidateCron("cron(0 15 15 * THU *)")
	if err == nil {
		t.Error("expected error when both day-of-month and day-of-week are non-?, got nil")
	}
}

func TestValidateCron_GarbageReturnsError(t *testing.T) {
	err := at.ValidateCron("garbage")
	if err == nil {
		t.Error("expected error for garbage input, got nil")
	}
}

// --- SanitizeScheduleName ---

func TestSanitizeScheduleName_AlreadyValid(t *testing.T) {
	result, err := at.SanitizeScheduleName("kill-sb-a1b2c3d4-10pm-tomorrow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "kill-sb-a1b2c3d4-10pm-tomorrow"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestSanitizeScheduleName_SpacesAndInvalidChars(t *testing.T) {
	result, err := at.SanitizeScheduleName("kill sb-a1b2c3d4 at 10pm!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "kill-sb-a1b2c3d4-at-10pm"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestSanitizeScheduleName_TruncatesAt64Chars(t *testing.T) {
	long := "this-is-a-very-long-schedule-name-that-exceeds-sixty-four-characters-maximum"
	result, err := at.SanitizeScheduleName(long)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) > 64 {
		t.Errorf("result length %d exceeds 64 chars: %q", len(result), result)
	}
}

func TestSanitizeScheduleName_EmptyReturnsError(t *testing.T) {
	_, err := at.SanitizeScheduleName("")
	if err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

func TestSanitizeScheduleName_AllInvalidCharsReturnsError(t *testing.T) {
	_, err := at.SanitizeScheduleName("!!!@@@###")
	if err == nil {
		t.Error("expected error when all chars stripped, got nil")
	}
}

// --- GenerateScheduleName ---

func TestGenerateScheduleName_WithSandboxID(t *testing.T) {
	result := at.GenerateScheduleName("kill", "sb-a1b2c3d4", "10pm tomorrow")
	want := "km-at-kill-sb-a1b2c3d4-10pm-tomorrow"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestGenerateScheduleName_WithoutSandboxID(t *testing.T) {
	result := at.GenerateScheduleName("create", "", "every thursday at 3pm")
	want := "km-at-create-every-thursday-at-3pm"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestGenerateScheduleName_AlwaysValid(t *testing.T) {
	// Result must always be <= 64 chars and match valid chars
	result := at.GenerateScheduleName("destroy", "sb-very-long-sandbox-id-xxxx", "every monday at 8:30am Pacific time with extra words")
	if len(result) > 64 {
		t.Errorf("generated name length %d exceeds 64 chars: %q", len(result), result)
	}
}

// --- Day-of-week mapping correctness ---

func TestDayOfWeekMapping_Sunday(t *testing.T) {
	spec, err := at.Parse("every sunday at 10am", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "cron(0 10 ? * 1 *)"
	if spec.Expression != want {
		t.Errorf("Sunday mapping: got %q, want %q", spec.Expression, want)
	}
}

func TestDayOfWeekMapping_Saturday(t *testing.T) {
	spec, err := at.Parse("every saturday at 6am", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "cron(0 6 ? * 7 *)"
	if spec.Expression != want {
		t.Errorf("Saturday mapping: got %q, want %q", spec.Expression, want)
	}
}

func TestDayOfWeekMapping_Wednesday(t *testing.T) {
	spec, err := at.Parse("every wednesday at noon", fixedNow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "cron(0 12 ? * 4 *)"
	if spec.Expression != want {
		t.Errorf("Wednesday mapping: got %q, want %q", spec.Expression, want)
	}
}
