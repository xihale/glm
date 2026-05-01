package cmd

import (
	"fmt"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/ui"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GLM quota status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.Current.APIKey == "" {
			return fmt.Errorf("no API key configured. Run 'glm login' first")
		}

		client := glm.NewClient()
		quota, err := client.GetQuota()

		if err != nil {
			ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
			return err
		}

		printQuotaStatus(quota)
		return nil
	},
}

func printQuotaStatus(q *glm.QuotaStatus) {
	// Remaining color
	color := ui.Green
	if q.Remaining < 20 {
		color = ui.Red
	} else if q.Remaining < 50 {
		color = ui.Yellow
	}
	pct := ui.Style(fmt.Sprintf("%d%%", q.Remaining), color, ui.Bold)

	// Reset time
	reset := ui.Dimmed("N/A")
	if !q.ResetTime.IsZero() {
		until := glm.FormatTimeUntil(q.ResetTime)
		at := q.ResetTime.Local().Format("15:04:05")
		if until == "Passed" {
			reset = fmt.Sprintf("%s, reset at %s", ui.Dimmed("passed"), at)
		} else {
			reset = fmt.Sprintf("%s, reset at %s", until, at)
		}
	}

	fmt.Printf("  %s (%s)\n", pct, reset)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
