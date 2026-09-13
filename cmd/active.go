package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
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
	Short: "Send heartbeat to activate quota (glm or agy provider)",
	Long: `Send a minimal request to activate the quota window.

Works with both providers selected in config ('glm' or 'agy'):
  - glm: GLM coding plan 5h tokens window (heartbeat to chat API)
  - agy: Antigravity/Gemini 5h pool (minimal warmup generateContent)

Verifies activation by polling quota after the request.
Use --force to activate even when the window is live.
agy accepts --pool gemini|3p to activate a specific pool (default from config).
With --service, runs as a daemon: activate, sleep until next run, repeat.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		serviceMode, _ := cmd.Flags().GetBool("service")
		pool, _ := cmd.Flags().GetString("pool")

		if serviceMode {
			return runDaemon(debug, force)
		}

		var client providerClient
		var err error
		if pool != "" {
			if config.EffectiveProvider() != "agy" {
				return fmt.Errorf("--pool only applies to --provider agy")
			}
			switch pool {
			case "gemini", "3p":
			default:
				return fmt.Errorf("unknown pool %q (want gemini or 3p)", pool)
			}
			client, err = newProviderClientWithPool(pool)
		} else {
			client, err = newProviderClient()
		}
		if err != nil {
			return err
		}
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

// runDaemon starts one anchor loop per configured pool. glm runs a single
// unnamed loop (its behavior is unchanged); agy runs one loop per pool from
// agy.pools, or a single loop for the legacy `pool` setting. Each loop owns
// its own schedule, state machine, and signal handling.
func runDaemon(debug, force bool) error {
	cfg := config.Snapshot()
	pools := daemonPools(cfg)

	if len(pools) == 1 {
		return runDaemonPool(pools[0], "", debug, force)
	}

	var wg sync.WaitGroup
	for _, pool := range pools {
		wg.Add(1)
		go func(pool string) {
			defer wg.Done()
			runDaemonPool(pool, "["+pool+"] ", debug, force)
		}(pool)
	}
	log.Infof("Daemon started for pools: %v", pools)
	wg.Wait()
	return nil
}

func runDaemonPool(pool, prefix string, debug, force bool) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)

	// plog prefixes messages with the pool name when multiple loops run, so
	// interleaved journal lines stay attributable.
	plog := func(format string, a ...interface{}) {
		log.Infof(prefix+format, a...)
	}
	perr := func(format string, a ...interface{}) {
		log.Errorf(prefix+format, a...)
	}

	if err := config.Reload(); err != nil {
		perr("Config reload failed, keeping current: %v", err)
	}
	cfg := config.Snapshot()
	sched := scheduleForScope(cfg, pool)
	if weeklyOnlyForScope(cfg, pool) {
		plog("Daemon started (weekly keepalive, no 5h anchoring)")
	} else {
		plog("Daemon started (auto=%v manual=%v)", sched.Auto, sched.Manual())
	}

	exhausted := false
	// Consecutive failed activations — drives the retry backoff (reset on
	// success; a permanent refusal ignores it and waits for the schedule).
	actFails := 0
	// True when the current sleep targets a reported reset: on wake, a few
	// seconds of leftover wait is a normal landing, not drift to re-decide.
	resetTarget := false
	// Set when the current sleep targets the weekly bucket's reset (opt-in
	// passive refresh): on the next wake a warmup fires right at the reset.
	weeklyTarget := false
	var weeklyTargetAt time.Time
	// Instant of the daemon's most recent heartbeat. A reported window no
	// newer than this was not moved by our own anchor — see the stale check.
	var lastAnchor time.Time
	for {
		if err := config.Reload(); err != nil {
			perr("Config reload failed, keeping current: %v", err)
		}
		cfg := config.Snapshot()
		weeklyOn := weeklyForScope(cfg, pool)
		wOnly := weeklyOnlyForScope(cfg, pool)
		client, err := newProviderClientFor(cfg, pool)
		if err != nil {
			perr("Init provider client: %v", err)
			switch sleepOr(sigCh, reloadCh, time.Minute) {
			case sleepSignal:
				plog("Received signal, shutting down")
				return nil
			case sleepReload:
				plog("Received SIGHUP, reloading config")
			}
			continue
		}
		client.SetDebug(debug)

		// Query first, decide second: the heartbeat only fires once this
		// quota check says the window is actually fresh. Waking straight
		// into a heartbeat would anchor a window that is still hours from
		// its reset when the API's timing drifted.
		quota, err := client.GetQuota()
		if err != nil {
			perr("Get quota failed: %v", err)
			switch sleepOr(sigCh, reloadCh, time.Minute) {
			case sleepSignal:
				plog("Received signal, shutting down")
				return nil
			case sleepReload:
				plog("Received SIGHUP, reloading config")
			}
			continue
		}

		// Weekly-only pool: the 5h window stays passive — the daemon never
		// anchors it on a schedule. The loop only keeps the weekly bucket
		// rolling: a window nobody ever requests stays "not started" (and a
		// lapsed one stays lapsed), so fire a warmup at startup while the
		// bucket is fresh, then again whenever a reported reset has passed.
		// Between fires, sleep to the reported reset.
		if wOnly {
			wr, wrOK := weeklyResetOf(client)
			if !wrOK || wr.IsZero() {
				plog("Weekly reset not reported — re-checking in %v", weeklyOnlyRecheck)
				switch sleepOr(sigCh, reloadCh, weeklyOnlyRecheck) {
				case sleepSignal:
					plog("Received signal, shutting down")
					return nil
				case sleepReload:
					plog("Received SIGHUP, reloading config")
				}
				continue
			}
			fresh := false
			if f, ok := weeklyRemainingOf(client); ok {
				// 0.9999 rather than 1.0: float jitter around a full bucket
				// must read as fresh, while our own warmup (~0.02%) must not.
				fresh = f >= 0.9999
			}
			resetPassed := !wr.After(time.Now())
			if fresh && (lastAnchor.IsZero() || resetPassed) {
				if hc, ok := client.(interface{ SendHeartbeat() error }); ok {
					plog("Weekly keep-alive — firing warmup (bucket fresh, reset %s)",
						wr.Local().Format("2006-01-02 15:04:05"))
					if err := hc.SendHeartbeat(); err != nil {
						perr("Weekly keep-alive failed: %v", err)
					} else {
						lastAnchor = time.Now()
					}
				}
			}
			target := wr
			if !target.After(time.Now()) {
				target = time.Now().Add(weeklyOnlyRecheck)
			}
			wait := time.Until(target) - resetQueryLead
			if wait < 0 {
				wait = 0
			}
			plog("Next weekly check at %s (sleeping %s)",
				target.Local().Format("2006-01-02 15:04:05"), glm.FormatTimeUntil(target))
			switch sleepOr(sigCh, reloadCh, wait) {
			case sleepSignal:
				plog("Received signal, shutting down")
				return nil
			case sleepReload:
				plog("Received SIGHUP, reloading config")
			}
			continue
		}

		// A live window (reset still ahead) must not be heartbeated: the
		// request is absorbed by it, or worse re-anchors it early. But once
		// the reset has passed — or no reset is reported at all while quota
		// remains, which is how the API shows a lapsed window — only a
		// request re-anchors it. Waiting for the API to show 100% waits for
		// a request that never comes: this daemon is the request. A window
		// no newer than our own last heartbeat is declined — re-anchoring it
		// would loop; the schedule decides instead.
		stale := quota.Remaining > 0 &&
			(quota.ResetTime.IsZero() || !quota.ResetTime.After(time.Now())) &&
			quota.ResetTime.After(lastAnchor)
		fired := false
		if quota.Remaining >= 100 || stale || force {
			if stale {
				lapse := "no reset reported"
				if !quota.ResetTime.IsZero() {
					lapse = fmt.Sprintf("reset %s passed",
						quota.ResetTime.Local().Format("15:04:05"))
				}
				plog("Window lapsed (%s, %d%% reported) — re-anchoring", lapse, quota.Remaining)
			}
			plog("Activating...")
			quota, err = client.Activate(force || stale, false)
			if err != nil {
				perr("Activation failed: %v", err)
				switch sleepOr(sigCh, reloadCh, activationFailureWait(sched, &actFails, err, plog)) {
				case sleepSignal:
					plog("Received signal, shutting down")
					return nil
				case sleepReload:
					plog("Received SIGHUP, reloading config")
				}
				continue
			}
			fired = true
			lastAnchor = time.Now()
			actFails = 0
		}

		// Quota exhausted: a heartbeat cannot refresh it. The reported reset
		// decides the wake: up to scheduleNearThreshold after the nearest
		// schedule point it is taken (hold, then anchor right at the reset);
		// before the point it is never taken early — and later than that it
		// is skipped. Either way the schedule point anchors instead, and the
		// quota (which auto-restores at the reset) sits un-anchored
		// meanwhile. Without a manual schedule the daemon always waits out
		// the reset; without a reset time it polls.
		if quota.Remaining <= 0 {
			untilReset := time.Until(quota.ResetTime)
			ref, schedPoint, near := resetNearSchedule(quota.ResetTime, sched)
			var wait time.Duration
			switch {
			case untilReset <= 0:
				if !exhausted {
					exhausted = true
					plog("Quota exhausted (no reset time reported) — polling every %s",
						exhaustedPollInterval)
				}
				wait = exhaustedPollInterval

			case resetTarget && untilReset <= imminentWindow:
				// Normal landing: this wake was planned for this reset and
				// only seconds are left — hold for it, don't re-decide.
				if !exhausted {
					exhausted = true
					plog("Quota exhausted — reset landing in %s, holding",
						untilReset.Round(time.Second))
				}
				wait = untilReset + time.Second

			case near:
				if !exhausted {
					exhausted = true
					plog("Quota exhausted — reset at %s is within %v after schedule %s — taking it",
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
					rel := fmt.Sprintf("more than %v after", scheduleNearThreshold)
					if quota.ResetTime.Before(ref) {
						rel = "before"
					}
					plog("Quota exhausted — reset at %s is %s schedule %s, sleeping to the schedule instead",
						quota.ResetTime.Local().Format("15:04:05"),
						rel,
						ref.Local().Format("15:04:05"))
				}
				resetTarget = false
				wait = time.Until(schedPoint)

			default:
				if !exhausted {
					exhausted = true
					plog("Quota exhausted — waiting for reset at %s (%s)",
						quota.ResetTime.Local().Format("2006-01-02 15:04:05"),
						glm.FormatTimeUntil(quota.ResetTime))
				}
				resetTarget = true
				wait = untilReset
			}

			switch sleepOr(sigCh, reloadCh, wait) {
			case sleepSignal:
				plog("Received signal, shutting down")
				return nil
			case sleepReload:
				plog("Received SIGHUP, reloading config")
			}
			continue
		}
		if exhausted {
			plog("Quota available again")
			exhausted = false
		}
		resetTarget = false

		// Weekly passive refresh (opt-in): if this wake landed at/past the
		// weekly reset we targeted, fire a warmup now — the weekly bucket
		// refills passively, the request just marks the fresh state and costs
		// ~0.02% of it. It also anchors the 5h window when that one is fresh,
		// which is why `fired` from the activate block above counts.
		if weeklyOn && weeklyTarget && !time.Now().Before(weeklyTargetAt) && !fired {
			if hc, ok := client.(interface{ SendHeartbeat() error }); ok {
				plog("Weekly reset reached — passive refresh")
				if err := hc.SendHeartbeat(); err != nil {
					perr("Weekly refresh failed: %v", err)
				} else {
					fired = true
					lastAnchor = time.Now()
				}
			}
		}
		weeklyTarget = false

		// Calculate next run
		next, atReset, err := nextWake(quota, sched)
		if err != nil {
			perr("Calculate next run: %v", err)
			return err
		}

		// Weekly passive refresh steals the wake when the weekly reset lands
		// before the regular 5h/schedule target: wake at the reset, fire the
		// warmup, then fall back to the regular decision next loop.
		weeklyTarget = false
		if weeklyOn {
			if wr, ok := weeklyResetOf(client); ok {
				next, weeklyTarget = maybeWeeklyWake(next, wr, time.Now())
				if weeklyTarget {
					weeklyTargetAt = next
					atReset = false
				}
			}
		}

		wait := time.Until(next)
		if atReset {
			// Wake a few seconds early so the quota query lands just before
			// the boundary and the grab decision runs on fresh data. Once
			// inside the lead window the full remainder to the boundary is
			// slept out — subtracting the lead again yields 0 and hot-loops
			// the quota API until the reset.
			if wait > resetQueryLead {
				wait -= resetQueryLead
			}
		} else if wait < minDaemonSleep {
			// Target may already have passed by now — never spin-loop on the API.
			wait = minDaemonSleep
		}
		if wait < 0 {
			wait = 0
		}

		plog("Next activation at %s (sleeping %s)",
			next.Local().Format("2006-01-02 15:04:05"), glm.FormatTimeUntil(next))

		switch sleepOr(sigCh, reloadCh, wait) {
		case sleepSignal:
			plog("Received signal, shutting down")
			return nil
		case sleepReload:
			plog("Received SIGHUP, reloading config")
		}
	}
}

// maybeWeeklyWake moves the wake target to the weekly reset when that lands
// before it and is still ahead; the bool reports the steal.
func maybeWeeklyWake(next, weeklyReset, now time.Time) (time.Time, bool) {
	if !weeklyReset.IsZero() && weeklyReset.After(now) && weeklyReset.Before(next) {
		return weeklyReset, true
	}
	return next, false
}

// weeklyResetOf fetches the pool's weekly-bucket reset via the optional agy
// interface (glm has no weekly concept and reports zero, false).
func weeklyResetOf(client providerClient) (time.Time, bool) {
	if wr, ok := client.(interface {
		WeeklyReset() (time.Time, bool)
	}); ok {
		return wr.WeeklyReset()
	}
	return time.Time{}, false
}

// weeklyRemainingOf fetches the pool's weekly-bucket remaining fraction
// (0-1) via the optional agy interface (glm reports zero, false).
func weeklyRemainingOf(client providerClient) (float64, bool) {
	if hc, ok := client.(interface {
		WeeklyRemaining() (float64, bool)
	}); ok {
		return hc.WeeklyRemaining()
	}
	return 0, false
}

// activationFailureWait decides the sleep after a failed activation. A warmup
// refusal that retrying cannot change — a geo-blocked egress (FAILED_PRECONDITION
// "location is not supported") is the canonical case — must not be re-shot
// every minute: that spams the API and floods the journal (a day of minute
// retries evicted a day of journal history on a small VPS). Policy refusals
// under a manual schedule sleep to the next schedule point (the egress or the
// policy may change by then); every other failure backs off exponentially
// from one minute, capped. Reset *fails to 0 on success.
func activationFailureWait(sched config.ScheduleConfig, fails *int, err error, logf func(string, ...interface{})) time.Duration {
	*fails++
	msg := err.Error()
	if strings.Contains(msg, "FAILED_PRECONDITION") ||
		strings.Contains(msg, "location is not supported") {
		if sched.Manual() {
			if next, err := nextScheduledTime(sched); err == nil {
				logf("Activation refusal looks permanent (%d in a row) — next attempt at the next schedule point %s",
					*fails, next.Local().Format("2006-01-02 15:04:05"))
				return time.Until(next)
			}
		}
		return activationBackoffCap
	}
	wait := time.Minute << (*fails - 1)
	if wait <= 0 || wait > activationBackoffCap {
		return activationBackoffCap
	}
	return wait
}

const (
	exhaustedPollInterval = 10 * time.Second
	minDaemonSleep        = 10 * time.Second
	// Weekly-only pools re-check on this cadence when no future reset is
	// reported (e.g. right after one passed without the bucket refilling
	// fully); the next query normally yields the next reset instead.
	weeklyOnlyRecheck = time.Hour
	// Failed activations back off exponentially from one minute up to this.
	activationBackoffCap = time.Hour
	// Manual-schedule rule: a reported reset up to this much AFTER the
	// nearest schedule point is taken (wake at the reset, anchor there). A
	// reset before the point is never taken early — the daemon waits for the
	// point and anchors there; a reset later than this is skipped and the
	// schedule point anchors instead.
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
// schedule is configured and the reset does not land up to
// scheduleNearThreshold after the nearest schedule point. A reset before
// the point would anchor early and drift the whole cadence, and one later
// than that sits off the scheduled grid; both are declined and the schedule
// point takes the anchor instead. Only a reset inside that late window keeps
// the reset wake. With no reset ahead —
// none reported, or one already past (a lapsed window the anchor decision
// declined to re-touch) — the manual schedule (or the auto 4h re-check)
// applies. A past reset is never a wake target: sleeping 0 to it just
// hot-loops the boundary.
func nextWake(quota *glm.QuotaStatus, sched config.ScheduleConfig) (time.Time, bool, error) {
	if !quota.ResetTime.IsZero() && quota.ResetTime.After(time.Now()) {
		if sched.Manual() {
			s, err := nextScheduledTime(sched)
			if err != nil {
				log.Errorf("Next scheduled time: %v — keeping reset wake", err)
			} else if c, err := closestScheduledTime(sched, quota.ResetTime); err != nil {
				log.Errorf("Closest scheduled time: %v — keeping reset wake", err)
			} else if quota.ResetTime.Before(c) || quota.ResetTime.Sub(c) > scheduleNearThreshold {
				log.Infof("Reset at %s is not within %v after schedule %s — following schedule instead",
					quota.ResetTime.Local().Format("15:04:05"),
					scheduleNearThreshold,
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
// now, where a skipped reset defers the wake to. The reset counts as near
// only when it falls after ref and within scheduleNearThreshold of it — a
// reset before its grid point is never taken early. Without a manual
// schedule both are zero and the daemon always wakes at the reset.
func resetNearSchedule(reset time.Time, sched config.ScheduleConfig) (ref, schedPoint time.Time, near bool) {
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
	late := reset.Sub(c)
	return c, s, late >= 0 && late <= scheduleNearThreshold
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
	activeCmd.Flags().String("pool", "", "agy pool to activate: gemini or 3p (default from config)")
}
