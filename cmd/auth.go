package cmd

import (
	"fmt"
	"syscall"

	"glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication credentials",
	Long:  `Manage API keys for GLM providers.`,
}

var setCmd = &cobra.Command{
	Use:   "set",
	Short: "Set credentials for a provider",
}

var setGlmCmd = &cobra.Command{
	Use:   "glm",
	Short: "Set GLM Coding Plan API Key (Secure Prompt)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n\033[1;36mSet GLM API Key\033[0m\n")
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")
		fmt.Print("  [*] Enter GLM API Key: ")
		bytePassword, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fmt.Printf("\n  \033[31m[-] Error reading input: %v\033[0m\n", err)
			return
		}
		fmt.Println()

		key := string(bytePassword)
		if key == "" {
			fmt.Println("  \033[33m[!] API Key cannot be empty.\033[0m")
			return
		}

		viper.Set("glm.api_key", key)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("  \033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Println("  \033[32m[+] GLM API Key saved securely.\033[0m")
	},
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(setCmd)
	setCmd.AddCommand(setGlmCmd)
}
