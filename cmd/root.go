package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/log"
)

var rootCmd = &cobra.Command{
	Use:   "glm",
	Short: "Quota activation tool for the GLM coding plan and Google Antigravity",
	Long: `glm manages quota activation with heartbeat and systemd scheduling.

Each provider keeps its own config and systemd service, so they can run
side by side with independent schedules:
  glm   -> ~/.config/glm/config.yaml   + glm.service
  agy   -> ~/.config/agy/config.yaml   + glm-agy.service

Select the provider with the global --provider flag (default: glm).`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m✘ %v\033[0m\n", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		if p := config.Provider; p != "" &&
			!strings.EqualFold(p, "glm") && !strings.EqualFold(p, "agy") {
			fmt.Fprintf(os.Stderr, "\033[31m✘ unknown provider %q (use glm or agy)\033[0m\n", p)
			os.Exit(1)
		}
		config.InitConfig()
		debug, _ := rootCmd.PersistentFlags().GetBool("debug")
		log.DebugMode = debug
	})

	rootCmd.PersistentFlags().StringVar(&config.CfgFile, "config", "", "config file (default per provider: ~/.config/glm or ~/.config/agy)")
	rootCmd.PersistentFlags().StringVar(&config.Provider, "provider", "", "provider scope: glm (default) or agy — picks config dir and service name")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
}
