package cmd

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"ai-daemon/pkg/providers"
	"ai-daemon/pkg/providers/interfaces"
	"ai-daemon/pkg/utils"

	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:       "monitor [provider1 provider2 ...]",
	Short:     "Monitor usage and quotas across all providers",
	ValidArgs: []string{"glm", "antigravity", "geminicli", "gemini", "all"},
	Run: func(cmd *cobra.Command, args []string) {
		targets := make(map[string]bool)
		for _, arg := range args {
			targets[arg] = true
			// If gemini/geminicli/antigravity is targeted, ensure they are linked
			if arg == "gemini" || arg == "geminicli" || arg == "antigravity" {
				targets["gemini"] = true
				targets["geminicli"] = true
				targets["antigravity"] = true
			}
			// Handle specific account targets to ensure paired monitoring
			if strings.HasPrefix(arg, "gemini_") {
				targets[strings.Replace(arg, "gemini_", "geminicli_", 1)] = true
				targets[strings.Replace(arg, "gemini_", "antigravity_", 1)] = true
			}
			if strings.HasPrefix(arg, "geminicli_") {
				targets[strings.Replace(arg, "geminicli_", "antigravity_", 1)] = true
			}
			if strings.HasPrefix(arg, "antigravity_") {
				targets[strings.Replace(arg, "antigravity_", "geminicli_", 1)] = true
			}
		}

		fmt.Printf("\n\033[1;36mAI-Daemon Quota Dashboard (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := providers.LoadProvidersFromConfig()

		type result struct {
			p     interfaces.Provider
			quota *interfaces.QuotaStatus
			err   error
		}
		results := make([]result, len(registry))
		var wg sync.WaitGroup

		for i, p := range registry {
			id := p.ID()
			shouldRun := false
			if len(args) == 0 || targets["all"] {
				shouldRun = true
			} else if targets[id] {
				shouldRun = true
			} else {
				for t := range targets {
					if strings.HasPrefix(id, t+"_") || id == t {
						shouldRun = true
						break
					}
				}
			}

			if !shouldRun {
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

			fmt.Printf("\033[1;35m%s\033[0m\n", res.p.Name())
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
	if strings.HasPrefix(id, "glm") {
		color := "\033[32m"
		if q.Remaining < 20 {
			color = "\033[31m"
		}
		duration := ""
		if !q.ResetTime.IsZero() {
			duration = fmt.Sprintf(" (%s)", utils.FormatTimeUntil(q.ResetTime))
		}
		fmt.Printf("  %-25s: %s%3d%%\033[0m%s\n", "General", color, q.Remaining, duration)
		return
	}

	if strings.HasPrefix(id, "antigravity") {
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
			duration := ""
			if !info.ResetTime.IsZero() {
				duration = fmt.Sprintf(" (%s)", utils.FormatTimeUntil(info.ResetTime))
			}
			fmt.Printf("  %-25s: %s%3.0f%%\033[0m%s\n", g.Label, color, info.Remaining, duration)
		}
		return
	}

	if strings.HasPrefix(id, "geminicli") {
		cliMap := utils.ExtractAllCliQuotas(q.Raw)
		for name, info := range cliMap {
			color := "\033[32m"
			if info.Remaining < 20 {
				color = "\033[31m"
			}
			duration := ""
			if !info.ResetTime.IsZero() {
				duration = fmt.Sprintf(" (%s)", utils.FormatTimeUntil(info.ResetTime))
			}
			fmt.Printf("  %-25s: %s%3.0f%%\033[0m%s\n", name, color, info.Remaining, duration)
		}
		return
	}
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().Bool("debug", false, "Enable debug output")
}
