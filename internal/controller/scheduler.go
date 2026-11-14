// Package controller implements the reconciliation and scheduling engine for ZeroPolicies.
package controller

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
)

// ScheduleDecision indicates whether a workload should be active or sleeping.
type ScheduleDecision string

const (
	// DecisionActive indicates the workload should have desired replicas running.
	DecisionActive ScheduleDecision = "Active"
	// DecisionSleep indicates the workload should be scaled to 0.
	DecisionSleep ScheduleDecision = "Sleep"
	// DecisionNoChange indicates no scheduled action is required at the current time.
	DecisionNoChange ScheduleDecision = "NoChange"
)

// EvaluateSchedule checks whether the current time matches sleep or wake windows.
//
// Parameters:
//   - spec: The ScheduleSpec containing SleepCron, WakeCron, and optional Timezone.
//   - now: The reference timestamp to evaluate against.
//
// Behavior:
//   - If spec is nil, returns DecisionNoChange.
//   - If Timezone is specified, now is converted to the target timezone.
//   - If SleepCron matches, returns DecisionSleep.
//   - If WakeCron matches, returns DecisionActive.
//   - Otherwise, returns DecisionNoChange.
func EvaluateSchedule(spec *v1alpha1.ScheduleSpec, now time.Time) (ScheduleDecision, error) {
	if spec == nil {
		return DecisionNoChange, nil
	}

	evalTime := now
	if spec.Timezone != "" {
		loc, err := time.LoadLocation(spec.Timezone)
		if err != nil {
			return DecisionNoChange, fmt.Errorf("invalid timezone '%s': %w", spec.Timezone, err)
		}
		evalTime = evalTime.In(loc)
	}

	if spec.SleepCron != "" {
		matches, err := matchCron(spec.SleepCron, evalTime)
		if err != nil {
			return DecisionNoChange, fmt.Errorf("invalid sleepCron '%s': %w", spec.SleepCron, err)
		}
		if matches {
			return DecisionSleep, nil
		}
	}

	if spec.WakeCron != "" {
		matches, err := matchCron(spec.WakeCron, evalTime)
		if err != nil {
			return DecisionNoChange, fmt.Errorf("invalid wakeCron '%s': %w", spec.WakeCron, err)
		}
		if matches {
			return DecisionActive, nil
		}
	}

	return DecisionNoChange, nil
}

// IsInactive determines if a workload has been idle longer than its configured idleTimeout.
//
// Parameters:
//   - inactivity: The InactivitySpec containing Enabled and IdleTimeout string (e.g. "30m").
//   - lastActivity: Timestamp of the last detected network activity.
//   - now: Current reference time.
//
// Edge cases:
//   - If inactivity is nil or not enabled, returns false.
//   - If lastActivity is zero or nil, returns false to avoid premature sleep on startup.
func IsInactive(inactivity *v1alpha1.InactivitySpec, lastActivity *time.Time, now time.Time) (bool, error) {
	if inactivity == nil || !inactivity.Enabled || lastActivity == nil || lastActivity.IsZero() {
		return false, nil
	}

	timeout, err := time.ParseDuration(inactivity.IdleTimeout)
	if err != nil {
		return false, fmt.Errorf("invalid idleTimeout format '%s': %w", inactivity.IdleTimeout, err)
	}

	if timeout <= 0 {
		return false, nil
	}

	return now.Sub(*lastActivity) >= timeout, nil
}

// matchCron performs standard 5-part cron matching (minute, hour, day-of-month, month, day-of-week).
// Supports wildcards (*), exact numbers (15), comma lists (1,2,3), and ranges (1-5).
func matchCron(expr string, t time.Time) (bool, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false, fmt.Errorf("cron expression must contain 5 fields, got %d", len(fields))
	}

	minuteMatch, err := matchCronField(fields[0], t.Minute(), 0, 59)
	if err != nil || !minuteMatch {
		return false, err
	}

	hourMatch, err := matchCronField(fields[1], t.Hour(), 0, 23)
	if err != nil || !hourMatch {
		return false, err
	}

	domMatch, err := matchCronField(fields[2], t.Day(), 1, 31)
	if err != nil || !domMatch {
		return false, err
	}

	monthMatch, err := matchCronField(fields[3], int(t.Month()), 1, 12)
	if err != nil || !monthMatch {
		return false, err
	}

	// In cron, 0 or 7 is Sunday, 1 is Monday ... 6 is Saturday
	dow := int(t.Weekday())
	dowMatch, err := matchCronField(fields[4], dow, 0, 7)
	if err != nil || !dowMatch {
		return false, err
	}

	return true, nil
}

// matchCronField tests a single cron field against an integer value.
func matchCronField(pattern string, val, min, max int) (bool, error) {
	if pattern == "*" {
		return true, nil
	}

	for _, part := range strings.Split(pattern, ",") {
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return false, fmt.Errorf("invalid range '%s'", part)
			}
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil {
				return false, fmt.Errorf("non-numeric range '%s'", part)
			}
			if val >= start && val <= end {
				return true, nil
			}
		} else {
			num, err := strconv.Atoi(part)
			if err != nil {
				return false, fmt.Errorf("non-numeric value '%s'", part)
			}
			if num == 7 && min == 0 && max == 7 && val == 0 {
				return true, nil // Support 7 as Sunday
			}
			if num == val {
				return true, nil
			}
		}
	}

	return false, nil
}
