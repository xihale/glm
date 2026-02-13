package cmd

import (
	"fmt"
	"strings"
	"time"

	"ai-daemon/pkg/providers"

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
			if arg == "gemini" {
				targets["geminicli"] = true
			}
		}

		fmt.Printf("\n\033[1;36mInitializing AI Quota Timers (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := providers.LoadProvidersFromConfig()

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

			fmt.Printf("\033[1m%s\033[0m\n", p.Name())
			p.SetDebug(debug)
			// Pass group filter to providers that support it
			if ap, ok := p.(interface{ SetGroup(string) }); ok {
				ap.SetGroup(group)
			}
			if err := p.Authenticate(); err != nil {

				fmt.Printf("  \033[33m[!] Auth skipped: %v\033[0m\n", err)
				fmt.Println()
				continue
			}

			if err := p.Activate(debug, force); err != nil {
				fmt.Printf("  \033[31m[-] Error: %v\033[0m\n", err)
			}
			fmt.Println()
		}
	},
}

func init() {
	rootCmd.AddCommand(activateCmd)
	activateCmd.Flags().Bool("debug", false, "Enable debug output")
	activateCmd.Flags().BoolP("force", "f", false, "Force activation")
	activateCmd.Flags().StringP("group", "g", "", "Select specific model group to activate")
}
