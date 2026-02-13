package cmd

import (
	"fmt"
	"sync"
	"time"

	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"
	"ai-daemon/pkg/utils"

	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:       "monitor [provider1 provider2 ...]",
	Short:     "Monitor usage and quotas across all providers",
	ValidArgs: []string{"glm", "antigravity", "geminicli", "all"},
	Run: func(cmd *cobra.Command, args []string) {
		targets := make(map[string]bool)
		for _, arg := range args {
			targets[arg] = true
		}

		fmt.Printf("\n\033[1;36m[*] AI-Daemon Quota Dashboard (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := []interfaces.Provider{
			glm.NewProvider(),
			antigravity.NewProvider(),
			geminicli.NewProvider(),
		}

		type result struct {
			p     interfaces.Provider
			quota *interfaces.QuotaStatus
			err   error
		}
		results := make([]result, len(registry))
		var wg sync.WaitGroup

		for i, p := range registry {
			if len(args) > 0 && !targets["all"] && !targets[p.ID()] {
				continue
			}

			wg.Add(1)
			go func(idx int, prov interfaces.Provider) {
				defer wg.Done()
				if err := prov.Authenticate(); err != nil {
					results[idx] = result{p: prov, err: err}
					return
				}
				q, err := prov.GetQuota()
				results[idx] = result{p: prov, quota: q, err: err}
			}(i, p)
		}

		wg.Wait()

		debug, _ := cmd.Flags().GetBool("debug")

		for _, res := range results {
			if res.p == nil {
				continue
			}
			fmt.Printf("\033[1;35m[ %s ]\033[0m\n", res.p.Name())
			if res.err != nil {
				fmt.Printf("  \033[33m[!] %v\033[0m\n", res.err)
				fmt.Println()
				continue
			}
			if debug && res.quota != nil {
				fmt.Printf("\033[34m[DEBUG] Raw Quota Output:\033[0m\n%s\n\n", res.quota.Raw)
			}
			displayQuota(res.p.ID(), res.quota)
			fmt.Println()
		}
	},
}

func displayQuota(id string, q *interfaces.QuotaStatus) {
	switch id {
	case "glm":
		color := "\033[32m"
		if q.Remaining < 20 {
			color = "\033[31m"
		}
		fmt.Printf("  \033[32m[+]\033[0m Quota: %s%d%%\033[0m Remaining | Used: %d%%\n", color, q.Remaining, q.Used)
		if !q.ResetTime.IsZero() {
			fmt.Printf("  \033[34m[*]\033[0m Next Reset: %s (%s left)\n",
				q.ResetTime.Local().Format("15:04:05"), utils.FormatTimeUntil(q.ResetTime))
		}
	case "antigravity":
		modelMap := utils.ExtractAllModelQuotas(q.Raw)
		groups := []struct {
			IDs   []string
			Label string
		}{
			{[]string{"gemini-3-flash"}, "Gemini 3 Flash"},
			{[]string{"gemini-3-pro-low"}, "Gemini 3 Pro"},
			{[]string{"claude-sonnet-4-5-thinking", "claude-opus-4-5-thinking", "gpt-oss-120b-medium"}, "Claude / GPT-OSS"},
		}
		for _, g := range groups {
			var info utils.ModelQuota
			var found bool
			for _, id := range g.IDs {
				if m, ok := modelMap[id]; ok {
					info = m
					found = true
					break
				}
			}
			if !found {
				continue
			}

			color := "\033[32m"
			if info.Remaining < 20 {
				color = "\033[31m"
			}
			fmt.Printf("  \033[32m[+]\033[0m %-18s: %s%3.0f%%\033[0m", g.Label, color, info.Remaining)
			if !info.ResetTime.IsZero() {
				fmt.Printf("  (%s / %s left)", info.ResetTime.Local().Format("15:04"), utils.FormatTimeUntil(info.ResetTime))
			}
			fmt.Println()
		}

	case "geminicli":
		cliMap := utils.ExtractAllCliQuotas(q.Raw)
		// To maintain order or just iterate
		for name, info := range cliMap {
			color := "\033[32m"
			if info.Remaining < 20 {
				color = "\033[31m"
			}
			fmt.Printf("  \033[32m[+]\033[0m %-25s: %s%3.0f%%\033[0m", name, color, info.Remaining)
			if !info.ResetTime.IsZero() {
				fmt.Printf("  (%s / %s left)", info.ResetTime.Local().Format("15:04"), utils.FormatTimeUntil(info.ResetTime))
			}
			fmt.Println()
		}
	}
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().Bool("debug", false, "Enable debug output")
}
