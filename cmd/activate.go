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

var activateCmd = &cobra.Command{
	Use:       "activate [provider1 provider2 ...]",
	Short:     "Initialize quota timers by sending warmup requests",
	ValidArgs: []string{"glm", "antigravity", "geminicli", "gemini", "all"},
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		group, _ := cmd.Flags().GetString("group")

		targets := make(map[string]bool)
		for _, arg := range args {
			targets[arg] = true
			// If gemini/geminicli/antigravity is targeted, ensure they are linked
			if arg == "gemini" || arg == "geminicli" || arg == "antigravity" {
				targets["gemini"] = true
				targets["geminicli"] = true
				targets["antigravity"] = true
			}
			// Handle specific account targets to ensure paired activation
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

		fmt.Printf("\n\033[1;36mInitializing AI Quota Timers (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := providers.LoadProvidersFromConfig()

		var wg sync.WaitGroup
		var mu sync.Mutex
		var earliestReset time.Time

		for _, p := range registry {
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
			go func(prov interfaces.Provider) {
				defer wg.Done()

				// Capture output to buffer to prevent console interleaving
				var output strings.Builder

				output.WriteString(fmt.Sprintf("\033[1m%s\033[0m\n", prov.Name()))
				prov.SetDebug(debug)
				// Pass group filter to providers that support it
				if ap, ok := prov.(interface{ SetGroup(string) }); ok {
					ap.SetGroup(group)
				}
				if err := prov.Authenticate(); err != nil {
					output.WriteString(fmt.Sprintf("  \033[33m[!] Auth skipped: %v\033[0m\n\n", err))
					mu.Lock()
					fmt.Print(output.String())
					mu.Unlock()
					return
				}

				if err := prov.Activate(&output, debug, force); err != nil {
					output.WriteString(fmt.Sprintf("  \033[31m[-] Error: %v\033[0m\n", err))
				}
				output.WriteString("\n")

				mu.Lock()
				fmt.Print(output.String())

				// Calculate next reset time
				q, err := prov.GetQuota()
				if err == nil {
					var next time.Time
					if strings.HasPrefix(prov.ID(), "antigravity") {
						modelMap := utils.ExtractAllModelQuotas(q.Raw)
						next = utils.GetEarliestFutureResetTime(modelMap)
					} else if strings.HasPrefix(prov.ID(), "geminicli") {
						cliMap := utils.ExtractAllCliQuotas(q.Raw)
						next = utils.GetEarliestFutureResetTime(cliMap)
					} else {
						// For simple providers like GLM
						if q.Remaining < 100 && !q.ResetTime.IsZero() && q.ResetTime.After(time.Now()) {
							next = q.ResetTime
						}
					}

					if !next.IsZero() {
						if earliestReset.IsZero() || next.Before(earliestReset) {
							earliestReset = next
						}
					}
				}
				mu.Unlock()
			}(p)
		}

		wg.Wait()

		if !earliestReset.IsZero() {
			fmt.Printf("\033[36m────────────────────────────────────────────────────────────\033[0m\n")
			fmt.Printf("Next scheduled availability: \033[1;32m%s\033[0m (%s)\n\n",
				earliestReset.Format("15:04:05"),
				utils.FormatTimeUntil(earliestReset))
		}
	},
}

func init() {
	rootCmd.AddCommand(activateCmd)
	activateCmd.Flags().Bool("debug", false, "Enable debug output")
	activateCmd.Flags().BoolP("force", "f", false, "Force activation")
	activateCmd.Flags().StringP("group", "g", "", "Select specific model group to activate")
}
