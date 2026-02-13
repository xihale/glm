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
	Use:   "gemini",
	Short: "Log in to Gemini/Antigravity via Google OAuth",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Starting Gemini OAuth flow...")
		if err := utils.LoginGemini(); err != nil {
			fmt.Printf("Login failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	authCmd.AddCommand(loginCmd)
	loginCmd.AddCommand(loginGeminiCmd)
}
