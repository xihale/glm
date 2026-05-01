package cmd

import (
	"fmt"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"

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
			return fmt.Errorf("failed to get quota: %w", err)
		}

		printQuotaStatus(quota)
		return nil
	},
}

func printQuotaStatus(q *glm.QuotaStatus) {
	// Reset time
	reset := "N/A"
	if !q.ResetTime.IsZero() {
		until := glm.FormatTimeUntil(q.ResetTime)
		at := q.ResetTime.Local().Format("15:04:05")
		if until == "Passed" {
			reset = fmt.Sprintf("passed, reset at %s", at)
		} else {
			reset = fmt.Sprintf("%s, reset at %s", until, at)
		}
	}

	fmt.Printf("  %d%% (%s)\n", q.Remaining, reset)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
