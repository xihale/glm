// Package codex anchors a ChatGPT Codex 5-hour window.
//
// A Codex window starts at the account's first request after the previous one
// ends, not at the reset. /wham/usage reports an idle window as 0% used with a
// placeholder reset a full window ahead. A live window's reset is sooner than
// that. The daemon waits for the reported reset, then pings only when the
// window is not open.
package codex

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/xihale/glm/pkg/glm"
)

const (
	// DefaultModel is the cheap GPT-6 the warm-up asks (magpie warmPick).
	DefaultModel = "gpt-6-luna"
	// DefaultEffort keeps the ping on the cheapest reasoning level luna lists.
	DefaultEffort = "low"
	// PingText is the one user turn. Short on purpose: the request only
	// exists to start the window.
	PingText = "hello"

	// WarmSlack matches magpie: an idle window's reset sits a whole span
	// out, so anything within 10 minutes of that is still "not started".
	WarmSlack = 10 * time.Minute
	// IdleRecheck is the floor wait when a computed wake is not in the
	// future (quota read failed, clock moved back).
	IdleRecheck = 5 * time.Minute
	// PostPingWait is how long to wait after a hello before reading usage
	// again. The read right after the ping usually still shows the idle
	// placeholder — the window the ping just started looks exactly like one
	// that never started. Waiting out WarmSlack plus a margin past the
	// classification boundary lets the next read see the real reset
	// instead of triggering another hello.
	PostPingWait = WarmSlack + time.Minute
	// ResumeSlack is how far wall time must run ahead of the monotonic
	// timer before a wake counts as suspend. Go timers use CLOCK_MONOTONIC,
	// which does not advance while the machine sleeps.
	ResumeSlack = 2 * time.Second

	fiveSeconds = int64(5 * 60 * 60)
)

// Window is one Codex allowance bucket.
type Window struct {
	Name        string
	UsedPercent float64
	Span        time.Duration
	Reset       time.Time
}

// Open reports whether the window is running at now. A reset that has
// passed, or a placeholder a full span ahead with nothing used, is not open:
// only a request starts the next one.
func (w Window) Open(now time.Time) bool {
	if w.Reset.IsZero() || !w.Reset.After(now) {
		return false
	}
	if w.Span <= 0 {
		return true
	}
	return w.Reset.Sub(now) < w.Span-WarmSlack
}

// Usage is one /wham/usage read: the 5h bucket the daemon anchors, and the
// weekly bucket it only displays.
type Usage struct {
	Plan string
	Five Window
	Week Window
}

// Quota maps the 5h bucket onto the shared status shape. Remaining is
// 0-100. An unopened window stays at 100 with a zero reset so callers do
// not treat the placeholder as a live countdown.
func (u Usage) Quota(now time.Time) *glm.QuotaStatus {
	remaining := int64(math.Round(100 - u.Five.UsedPercent))
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	reset := u.Five.Reset
	if !u.Five.Open(now) {
		remaining = 100
		reset = time.Time{}
	}
	return &glm.QuotaStatus{
		Used:      100 - remaining,
		Limit:     100,
		Remaining: remaining,
		ResetTime: reset,
		Found:     true,
		Raw:       fmt.Sprintf("5h %.4g%% used, open=%v", u.Five.UsedPercent, u.Five.Open(now)),
	}
}

// Action is what one check of the 5h window should do.
type Action int

const (
	// ActionWait means the window is open: sleep until its reset.
	ActionWait Action = iota
	// ActionPing means the window is not open: send the hello.
	ActionPing
)

// Plan is one check's decision. Next is the wall-clock moment to look
// again when Action is ActionWait.
type Plan struct {
	Action Action
	Next   time.Time
	Why    string
}

// PlanCheck decides a check at now. An open window is left alone until its
// reset; anything else gets the ping.
func PlanCheck(w Window, now time.Time) Plan {
	if w.Open(now) {
		return Plan{Action: ActionWait, Next: w.Reset, Why: "window open"}
	}
	return Plan{Action: ActionPing, Why: "window not open"}
}

// NextAfterPing is the next check after a hello. A window the ping started
// is waited out; a read that still shows the idle placeholder is the fresh
// window's lag, so wait out PostPingWait rather than ping again.
func NextAfterPing(w Window, now time.Time) time.Time {
	if w.Open(now) && w.Reset.After(now) {
		return w.Reset
	}
	return now.Add(PostPingWait)
}

// SleptThrough reports that wall time jumped ahead of the monotonic sleep,
// which is what a suspend looks like when the timer finally fires.
func SleptThrough(wallElapsed, monoElapsed time.Duration) bool {
	return wallElapsed > monoElapsed+ResumeSlack
}

type wireUsage struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Primary   *wireWindow `json:"primary_window"`
		Secondary *wireWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type wireWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

func (w wireWindow) window() Window {
	out := Window{
		Name:        durationName(w.LimitWindowSeconds),
		UsedPercent: w.UsedPercent,
		Span:        time.Duration(w.LimitWindowSeconds) * time.Second,
	}
	if w.ResetAt > 0 {
		out.Reset = time.Unix(w.ResetAt, 0)
	}
	return out
}

func durationName(seconds int64) string {
	if seconds > 0 && seconds%(24*60*60) == 0 {
		return fmt.Sprintf("%d days", seconds/(24*60*60))
	}
	if seconds > 0 && seconds%(60*60) == 0 {
		return fmt.Sprintf("%d hours", seconds/(60*60))
	}
	return "allowance"
}

// ParseUsage reads a /wham/usage body. The 5h bucket is whichever window's
// span is nearer five hours; the other one is weekly.
func ParseUsage(body []byte) (Usage, error) {
	var raw wireUsage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Usage{}, fmt.Errorf("parse usage: %w", err)
	}
	five, week, ok := pickFive(raw.RateLimit.Primary, raw.RateLimit.Secondary)
	if !ok {
		return Usage{}, fmt.Errorf("no 5h window in usage")
	}
	return Usage{Plan: raw.PlanType, Five: five, Week: week}, nil
}

func pickFive(primary, secondary *wireWindow) (five, week Window, ok bool) {
	var ws []Window
	for _, w := range []*wireWindow{primary, secondary} {
		if w != nil && w.LimitWindowSeconds > 0 {
			ws = append(ws, w.window())
		}
	}
	if len(ws) == 0 {
		return Window{}, Window{}, false
	}
	best := 0
	bestDist := abs64(int64(ws[0].Span/time.Second) - fiveSeconds)
	for i := 1; i < len(ws); i++ {
		d := abs64(int64(ws[i].Span/time.Second) - fiveSeconds)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	five = ws[best]
	if len(ws) == 2 {
		week = ws[1-best]
	}
	return five, week, true
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
