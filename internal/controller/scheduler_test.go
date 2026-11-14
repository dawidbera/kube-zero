package controller

import (
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
)

// TestEvaluateSchedule tests cron scheduling logic and timezone handling.
func TestEvaluateSchedule(t *testing.T) {
	// Reference: 2026-11-16 19:30:00 (Monday evening)
	refTime := time.Date(2026, 11, 16, 19, 30, 0, 0, time.UTC)

	spec := &v1alpha1.ScheduleSpec{
		SleepCron: "30 19 * * 1-5", // 19:30 Mon-Fri
		WakeCron:  "00 07 * * 1-5", // 07:00 Mon-Fri
		Timezone:  "UTC",
	}

	decision, err := EvaluateSchedule(spec, refTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != DecisionSleep {
		t.Errorf("expected DecisionSleep, got %s", decision)
	}

	// Reference: 2026-11-16 07:00:00 (Monday morning)
	wakeTime := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)
	decision, err = EvaluateSchedule(spec, wakeTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != DecisionActive {
		t.Errorf("expected DecisionActive, got %s", decision)
	}

	// Reference: 2026-11-16 12:00:00 (Monday noon - no change)
	noonTime := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	decision, err = EvaluateSchedule(spec, noonTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != DecisionNoChange {
		t.Errorf("expected DecisionNoChange, got %s", decision)
	}
}

// TestIsInactive tests inactivity timeout calculation.
func TestIsInactive(t *testing.T) {
	now := time.Now()
	lastAct := now.Add(-35 * time.Minute)

	spec := &v1alpha1.InactivitySpec{
		Enabled:     true,
		IdleTimeout: "30m",
	}

	inactive, err := IsInactive(spec, &lastAct, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inactive {
		t.Errorf("expected inactive to be true after 35 minutes idle with 30m timeout")
	}

	// Recent activity (10 min ago)
	recentAct := now.Add(-10 * time.Minute)
	inactive, err = IsInactive(spec, &recentAct, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inactive {
		t.Errorf("expected inactive to be false with recent activity")
	}
}
