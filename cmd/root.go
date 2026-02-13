package cmd

import (
	"fmt"
	"os"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "ai-daemon",
	Short: "A system-level daemon for AI service quota management",
	Long: `ai-daemon integrates GLM, Gemini CLI, and Antigravity into a unified 
interface for quota monitoring and automated heartbeat management.`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(config.InitConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/ai-daemon/config.yaml)")
}
