package cmd

import (
	"fmt"
	"strconv"

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

		s := ui.NewSpinner("Fetching quota...")
		s.Start()

		client := glm.NewClient()
		quota, err := client.GetQuota()

		s.Stop()
		fmt.Println()

		if err != nil {
			ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
			return err
		}

		printQuotaStatus(quota)
		return nil
	},
}

func printQuotaStatus(q *glm.QuotaStatus) {
	// Status
	var status string
	if q.Remaining > 50 {
		status = ui.Style("Healthy", ui.Green)
	} else if q.Remaining > 10 {
		status = ui.Style("Low", ui.Yellow)
	} else {
		status = ui.Style("Critical", ui.Red)
	}

	// Remaining
	color := ui.Green
	if q.Remaining < 20 {
		color = ui.Red
	} else if q.Remaining < 50 {
		color = ui.Yellow
	}
	remaining := ui.Style(strconv.FormatInt(q.Remaining, 10)+"%", color, ui.Bold)

	// Reset time
	reset := ui.Dimmed("N/A")
	if !q.ResetTime.IsZero() {
		until := glm.FormatTimeUntil(q.ResetTime)
		at := q.ResetTime.Local().Format("15:04:05")
		if until == "Passed" {
			reset = ui.Dimmed("Passed (" + at + ")")
		} else {
			reset = fmt.Sprintf("%s (%s)", until, ui.Dimmed(at))
		}
	}

	fmt.Printf("  Status:    %s\n", status)
	fmt.Printf("  Remaining: %s\n", remaining)
	fmt.Printf("  Reset:     %s\n", reset)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
