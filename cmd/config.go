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
		fmt.Printf("\n\033[1;36mConfigure Base URL\033[0m\n")
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		provider := args[0]
		url := args[1]

		switch provider {
		case "glm":
			viper.Set("glm.base_url", url)
		default:
			fmt.Printf("  \033[31m[-] Error: Unknown provider: %s\033[0m\n", provider)
			os.Exit(1)
		}

		if err := config.SaveConfig(); err != nil {
			fmt.Printf("  \033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Printf("  \033[32m[+] Base URL for %s set to %s\033[0m\n\n", provider, url)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(setBaseURLCmd)
}
