package cmd

import (
	"fmt"
	"testing"
	"time"

	"github.com/xihale/glm/pkg/config"
)

func TestMaybeWeeklyWake(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	regular := now.Add(4 * time.Hour) // e.g. the next schedule point
	weekly := now.Add(3 * time.Hour)  // weekly reset lands first

	next, stolen := maybeWeeklyWake(regular, weekly, now)
	if !stolen || !next.Equal(weekly) {
		t.Fatalf("weekly reset before regular target should steal the wake: got next=%v stolen=%v", next, stolen)
	}

	// Weekly reset after the regular target: no steal.
	weekly2 := now.Add(5 * time.Hour)
	next, stolen = maybeWeeklyWake(regular, weekly2, now)
	if stolen || !next.Equal(regular) {
		t.Fatalf("weekly reset after regular target must not steal: got next=%v stolen=%v", next, stolen)
	}

	// Weekly reset already passed: never a wake target (hot-loop guard).
	past := now.Add(-time.Minute)
	next, stolen = maybeWeeklyWake(regular, past, now)
	if stolen || !next.Equal(regular) {
		t.Fatalf("past weekly reset must not steal: got next=%v stolen=%v", next, stolen)
	}

	// Zero reset (not reported): no steal.
	next, stolen = maybeWeeklyWake(regular, time.Time{}, now)
	if stolen || !next.Equal(regular) {
		t.Fatalf("zero weekly reset must not steal: got next=%v stolen=%v", next, stolen)
	}
}

func TestWeeklyForScope(t *testing.T) {
	defer func() { config.Provider = "" }()
	config.Provider = "agy"

	on := true
	off := false

	// pools entry with explicit weekly wins over agy.weekly
	cfg := config.Config{
		AGY: config.AGYConfig{
			Weekly: true,
			Pools: map[string]config.AGYPoolConfig{
				"gemini": {Weekly: &off},
				"3p":     {}, // nil falls back to agy.weekly
			},
		},
	}
	if weeklyForScope(cfg, "gemini") {
		t.Fatal("pools.gemini.weekly=false should override agy.weekly=true")
	}
	if !weeklyForScope(cfg, "3p") {
		t.Fatal("pools entry without weekly falls back to agy.weekly=true")
	}

	// unlisted pool falls back to agy.weekly
	if !weeklyForScope(cfg, "9p") {
		t.Fatal("unlisted pool should fall back to agy.weekly=true")
	}

	// explicit true in pools entry
	cfg.AGY.Pools["9p"] = config.AGYPoolConfig{Weekly: &on}
	if !weeklyForScope(cfg, "9p") {
		t.Fatal("pools.9p.weekly=true should enable weekly refresh")
	}

	// glm has no weekly
	config.Provider = "glm"
	if weeklyForScope(cfg, "gemini") {
		t.Fatal("glm must never have weekly refresh")
	}
}

func TestWeeklyOnlyForScope(t *testing.T) {
	defer func() { config.Provider = "" }()
	config.Provider = "agy"

	cfg := config.Config{
		AGY: config.AGYConfig{
			Pools: map[string]config.AGYPoolConfig{
				"gemini": {},
				"3p":     {WeeklyOnly: true},
			},
		},
	}
	if !weeklyOnlyForScope(cfg, "3p") {
		t.Fatal("pools.3p.weekly_only=true should enable weekly keepalive")
	}
	if weeklyOnlyForScope(cfg, "gemini") {
		t.Fatal("pools entry without weekly_only must stay in schedule mode")
	}
	if weeklyOnlyForScope(cfg, "9p") {
		t.Fatal("unlisted pool must not be weekly-only (default comes from the map, never agy-wide)")
	}

	// the unnamed glm loop can never be weekly-only
	config.Provider = "glm"
	if weeklyOnlyForScope(cfg, "3p") {
		t.Fatal("glm must never run weekly-only")
	}
}

func TestActivationFailureWait(t *testing.T) {
	logf := func(string, ...interface{}) {}
	manual := config.ScheduleConfig{
		Timezone: "+8",
		Times:    []string{"04:30:00", "09:30:00", "14:30:00", "19:30:00"},
	}
	geoErr := fmt.Errorf(`warmup failed: 400 ({"error":{"code":400,"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}})`)
	transient := fmt.Errorf("warmup failed: 502 (bad gateway)")

	// Permanent refusal + manual schedule: sleep to the next schedule point
	// (strictly in the future, never a hot loop).
	fails := 0
	w := activationFailureWait(manual, &fails, geoErr, logf)
	if fails != 1 || w <= time.Second || w > 24*time.Hour {
		t.Fatalf("permanent refusal with manual schedule should wait for the next point, got fails=%d wait=%v", fails, w)
	}

	// Permanent refusal without a manual schedule: capped backoff, not 1 minute.
	w = activationFailureWait(config.ScheduleConfig{}, &fails, geoErr, logf)
	if w != activationBackoffCap {
		t.Fatalf("permanent refusal without schedule should use the cap, got %v", w)
	}

	// Transient failures: exponential backoff 1m, 2m, ... capped.
	fails = 0
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		w = activationFailureWait(manual, &fails, transient, logf)
		if w != want {
			t.Fatalf("transient failure %d: want %v, got %v", i+1, want, w)
		}
	}
	fails = 40 // far past the shift overflow guard
	if w = activationFailureWait(manual, &fails, transient, logf); w != activationBackoffCap {
		t.Fatalf("transient failure overflow should clamp to the cap, got %v", w)
	}
}
