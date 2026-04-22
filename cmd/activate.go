package cmd

import (
	"fmt"
	"time"

	"glm/pkg/providers"
	pkgutils "glm/pkg/utils"

	"github.com/spf13/cobra"
)

var activateCmd = &cobra.Command{
	Use:   "activate",
	Short: "Send heartbeat to activate GLM quota",
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")

		registry := providers.LoadProvidersFromConfig()
		if len(registry) == 0 {
			fmt.Println("No GLM providers configured. Run 'glm auth set' first.")
			return
		}

		var earliestReset time.Time

		for _, p := range registry {
			p.SetDebug(debug)
			if err := p.Authenticate(); err != nil {
				fmt.Printf("%s \033[33m[!] Auth skipped: %v\033[0m\n", p.Name(), err)
				continue
			}

			q, err := p.Activate(nil, debug, force)
			if err != nil {
				fmt.Printf("%s \033[31m[-] %v\033[0m\n", p.Name(), err)
				continue
			}

			// Re-fetch quota after activation to get the new reset time.
			if q, err = p.GetQuota(); err != nil {
				continue
			}
			if q != nil && !q.ResetTime.IsZero() && q.ResetTime.After(time.Now()) {
				if earliestReset.IsZero() || q.ResetTime.Before(earliestReset) {
					earliestReset = q.ResetTime
				}
			}
		}

		if !earliestReset.IsZero() {
			buf := earliestReset.Add(pkgutils.ResetBuffer)
			fmt.Printf("Next reset: \033[1;32m%s\033[0m (%s)\n",
				buf.Format("15:04:05"),
				pkgutils.FormatTimeUntil(buf))
		}
	},
}

func init() {
	rootCmd.AddCommand(activateCmd)
	activateCmd.Flags().Bool("debug", false, "Show raw API response")
	activateCmd.Flags().BoolP("force", "f", false, "Force activation even if quota is active")
}
