package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/log"
	"github.com/xihale/glm/pkg/ui"

	"github.com/spf13/cobra"
)

var activeCmd = &cobra.Command{
	Use:   "active",
	Short: "Send heartbeat to activate GLM quota",
	Long: `Send heartbeat to activate GLM quota.

Verifies activation by polling quota after heartbeat.
Use --force to activate even when quota is active.
With --service, runs as a daemon: activate, sleep until next run, repeat.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.Current.APIKey == "" {
			return fmt.Errorf("no API key configured. Run 'glm login' first")
		}

		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		serviceMode, _ := cmd.Flags().GetBool("service")

		if serviceMode {
			return runDaemon(debug, force)
		}

		client := glm.NewClient()
		client.SetDebug(debug)

		// One-shot mode
		quota, err := client.Activate(force, false)
		if err != nil {
			ui.Error(fmt.Sprintf("Activation failed: %v", err))
			return err
		}
		printQuotaResult(quota)
		return nil
	},
}

func runDaemon(debug, force bool) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)

	log.Infof("Daemon started (auto=%v manual=%v)",
		config.Current.Schedule.Auto, config.Current.Schedule.Manual())

	exhausted := false
	// True when the current sleep targets a reported reset: on wake, a few
	// seconds of leftover wait is a normal landing, not drift to re-decide.
	resetTarget := false
	for {
		if err := config.Reload(); err != nil {
			log.Errorf("Config reload failed, keeping current: %v", err)
		}
		client := glm.NewClient()
		client.SetDebug(debug)

		// Query first, decide second: the heartbeat only fires once this
		// quota check says the window is actually fresh. Waking straight
		// into a heartbeat would anchor a window that is still hours from
		// its reset when the API's timing drifted.
		quota, err := client.GetQuota()
		if err != nil {
			log.Errorf("Get quota failed: %v", err)
			switch sleepOr(sigCh, reloadCh, time.Minute) {
			case sleepSignal:
				log.Infof("Received signal, shutting down")
				return nil
			case sleepReload:
				log.Infof("Received SIGHUP, reloading config")
			}
			continue
		}

		if quota.Remaining >= 100 || force {
			log.Infof("Activating...")
			quota, err = client.Activate(force, false)
			if err != nil {
				log.Errorf("Activation failed: %v", err)
				switch sleepOr(sigCh, reloadCh, time.Minute) {
				case sleepSignal:
					log.Infof("Received signal, shutting down")
					return nil
				case sleepReload:
					log.Infof("Received SIGHUP, reloading config")
				}
				continue
			}
		}

		// Quota exhausted: a heartbeat cannot refresh it. The reported reset
		// decides the wake: within scheduleNearThreshold of the nearest
		// schedule point it is taken (hold, then anchor right at the reset);
		// farther out it is skipped — the schedule point anchors instead, and
		// the quota (which auto-restores at the reset) sits un-anchored
		// meanwhile. Without a manual schedule the daemon always waits out
		// the reset; without a reset time it polls.
		if quota.Remaining <= 0 {
			untilReset := time.Until(quota.ResetTime)
			ref, schedPoint, near := resetNearSchedule(quota.ResetTime)
			var wait time.Duration
			switch {
			case untilReset <= 0:
				if !exhausted {
					exhausted = true
					log.Infof("Quota exhausted (no reset time reported) — polling every %s",
						exhaustedPollInterval)
				}
				wait = exhaustedPollInterval

			case resetTarget && untilReset <= imminentWindow:
				// Normal landing: this wake was planned for this reset and
				// only seconds are left — hold for it, don't re-decide.
				if !exhausted {
					exhausted = true
					log.Infof("Quota exhausted — reset landing in %s, holding",
						untilReset.Round(time.Second))
				}
				wait = untilReset + time.Second

			case near:
				if !exhausted {
					exhausted = true
					log.Infof("Quota exhausted — reset at %s is within %v of schedule %s — taking it",
						quota.ResetTime.Local().Format("15:04:05"),
						scheduleNearThreshold,
						ref.Local().Format("15:04:05"))
				}
				resetTarget = true
				wait = time.Until(quota.ResetTime) - resetQueryLead
				if wait < 0 {
					wait = 0
				}

			case !schedPoint.IsZero():
				if !exhausted {
					exhausted = true
					log.Infof("Quota exhausted — reset at %s is far from schedule %s, sleeping to the schedule instead",
						quota.ResetTime.Local().Format("15:04:05"),
						ref.Local().Format("15:04:05"))
				}
				resetTarget = false
				wait = time.Until(schedPoint)

			default:
				if !exhausted {
					exhausted = true
					log.Infof("Quota exhausted — waiting for reset at %s (%s)",
						quota.ResetTime.Local().Format("2006-01-02 15:04:05"),
						glm.FormatTimeUntil(quota.ResetTime))
				}
				resetTarget = true
				wait = untilReset
			}

			switch sleepOr(sigCh, reloadCh, wait) {
			case sleepSignal:
				log.Infof("Received signal, shutting down")
				return nil
			case sleepReload:
				log.Infof("Received SIGHUP, reloading config")
			}
			continue
		}
		if exhausted {
			log.Infof("Quota available again")
			exhausted = false
		}
		resetTarget = false

		// Calculate next run
		next, atReset, err := nextWake(quota)
		if err != nil {
			log.Errorf("Calculate next run: %v", err)
			return err
		}

		wait := time.Until(next)
		if atReset {
			// Wake a few seconds early so the quota query lands just before
			// the boundary and the grab decision runs on fresh data.
			wait -= resetQueryLead
		} else if wait < minDaemonSleep {
			// Target may already have passed by now — never spin-loop on the API.
			wait = minDaemonSleep
		}
		if wait < 0 {
			wait = 0
		}
		log.Infof("Next activation at %s (sleeping %s)",
			next.Local().Format("2006-01-02 15:04:05"), glm.FormatTimeUntil(next))

		switch sleepOr(sigCh, reloadCh, wait) {
		case sleepSignal:
			log.Infof("Received signal, shutting down")
			return nil
		case sleepReload:
			log.Infof("Received SIGHUP, reloading config")
		}
	}
}

const (
	exhaustedPollInterval = 10 * time.Second
	minDaemonSleep        = 10 * time.Second
	// Manual-schedule rule: a reported reset within ±this of the nearest
	// schedule point is taken (wake at the reset, anchor there); resets
	// farther from the schedule grid are skipped — the next schedule point
	// anchors instead.
	scheduleNearThreshold = 90 * time.Minute
	// Reset wakes fire this many seconds early so the quota query lands
	// just before the boundary and the decision runs on fresh data.
	resetQueryLead = 5 * time.Second
	// A reset this close when we woke for it is a normal landing: hold the
	// few remaining seconds instead of re-running the decision.
	imminentWindow = 30 * time.Second
)

type sleepOutcome int

const (
	sleepDone sleepOutcome = iota
	sleepSignal
	sleepReload
)

// sleepOr waits for d, interrupted by shutdown or config-reload signals.
func sleepOr(sigCh, reloadCh chan os.Signal, d time.Duration) sleepOutcome {
	if d < 0 {
		d = 0
	}
	select {
	case <-sigCh:
		return sleepSignal
	case <-reloadCh:
		return sleepReload
	case <-time.After(d):
		return sleepDone
	}
}

// nextWake returns when the daemon should next wake, and whether that target
// is a reported reset (woken resetQueryLead early to query before deciding).
//
// A heartbeat is only useful once the current 5h window has expired: before
// the reset it either no-ops or anchors the window prematurely (and every
// premature anchor shifts all later cycles that much earlier, compounding).
// So with a reported reset the daemon wakes at the reset — unless a manual
// schedule is configured and the reset lies farther than scheduleNearThreshold
// from the nearest schedule point: anchoring that far from any scheduled use is
// wasted and drifts the whole cadence, so the schedule point takes the anchor
// instead. Resets near the grid keep the reset wake. With no reset time, the
// manual schedule (or the auto 4h re-check) applies.
func nextWake(quota *glm.QuotaStatus) (time.Time, bool, error) {
	sched := config.Current.Schedule
	if !quota.ResetTime.IsZero() {
		if sched.Manual() {
			s, err := nextScheduledTime(sched)
			if err != nil {
				log.Errorf("Next scheduled time: %v — keeping reset wake", err)
			} else if c, err := closestScheduledTime(sched, quota.ResetTime); err != nil {
				log.Errorf("Closest scheduled time: %v — keeping reset wake", err)
			} else if absDuration(quota.ResetTime.Sub(c)) > scheduleNearThreshold {
				log.Infof("Reset at %s is far from schedule %s — following schedule instead",
					quota.ResetTime.Local().Format("15:04:05"),
					c.Local().Format("15:04:05"))
				return s, false, nil
			}
		}
		return quota.ResetTime, true, nil
	}

	if sched.Auto {
		// No reset reported: re-check halfway into a nominal cycle.
		return time.Now().Add(4 * time.Hour), false, nil
	}
	s, err := nextScheduledTime(sched)
	return s, false, err
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// resetNearSchedule evaluates a reported reset against the manual schedule
// grid. ref is the grid occurrence nearest the reset and anchors the
// scheduleNearThreshold comparison; schedPoint is the next occurrence after
// now, where a skipped reset defers the wake to. Without a manual schedule
// both are zero and the daemon always wakes at the reset.
func resetNearSchedule(reset time.Time) (ref, schedPoint time.Time, near bool) {
	sched := config.Current.Schedule
	if !sched.Manual() {
		return time.Time{}, time.Time{}, false
	}
	s, err := nextScheduledTime(sched)
	if err != nil {
		log.Errorf("Next scheduled time: %v", err)
		return time.Time{}, time.Time{}, false
	}
	c, err := closestScheduledTime(sched, reset)
	if err != nil {
		log.Errorf("Closest scheduled time: %v", err)
		return time.Time{}, s, false
	}
	return c, s, absDuration(reset.Sub(c)) <= scheduleNearThreshold
}

func nextScheduledTime(sched config.ScheduleConfig) (time.Time, error) {
	loc, err := parseTimezone(sched.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad timezone %q: %w", sched.Timezone, err)
	}

	now := time.Now().In(loc)
	var earliest time.Time

	for _, t := range sched.Times {
		parts := splitTime(t)
		if len(parts) != 3 {
			continue
		}
		h, _ := parseRange(parts[0], 0, 23)
		m, _ := parseRange(parts[1], 0, 59)
		s, _ := parseRange(parts[2], 0, 59)

		// Today's occurrence
		candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, loc)
		// If already passed, use tomorrow
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}

		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}

	if earliest.IsZero() {
		return time.Time{}, fmt.Errorf("no valid times in schedule")
	}
	return earliest, nil
}

// closestScheduledTime returns the schedule occurrence nearest to t, searching
// a day on either side of it. Reset-vs-schedule decisions must compare against
// this, not the next occurrence after now: the daemon wakes exactly on a
// schedule point, so a reset landing seconds past it belongs to that point —
// measured against the next point it is always ~5h away and would be skipped
// on every wake.
func closestScheduledTime(sched config.ScheduleConfig, t time.Time) (time.Time, error) {
	loc, err := parseTimezone(sched.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad timezone %q: %w", sched.Timezone, err)
	}

	t = t.In(loc)
	var nearest time.Time
	var minDiff time.Duration

	for _, tt := range sched.Times {
		parts := splitTime(tt)
		if len(parts) != 3 {
			continue
		}
		h, _ := parseRange(parts[0], 0, 23)
		m, _ := parseRange(parts[1], 0, 59)
		s, _ := parseRange(parts[2], 0, 59)

		for _, dayOffset := range []int{-1, 0, 1} {
			candidate := time.Date(t.Year(), t.Month(), t.Day(), h, m, s, 0, loc).AddDate(0, 0, dayOffset)
			diff := absDuration(candidate.Sub(t))
			if nearest.IsZero() || diff < minDiff {
				nearest, minDiff = candidate, diff
			}
		}
	}

	if nearest.IsZero() {
		return time.Time{}, fmt.Errorf("no valid times in schedule")
	}
	return nearest, nil
}

func splitTime(s string) []string {
	parts := make([]string, 0, 3)
	for _, p := range splitStr(s, ":") {
		parts = append(parts, p)
	}
	return parts
}

func splitStr(s, sep string) []string {
	var result []string
	for {
		idx := indexOf(s, sep)
		if idx < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

func printQuotaResult(q *glm.QuotaStatus) {
	if q.Remaining <= 0 {
		reset := "unknown"
		if !q.ResetTime.IsZero() {
			reset = q.ResetTime.Local().Format("15:04:05")
		}
		ui.Error(fmt.Sprintf("Quota exhausted (0%% remaining) — nothing to activate until reset (%s)", reset))
		if !q.ResetTime.IsZero() {
			fmt.Printf("  Reset at: %s (%s)\n",
				ui.Style(q.ResetTime.Local().Format("15:04:05"), ui.Cyan, ui.Bold),
				ui.Dimmed(glm.FormatTimeUntil(q.ResetTime)))
		}
		return
	}

	if q.Remaining >= 100 {
		ui.Info(fmt.Sprintf("Quota: %s remaining (may already be fresh)",
			ui.Style("100%", ui.Green, ui.Bold)))
	} else {
		ui.Success(fmt.Sprintf("Activated — %s remaining",
			ui.Style(fmt.Sprintf("%d%%", q.Remaining), ui.Green, ui.Bold)))
	}

	if !q.ResetTime.IsZero() {
		fmt.Printf("  Reset at: %s (%s)\n",
			ui.Style(q.ResetTime.Local().Format("15:04:05"), ui.Cyan, ui.Bold),
			ui.Dimmed(glm.FormatTimeUntil(q.ResetTime)))
	}
}

func init() {
	rootCmd.AddCommand(activeCmd)
	activeCmd.Flags().BoolP("force", "f", false, "Force activation even if quota is active")
	activeCmd.Flags().Bool("service", false, "Daemon mode: activate, sleep, repeat")
	activeCmd.Flags().Bool("debug", false, "Show raw API responses")
}
