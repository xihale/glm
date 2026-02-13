package cmd

import (
	"fmt"
	"syscall"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication credentials",
	Long:  `Manage API keys and session tokens for GLM, Gemini, and other providers.`,
}

var setCmd = &cobra.Command{
	Use:   "set",
	Short: "Set credentials for a provider",
}

var setGlmCmd = &cobra.Command{
	Use:   "glm",
	Short: "Set GLM Coding Plan API Key (Secure Prompt)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print("Enter GLM API Key: ")
		bytePassword, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fmt.Printf("\nError reading input: %v\n", err)
			return
		}
		fmt.Println()

		key := string(bytePassword)
		if key == "" {
			fmt.Println("API Key cannot be empty.")
			return
		}

		viper.Set("glm.api_key", key)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			return
		}
		fmt.Println("GLM API Key saved securely.")
	},
}

var setGeminiCmd = &cobra.Command{
	Use:   "gemini",
	Short: "Set Gemini Web session cookies (Secure Prompt)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print("Enter __Secure-1PSID: ")
		byte1PSID, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fmt.Printf("\nError reading input: %v\n", err)
			return
		}
		fmt.Println()

		fmt.Print("Enter __Secure-1PSIDTS: ")
		byte1PSIDTS, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fmt.Printf("\nError reading input: %v\n", err)
			return
		}
		fmt.Println()

		psid := string(byte1PSID)
		psidts := string(byte1PSIDTS)

		if psid == "" || psidts == "" {
			fmt.Println("Session cookies cannot be empty.")
			return
		}

		viper.Set("gemini.secure_1psid", psid)
		viper.Set("gemini.secure_1psidts", psidts)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			return
		}
		fmt.Println("Gemini session cookies saved securely.")
	},
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(setCmd)
	setCmd.AddCommand(setGlmCmd)
	setCmd.AddCommand(setGeminiCmd)
}
