package cmd

import (
	"fmt"
	"os"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var setBaseURLCmd = &cobra.Command{
	Use:   "set-base-url [provider] [url]",
	Short: "Set custom Base URL for a provider (e.g. glm)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		provider := args[0]
		url := args[1]

		switch provider {
		case "glm":
			viper.Set("glm.base_url", url)
		default:
			fmt.Printf("Unknown provider: %s\n", provider)
			os.Exit(1)
		}

		if err := config.SaveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			return
		}
		fmt.Printf("Base URL for %s set to %s\n", provider, url)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(setBaseURLCmd)
}
