package cmd

import (
	"fmt"
	"os"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "glm",
	Short: "GLM quota activation tool",
	Long:  `glm manages GLM quota activation with heartbeat and systemd scheduling.`,
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
		config.InitConfig()
		debug, _ := rootCmd.PersistentFlags().GetBool("debug")
		log.DebugMode = debug
	})

	rootCmd.PersistentFlags().StringVar(&config.CfgFile, "config", "", "config file (default $HOME/.config/glm/config.yaml)")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
}
