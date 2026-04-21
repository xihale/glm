package cmd

import (
	"fmt"

	"glm/pkg/providers"
	"glm/pkg/providers/interfaces"
	"glm/pkg/utils"

	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Show GLM quota status",
	Run: func(cmd *cobra.Command, args []string) {
		registry := providers.LoadProvidersFromConfig()
		if len(registry) == 0 {
			fmt.Println("No GLM providers configured. Run 'glm auth set' first.")
			return
		}

		debug, _ := cmd.Flags().GetBool("debug")

		for _, p := range registry {
			if err := p.Authenticate(); err != nil {
				fmt.Printf("%s \033[33m[!] %v\033[0m\n", p.Name(), err)
				continue
			}
			q, err := p.GetQuota()
			if err != nil {
				fmt.Printf("%s \033[33m[!] %v\033[0m\n", p.Name(), err)
				continue
			}
			if debug && q != nil {
				fmt.Printf("\033[34m[DEBUG] Raw:\033[0m\n%s\n", q.Raw)
			}
			displayQuota(p.Name(), q)
		}
	},
}

func displayQuota(name string, q *interfaces.QuotaStatus) {
	if q == nil {
		return
	}
	color := "\033[32m"
	if q.Remaining < 20 {
		color = "\033[31m"
	}
	suffix := ""
	if !q.ResetTime.IsZero() {
		until := utils.FormatTimeUntil(q.ResetTime)
		at := q.ResetTime.Local().Format("15:04:05")
		if until == "Passed" {
			suffix = fmt.Sprintf(" (reset passed at %s)", at)
		} else {
			suffix = fmt.Sprintf(" (%s, reset at %s)", until, at)
		}
	}
	fmt.Printf("%s %s%3d%%\033[0m%s\n", name, color, q.Remaining, suffix)
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().Bool("debug", false, "Show raw API response")
}
