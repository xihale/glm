package cmd

import (
	"fmt"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/providers"
	pkgutils "github.com/xihale/glm/pkg/utils"

	"github.com/spf13/cobra"
)

var activeCmd = &cobra.Command{
	Use:   "active",
	Short: "Send heartbeat to activate GLM quota",
	Long: `Send heartbeat requests to activate quota for all configured providers.
If a schedule is configured, manual activation is blocked (use schedule instead).`,
	Run: func(cmd *cobra.Command, args []string) {
		if !config.Current.Schedule.IsEmpty() {
			fmt.Println("\033[31m[-] Schedule is configured. Manual activation is disabled.\033[0m")
			fmt.Println("    Use 'glm schedule show' to view schedule, or 'glm schedule clear' to remove it.")
			return
		}

		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")

		runActivation(debug, force)
	},
}

func runActivation(debug, force bool) {
	registry := providers.LoadProvidersFromConfig()
	if len(registry) == 0 {
		fmt.Println("No GLM providers configured. Run 'glm login' first.")
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
}

func collectEarliestReset(debug bool) time.Time {
	registry := providers.LoadProvidersFromConfig()
	if len(registry) == 0 {
		return time.Time{}
	}

	var earliestReset time.Time
	for _, p := range registry {
		p.SetDebug(debug)
		if err := p.Authenticate(); err != nil {
			continue
		}

		q, err := p.GetQuota()
		if err != nil || q == nil || q.ResetTime.IsZero() || !q.ResetTime.After(time.Now()) {
			continue
		}

		if earliestReset.IsZero() || q.ResetTime.Before(earliestReset) {
			earliestReset = q.ResetTime
		}
	}

	return earliestReset
}

func init() {
	rootCmd.AddCommand(activeCmd)
	activeCmd.Flags().Bool("debug", false, "Show raw API response")
	activeCmd.Flags().BoolP("force", "f", false, "Force activation even if quota is active")
}
