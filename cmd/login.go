package cmd

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Set API key",
	Long:  `Set or update the GLM API key. Replaces any existing key.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// If key provided via flag, use it directly
		if apiKeyFlag != "" {
			config.Current.APIKey = apiKeyFlag
		} else {
			// Interactive prompt
			if config.Current.APIKey != "" {
				masked := config.Current.APIKey
				if len(masked) > 8 {
					masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
				}
				fmt.Printf("  Current: %s\n", ui.Dimmed(masked))
			}

			fmt.Printf("%s API key: ", ui.Style("?", ui.Cyan, ui.Bold))
			bytePassword, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				fmt.Println()
				return fmt.Errorf("read API key: %w", err)
			}
			fmt.Println()

			key := strings.TrimSpace(string(bytePassword))
			if key == "" {
				return fmt.Errorf("API key cannot be empty")
			}
			config.Current.APIKey = key
		}

		if err := config.SaveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		ui.Success("API key saved")
		return nil
	},
}

var apiKeyFlag string

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().StringVarP(&apiKeyFlag, "key", "k", "", "API key (skip interactive prompt)")
}
