package cmd

import (
	"fmt"
	"strings"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/gemini"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/spf13/cobra"
)

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Send heartbeat to providers to keep sessions alive",
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		target, _ := cmd.Flags().GetString("target")

		providers := []interfaces.Provider{}

		if target == "all" || target == "glm" {
			if config.Current.GLM.APIKey != "" || target == "glm" {
				providers = append(providers, glm.NewProvider())
			}
		}
		if target == "all" || target == "gemini" {
			if config.Current.Gemini.Secure1PSID != "" || config.Current.Gemini.AccessToken != "" || config.Current.Gemini.RefreshToken != "" || target == "gemini" {
				providers = append(providers, gemini.NewProvider())
			}
		}
		if target == "all" || target == "antigravity" {
			providers = append(providers, antigravity.NewProvider())
		}

		for _, p := range providers {
			p.SetDebug(debug)

			if err := p.Authenticate(); err != nil {
				if strings.Contains(err.Error(), "process not found") && target == "all" {
					if debug {
						fmt.Printf("Processing %s: Skipped (Not running)\n", p.Name())
					}
					continue
				}

				fmt.Printf("Processing %s...\n", p.Name())
				fmt.Printf("  Authentication failed: %v\n", err)
				continue
			}

			fmt.Printf("Processing %s...\n", p.Name())
			if err := p.SendHeartbeat(); err != nil {
				fmt.Printf("  Heartbeat failed: %v\n", err)
			} else {
				fmt.Printf("  Heartbeat sent successfully.\n")
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(refreshCmd)
	refreshCmd.Flags().Bool("debug", false, "Enable debug output")
	refreshCmd.Flags().String("target", "all", "Target provider (all, glm, gemini)")
}
