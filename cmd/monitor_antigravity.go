package cmd

import (
	"fmt"
	"os"
	"time"

	"ai-daemon/pkg/providers/antigravity"

	"github.com/spf13/cobra"
)

var monitorAntigravityCmd = &cobra.Command{
	Use:   "antigravity-monitor",
	Short: "Monitor Antigravity IDE usage",
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")

		provider := antigravity.NewProvider()
		provider.SetDebug(debug)

		if err := provider.Authenticate(); err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Antigravity Provider Authenticated (Local Process Found).")

		quota, err := provider.GetQuota()
		if err != nil {
			fmt.Printf("Error fetching quota: %v\n", err)
		} else {
			fmt.Printf("Quota Status: Type=%s, Remaining=%d%%, Used=%d%%\n", quota.Type, quota.Remaining, quota.Used)
			if !quota.ResetTime.IsZero() {
				fmt.Printf("Next Reset: %v\n", quota.ResetTime.Local().Format(time.RFC3339))
			}
			if debug {
				fmt.Printf("DEBUG Raw Response: %s\n", quota.Raw)
			}
		}

		sendHeartbeat, _ := cmd.Flags().GetBool("heartbeat")
		if sendHeartbeat {
			fmt.Println("DEPRECATED: Use 'refresh' command.")
			provider.SendHeartbeat()
		}
	},
}

func init() {
	monitorCmd.AddCommand(monitorAntigravityCmd)
	monitorAntigravityCmd.Flags().Bool("heartbeat", false, "Send a heartbeat ping")
	monitorAntigravityCmd.Flags().Bool("debug", false, "Enable debug output")
}
