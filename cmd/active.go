package cmd

import (
	"fmt"
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
In auto-schedule mode, reschedules the next run based on quota reset time.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.Current.APIKey == "" {
			return fmt.Errorf("no API key configured. Run 'glm login' first")
		}

		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		serviceMode, _ := cmd.Flags().GetBool("service")

		client := glm.NewClient()
		client.SetDebug(debug)

		s := ui.NewSpinner("Activating...")
		s.Start()

		if serviceMode {
			log.Infof("Service mode activation")
		}

		quota, err := client.Activate(force, serviceMode)
		s.Stop()
		fmt.Println()

		if err != nil {
			ui.Error(fmt.Sprintf("Activation failed: %v", err))
			return err
		}

		printQuotaResult(quota)

		// Auto-reschedule if in auto mode + service mode
		if serviceMode && config.Current.Schedule.Auto && !quota.ResetTime.IsZero() {
			if err := rescheduleAuto(quota.ResetTime); err != nil {
				log.Errorf("Auto-reschedule failed: %v", err)
			}
		}

		return nil
	},
}

func rescheduleAuto(resetTime time.Time) error {
	// Schedule next activation shortly after reset
	nextRun := resetTime.Add(30 * time.Second)

	// If next run is in the past, skip
	if time.Until(nextRun) <= 0 {
		log.Debugf("Next run %s is in the past, skipping reschedule", nextRun.Format("2006-01-02 15:04:05"))
		return nil
	}

	log.Infof("Rescheduling next activation to %s", nextRun.Format("2006-01-02 15:04:05"))

	if err := RescheduleTimer(nextRun); err != nil {
		return fmt.Errorf("reschedule timer: %w", err)
	}

	ui.Info(fmt.Sprintf("Next activation: %s (%s)",
		ui.Style(nextRun.Local().Format("15:04:05"), ui.Cyan, ui.Bold),
		ui.Dimmed(glm.FormatTimeUntil(nextRun))))
	return nil
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
	activeCmd.Flags().Bool("service", false, "Service mode: auto-sleep on imminent reset")
	activeCmd.Flags().Bool("debug", false, "Show raw API responses")
}
