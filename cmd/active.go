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

		client := glm.NewClient()
		client.SetDebug(debug)

		if serviceMode {
			return runDaemon(client, force)
		}

		// One-shot mode
		s := ui.NewSpinner("Activating...")
		s.Start()
		quota, err := client.Activate(force, false)
		s.Stop()
		fmt.Println()
		if err != nil {
			ui.Error(fmt.Sprintf("Activation failed: %v", err))
			return err
		}
		printQuotaResult(quota)
		return nil
	},
}

func runDaemon(client *glm.Client, force bool) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Infof("Daemon started (auto=%v manual=%v)",
		config.Current.Schedule.Auto, !config.Current.Schedule.IsEmpty())

	for {
		// Activate
		log.Infof("Activating...")
		quota, err := client.Activate(force, true)
		if err != nil {
			log.Errorf("Activation failed: %v", err)
			// Back off and retry
			select {
			case <-sigCh:
				log.Infof("Received signal, shutting down")
				return nil
			case <-time.After(1 * time.Minute):
				continue
			}
		}

		log.Infof("Activated — %d%% remaining", quota.Remaining)
	// Fetch fresh status to get updated reset time (may shift due to network)
	fresh, ferr := client.GetQuota()
	if ferr == nil && fresh != nil {
		quota = fresh
	}

	log.Infof("Activated — %d%% remaining", quota.Remaining)
	if !quota.ResetTime.IsZero() {
		log.Infof("Reset at: %s (%s)",
			quota.ResetTime.Local().Format("15:04:05"), glm.FormatTimeUntil(quota.ResetTime))
	}
		// Calculate next run
		nextRun, err := nextActivationTime(quota)
		if err != nil {
			log.Errorf("Calculate next run: %v", err)
			return err
		}

		wait := time.Until(nextRun)
		log.Infof("Next activation at %s (sleeping %s)",
			nextRun.Local().Format("2006-01-02 15:04:05"), glm.FormatTimeUntil(nextRun))

		// Sleep until next run or signal
		select {
		case <-sigCh:
			log.Infof("Received signal, shutting down")
			return nil
		case <-time.After(wait):
		}
	}
}

const imminentThreshold = 20 * time.Minute

func nextActivationTime(quota *glm.QuotaStatus) (time.Time, error) {
	sched := config.Current.Schedule

	// Determine the "normal" next run time
	var normal time.Time
	if sched.Auto {
		if quota.ResetTime.IsZero() {
			return time.Now().Add(4 * time.Hour), nil
		}
		normal = quota.ResetTime
	} else {
		var err error
		normal, err = nextScheduledTime(sched)
		if err != nil {
			return time.Time{}, err
		}
	}

	// Smart override: if reset is imminent from now or from next run, activate at reset time
	if !quota.ResetTime.IsZero() {
		untilReset := time.Until(quota.ResetTime)
		untilNext := time.Until(normal)
		if untilReset > 0 && (untilReset < imminentThreshold || untilNext < imminentThreshold) {
			return quota.ResetTime, nil
		}
	}

	return normal, nil
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
