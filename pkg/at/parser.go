// Package at provides natural language time expression parsing for EventBridge Scheduler.
package at

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
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

// EventBridge day-of-week mapping: 1=SUN, 2=MON, 3=TUE, 4=WED, 5=THU, 6=FRI, 7=SAT.
// This differs from unix cron where 0=SUN.
var ebDOW = map[string]int{
	"sun": 1, "sunday": 1,
	"mon": 2, "monday": 2,
	"tue": 3, "tuesday": 3,
	"wed": 4, "wednesday": 4,
	"thu": 5, "thursday": 5,
	"fri": 6, "friday": 6,
	"sat": 7, "saturday": 7,
}

// recurringKeywords triggers the custom recurring parser path.
var recurringKeywords = []string{"every", "each", "weekly", "daily", "monthly", "everyday"}

// ratePattern matches "every N hours" or "every N minutes".
var ratePattern = regexp.MustCompile(`(?i)every\s+(\d+)\s+(hours?|minutes?)`)

// dayAtPattern matches "every <day> at <time>" or "each <day> at <time>".
var dayAtPattern = regexp.MustCompile(`(?i)(?:every|each)\s+(\w+)\s+at\s+(\S+)`)

// dailyPattern matches "every day at <time>" or "daily at <time>".
var dailyPattern = regexp.MustCompile(`(?i)(?:every\s*day|daily)\s+at\s+(\S+)`)

// noonAliases maps simple time aliases.
var noonAliases = map[string]string{
	"noon":     "12:00pm",
	"midnight": "12:00am",
}

// isRecurring returns true if the expression looks like a recurring schedule.
func isRecurring(expr string) bool {
	lower := strings.ToLower(expr)
	for _, kw := range recurringKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// parseTimeStr parses a time string like "3pm", "8:30am", "14:00", "noon" into hour and minute.
func parseTimeStr(s string) (hour, minute int, err error) {
	// Apply aliases
	lower := strings.ToLower(s)
	if alias, ok := noonAliases[lower]; ok {
		s = alias
		lower = alias
	}

	// Try "15:04" (24h)
	for _, layout := range []string{"15:04", "3:04pm", "3pm", "15"} {
		t, e := time.Parse(layout, s)
		if e == nil {
			return t.Hour(), t.Minute(), nil
		}
	}

	// Try lowercase with am/pm
	for _, layout := range []string{"3:04pm", "3pm"} {
		t, e := time.Parse(layout, lower)
		if e == nil {
			return t.Hour(), t.Minute(), nil
		}
	}

	return 0, 0, fmt.Errorf("cannot parse time %q", s)
}

// parseRecurring handles recurring expressions and returns a ScheduleSpec.
func parseRecurring(expr string) (ScheduleSpec, error) {
	lower := strings.ToLower(strings.TrimSpace(expr))

	// "every N hours" / "every N minutes" → rate()
	if m := ratePattern.FindStringSubmatch(lower); m != nil {
		n := m[1]
		unit := m[2]
		// Normalize unit: ensure plural
		if !strings.HasSuffix(unit, "s") {
			unit += "s"
		}
		return ScheduleSpec{
			Expression:  fmt.Sprintf("rate(%s %s)", n, unit),
			IsRecurring: true,
			HumanExpr:   expr,
		}, nil
	}

	// "every day at <time>" / "daily at <time>" → cron(M H * * ? *)
	if m := dailyPattern.FindStringSubmatch(lower); m != nil {
		h, min, err := parseTimeStr(m[1])
		if err != nil {
			return ScheduleSpec{}, fmt.Errorf("cannot parse time in %q: %w", expr, err)
		}
		return ScheduleSpec{
			Expression:  fmt.Sprintf("cron(%d %d * * ? *)", min, h),
			IsRecurring: true,
			HumanExpr:   expr,
		}, nil
	}

	// "every <day> at <time>" → cron(M H ? * DOW *)
	if m := dayAtPattern.FindStringSubmatch(lower); m != nil {
		dayStr := strings.ToLower(m[1])
		timeStr := m[2]

		dow, ok := ebDOW[dayStr]
		if !ok {
			return ScheduleSpec{}, fmt.Errorf("unknown day of week %q in %q", dayStr, expr)
		}

		h, min, err := parseTimeStr(timeStr)
		if err != nil {
			return ScheduleSpec{}, fmt.Errorf("cannot parse time in %q: %w", expr, err)
		}

		return ScheduleSpec{
			Expression:  fmt.Sprintf("cron(%d %d ? * %d *)", min, h, dow),
			IsRecurring: true,
			HumanExpr:   expr,
		}, nil
	}

	return ScheduleSpec{}, fmt.Errorf("cannot parse recurring expression %q", expr)
}

// compactTimeRe matches compact time formats like "845AM", "915pm", "1030AM"
// (3-4 digits immediately followed by AM/PM with no colon separator).
var compactTimeRe = regexp.MustCompile(`(?i)\b(\d{1,2})(\d{2})\s*(am|pm)\b`)

// normalizeCompactTimes rewrites compact times like "845AM" → "8:45AM"
// so the olebedev/when library can parse them.
func normalizeCompactTimes(expr string) string {
	return compactTimeRe.ReplaceAllString(expr, "${1}:${2}${3}")
}

// Parse converts a human-readable time expression into an EventBridge Scheduler ScheduleSpec.
//
// One-time expressions ("10pm tomorrow", "in 30 minutes") return an at() expression.
// Recurring expressions ("every thursday at 3pm", "daily at 9am") return cron() or rate().
func Parse(expr string, now time.Time) (ScheduleSpec, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ScheduleSpec{}, fmt.Errorf("schedule expression must not be empty")
	}

	if isRecurring(expr) {
		return parseRecurring(expr)
	}

	// Normalize compact time formats (e.g. "845AM" → "8:45AM") before
	// passing to the when library which requires colon-separated times.
	expr = normalizeCompactTimes(expr)

	// One-time: use olebedev/when for natural language parsing
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)

	result, err := w.Parse(expr, now)
	if err != nil {
		return ScheduleSpec{}, fmt.Errorf("failed to parse %q: %w", expr, err)
	}
	if result == nil {
		return ScheduleSpec{}, fmt.Errorf("cannot parse time expression %q", expr)
	}

	t := result.Time.UTC()
	atExpr := fmt.Sprintf("at(%04d-%02d-%02dT%02d:%02d:%02d)", t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second())

	return ScheduleSpec{
		Expression:  atExpr,
		IsRecurring: false,
		HumanExpr:   expr,
	}, nil
}

// ValidateCron validates an EventBridge cron() expression string.
// EventBridge cron has 6 fields: minutes hours day-of-month month day-of-week year.
// Either day-of-month or day-of-week must be ?, not both or neither.
func ValidateCron(expr string) error {
	expr = strings.TrimSpace(expr)

	// Must start with "cron(" and end with ")"
	if !strings.HasPrefix(expr, "cron(") || !strings.HasSuffix(expr, ")") {
		return fmt.Errorf("expression must be wrapped in cron(): got %q", expr)
	}

	inner := expr[5 : len(expr)-1]
	fields := strings.Fields(inner)
	if len(fields) != 6 {
		return fmt.Errorf("EventBridge cron requires 6 fields, got %d in %q", len(fields), expr)
	}

	// fields: [min] [hour] [dom] [month] [dow] [year]
	dom := fields[2]
	dow := fields[4]

	domIsQ := dom == "?"
	dowIsQ := dow == "?"

	// Exactly one of dom or dow must be ?
	if domIsQ == dowIsQ {
		if domIsQ {
			return fmt.Errorf("day-of-month and day-of-week cannot both be ? in %q", expr)
		}
		return fmt.Errorf("exactly one of day-of-month or day-of-week must be ? in %q: dom=%s dow=%s", expr, dom, dow)
	}

	return nil
}

// validNameChars matches EventBridge schedule name valid characters.
var validNameChars = regexp.MustCompile(`[^0-9a-zA-Z\-_.]`)

// multiDash collapses multiple consecutive dashes.
var multiDash = regexp.MustCompile(`-{2,}`)

// SanitizeScheduleName sanitizes a name to EventBridge Scheduler constraints:
// max 64 chars, only [0-9a-zA-Z-_.] allowed. Spaces become dashes. Returns error on empty result.
func SanitizeScheduleName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("schedule name must not be empty")
	}

	// Replace spaces with dashes
	name = strings.ReplaceAll(name, " ", "-")

	// Strip any characters not in [0-9a-zA-Z-_.]
	name = validNameChars.ReplaceAllString(name, "")

	// Collapse multiple consecutive dashes
	name = multiDash.ReplaceAllString(name, "-")

	// Trim leading/trailing dashes
	name = strings.Trim(name, "-")

	if name == "" {
		return "", fmt.Errorf("schedule name is empty after sanitization")
	}

	// Truncate to 64 characters
	if len(name) > 64 {
		name = name[:64]
		// Trim trailing dashes after truncation
		name = strings.TrimRight(name, "-")
	}

	return name, nil
}

// GenerateScheduleName builds a sanitized EventBridge-compatible schedule name from its parts.
// Format: "km-at-{command}-{sandboxID}-{timeExpr}" or "km-at-{command}-{timeExpr}" if sandboxID is empty.
// The result is always valid per EventBridge constraints (sanitized, <= 64 chars).
func GenerateScheduleName(command, sandboxID, timeExpr string) string {
	var parts []string
	parts = append(parts, "km-at")
	if command != "" {
		parts = append(parts, command)
	}
	if sandboxID != "" {
		parts = append(parts, sandboxID)
	}
	if timeExpr != "" {
		parts = append(parts, timeExpr)
	}

	raw := strings.Join(parts, "-")

	sanitized, err := SanitizeScheduleName(raw)
	if err != nil {
		// Fallback: return a safe default name
		return "km-at-schedule"
	}
	return sanitized
}
