package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/log"
)

var rootCmd = &cobra.Command{
	Use:   "glm",
	Short: "Quota activation tool for GLM, Antigravity, Command Code, and Codex",
	Long: `glm manages quota activation with heartbeat and systemd scheduling.

Each provider keeps its own config section and systemd service, so they can
run side by side with independent schedules:
  glm         -> ~/.config/glm/config.yaml [schedule] + glm.service
  agy         -> ~/.config/glm/config.yaml [agy] + glm-agy.service
  commandcode -> ~/.config/glm/config.yaml [commandcode] + glm-commandcode.service
  codex       -> ~/.config/glm/config.yaml [codex] + glm-codex.service

Select the provider with the global --provider flag (default: glm).`,
}

func completeProvider(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{"glm", "agy", "commandcode", "codex"}, cobra.ShellCompDirectiveNoFileComp
}

func completePool(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{"gemini", "3p"}, cobra.ShellCompDirectiveNoFileComp
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m✘ %v\033[0m\n", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		if p := config.Provider; p != "" {
			switch config.EffectiveProvider() {
			case "glm", "agy", "commandcode", "codex":
			default:
				fmt.Fprintf(os.Stderr, "\033[31m✘ unknown provider %q (use glm, agy, commandcode, or codex)\033[0m\n", p)
				os.Exit(1)
			}
		}
		config.InitConfig()
		debug, _ := rootCmd.PersistentFlags().GetBool("debug")
		log.DebugMode = debug
	})

	rootCmd.PersistentFlags().StringVar(&config.CfgFile, "config", "", "config file (default: ~/.config/glm/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&config.Provider, "provider", "", "provider scope: glm (default), agy, commandcode (cc), or codex — picks config section and service name")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")

	rootCmd.RegisterFlagCompletionFunc("provider", completeProvider)
	_ = rootCmd.MarkPersistentFlagFilename("config", "yaml", "yml")
}
