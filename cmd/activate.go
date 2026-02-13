package cmd

import (
	"fmt"
	"time"

	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/spf13/cobra"
)

var activateCmd = &cobra.Command{
	Use:   "activate [all|antigravity|cli]",
	Short: "Initialize quota timers by sending warmup requests",
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		force, _ := cmd.Flags().GetBool("force")
		target := "all"
		if len(args) > 0 {
			target = args[0]
		}

		fmt.Printf("\n\033[1;36m[*] Initializing AI Quota Timers (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := []interfaces.Provider{
			antigravity.NewProvider(),
			geminicli.NewProvider(),
		}

		for _, p := range registry {
			if target != "all" && p.ID() != target {
				if target == "cli" && p.ID() == "geminicli" {
					// match
				} else {
					continue
				}
			}

			fmt.Printf("\033[1m[ %s ]\033[0m\n", p.Name())
			p.SetDebug(debug)
			if err := p.Authenticate(); err != nil {
				fmt.Printf("  \033[31m[-] Auth: %v\033[0m\n", err)
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
	activateCmd.Flags().Bool("force", false, "Force activation")
}
