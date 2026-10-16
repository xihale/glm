package cmd

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xihale/glm/pkg/codex"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/log"
)

const wallChunk = 30 * time.Second

// runCodexDaemon anchors the ChatGPT 5h window.
//
// Startup reads /wham/usage and remembers the reset. It does not ping while
// the window is open. At the reset — and again after the machine resumes
// from sleep, or when the service starts after a shutdown — it reads usage
// once more. A window that is not open gets one gpt-6-luna "hello"; a window
// that is still open (real use moved the reset) is waited out until the new
// time.
func runCodexDaemon(debug, force bool) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)

	log.Infof("Codex daemon started (ping %s on a closed 5h window)", codex.DefaultModel)
	fails := 0
	forceOnce := force
	for {
		if err := config.Reload(); err != nil {
			log.Errorf("Config reload failed, keeping current: %v", err)
		}
		cfg := config.Snapshot()
		client := codex.NewClientFrom(cfg)
		client.SetDebug(debug)

		usage, err := client.Usage()
		if err != nil {
			log.Errorf("Get quota failed: %v", err)
			switch sleepWall(sigCh, reloadCh, time.Now().Add(time.Minute)) {
			case sleepSignal:
				log.Infof("Received signal, shutting down")
				return nil
			case sleepReload:
				log.Infof("Received SIGHUP, reloading config")
			}
			continue
		}

		now := time.Now()
		plan := codex.PlanCheck(usage.Five, now)
		var next time.Time
		if forceOnce || plan.Action == codex.ActionPing {
			why := plan.Why
			if forceOnce {
				why = "forced"
			}
			log.Infof("5h window %s — sending %s %q", why, codexModel(cfg), codex.PingText)
			if err := client.Ping(); err != nil {
				log.Errorf("Ping failed: %v", err)
				fails++
				wait := pingBackoff(fails)
				log.Infof("Retrying in %s", wait)
				if sleepWall(sigCh, reloadCh, time.Now().Add(wait)) == sleepSignal {
					log.Infof("Received signal, shutting down")
					return nil
				}
				continue
			}
			forceOnce = false
			fails = 0
			if again, err := client.Usage(); err != nil {
				log.Errorf("Get quota after ping failed: %v", err)
			} else {
				usage = again
			}
			next = codex.NextAfterPing(usage.Five, time.Now())
		} else {
			next = plan.Next
			log.Infof("5h window open until %s (%s) — next check then",
				usage.Five.Reset.Local().Format("2006-01-02 15:04:05"),
				glm.FormatTimeUntil(usage.Five.Reset))
		}
		if !next.After(time.Now()) {
			next = time.Now().Add(codex.IdleRecheck)
		}
		log.Infof("Next check at %s (sleeping %s)",
			next.Local().Format("2006-01-02 15:04:05"), glm.FormatTimeUntil(next))
		switch sleepWall(sigCh, reloadCh, next) {
		case sleepSignal:
			log.Infof("Received signal, shutting down")
			return nil
		case sleepReload:
			log.Infof("Received SIGHUP, reloading config")
		case sleepResume:
			log.Infof("Resumed from sleep — checking the 5h window again")
		}
	}
}

func codexModel(cfg config.Config) string {
	if m := cfg.Codex.Model; m != "" {
		return m
	}
	return codex.DefaultModel
}

func pingBackoff(fails int) time.Duration {
	if fails < 1 {
		fails = 1
	}
	d := time.Minute
	for i := 1; i < fails && d < 15*time.Minute; i++ {
		d *= 2
	}
	if d > 15*time.Minute {
		return 15 * time.Minute
	}
	return d
}

// sleepWall waits until the wall-clock deadline in short chunks. A suspend
// freezes CLOCK_MONOTONIC, so the in-flight chunk finishes only after the
// machine is awake again; the wall/mono gap is then a resume, and the caller
// re-reads usage instead of trusting the old deadline.
func sleepWall(sigCh, reloadCh <-chan os.Signal, until time.Time) sleepOutcome {
	for {
		now := time.Now()
		if !now.Before(until) {
			return sleepDone
		}
		chunk := time.Until(until)
		if chunk > wallChunk {
			chunk = wallChunk
		}
		wallStart := now.Round(0)
		timer := time.NewTimer(chunk)
		select {
		case <-sigCh:
			timer.Stop()
			return sleepSignal
		case <-reloadCh:
			timer.Stop()
			return sleepReload
		case <-timer.C:
		}
		wallElapsed := time.Now().Round(0).Sub(wallStart)
		if codex.SleptThrough(wallElapsed, chunk) {
			return sleepResume
		}
	}
}
