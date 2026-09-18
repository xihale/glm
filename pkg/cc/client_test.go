package cc

import (
	"testing"
	"time"
)

func TestParseCredits(t *testing.T) {
	// Live window captured 2026-09-18 (individual plan).
	body := []byte(`{"credits":{"belowThreshold":false,"creditThreshold":0,"monthlyCredits":69.8915976782,"purchasedCredits":0,"freeCredits":0},"windowLimits":{"limited":true,"exceeded":null,"fiveHour":{"used":0.1084023218,"cap":14,"exceeded":false,"resetAt":1789743059393},"weekly":{"used":0.1084023218,"cap":35,"exceeded":false,"resetAt":1790329859393}}}`)

	cr, err := parseCredits(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cr.Monthly < 69.89 || cr.Monthly > 69.90 {
		t.Fatalf("monthly: got %v", cr.Monthly)
	}
	if cr.Five.Cap != 14 || cr.Five.Used < 0.1084 || cr.Five.Used > 0.1085 {
		t.Fatalf("fiveHour: got used=%v cap=%v", cr.Five.Used, cr.Five.Cap)
	}
	want := time.UnixMilli(1789743059393)
	if !cr.Five.ResetAt.Equal(want) {
		t.Fatalf("fiveHour reset: want %v, got %v", want, cr.Five.ResetAt)
	}
	if cr.Weekly.Cap != 35 {
		t.Fatalf("weekly cap: got %v", cr.Weekly.Cap)
	}
}

func TestParseCreditsExceededNull(t *testing.T) {
	// "exceeded":null must parse as false, not an error.
	body := []byte(`{"credits":{"monthlyCredits":10},"windowLimits":{"exceeded":null,"fiveHour":{"used":0,"cap":14,"exceeded":false,"resetAt":0},"weekly":{"used":0,"cap":35,"exceeded":false,"resetAt":0}}}`)
	cr, err := parseCredits(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cr.Five.Used != 0 {
		t.Fatal("fresh window should parse with zero used")
	}
	if !cr.Five.ResetAt.IsZero() {
		t.Fatalf("never-started window should have zero reset, got %v", cr.Five.ResetAt)
	}
}

func TestQuotaMapping(t *testing.T) {
	live := time.Now().Add(4 * time.Hour)

	// Live window with a rounding-relevant sliver of usage: 99%, no touch.
	cr := &Credits{Five: Window{Used: 0.1084, Cap: 14, ResetAt: live}}
	q := cr.Quota()
	if q.Remaining != 99 || q.Used != 1 {
		t.Fatalf("live window: want 99%% remaining, got %d", q.Remaining)
	}

	// Fresh, never started: 100%, no reset.
	cr = &Credits{Five: Window{Used: 0, Cap: 14}}
	q = cr.Quota()
	if q.Remaining != 100 || !q.ResetTime.IsZero() {
		t.Fatalf("fresh window: want 100%% and zero reset, got %d %v", q.Remaining, q.ResetTime)
	}

	// Lapsed window: reset passed while used still shows the last cycle —
	// the bucket is rolling, so this reads fresh for the state machine.
	cr = &Credits{Five: Window{Used: 0.1084, Cap: 14, ResetAt: time.Now().Add(-time.Minute)}}
	q = cr.Quota()
	if q.Remaining != 100 {
		t.Fatalf("lapsed window: want 100%% remaining, got %d", q.Remaining)
	}

	// Exhausted inside the live window: 0%, wait for the reported reset.
	cr = &Credits{Five: Window{Used: 14, Cap: 14, Exceeded: true, ResetAt: live}}
	q = cr.Quota()
	if q.Remaining != 0 {
		t.Fatalf("exhausted window: want 0%% remaining, got %d", q.Remaining)
	}

	// Half-used inside the live window rounds away from the boundary.
	cr = &Credits{Five: Window{Used: 7, Cap: 14, ResetAt: live}}
	if q := cr.Quota(); q.Remaining != 50 {
		t.Fatalf("half-used window: want 50%%, got %d", q.Remaining)
	}
}
