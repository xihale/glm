package cmd

import (
	"fmt"
	"os"

	"ai-daemon/internal/utils"

	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to a provider (OAuth flow)",
}

var loginGeminiCmd = &cobra.Command{
	Use:   "gemini [account_name]",
	Short: "Log in to Gemini/Antigravity via Google OAuth",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n\033[1;36mGoogle OAuth Authentication Flow\033[0m\n")
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		account := ""
		if len(args) > 0 {
			account = args[0]
		}
		if account != "" {
			fmt.Printf("  [*] Target Account: %s\n", account)
		} else {
			fmt.Println("  [*] Target Account: Default (Global)")
		}

		fmt.Printf("  [*] Starting browser flow ... ")
		if err := utils.LoginGemini(account); err != nil {
			fmt.Printf("\033[31m[-] Failed: %v\033[0m\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[32m[+] Success\033[0m\n\n")
	},
}

func init() {
	authCmd.AddCommand(loginCmd)
	loginCmd.AddCommand(loginGeminiCmd)
}
