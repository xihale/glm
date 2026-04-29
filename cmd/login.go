package cmd

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login [name]",
	Short: "Add or update a GLM account",
	Long:  `Add or update a GLM provider account. Name can be passed as an argument; API key is prompted securely.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) > 0 {
			name = strings.TrimSpace(args[0])
		}
		if name == "" {
			fmt.Print("Name: ")
			if _, err := fmt.Scanln(&name); err != nil {
				return fmt.Errorf("read name: %w", err)
			}
			name = strings.TrimSpace(name)
		}
		if name == "" {
			return fmt.Errorf("name cannot be empty")
		}

		fmt.Print("API key: ")
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

		for i := range config.Current.Providers {
			if config.Current.Providers[i].Name == name {
				config.Current.Providers[i].Type = "glm"
				config.Current.Providers[i].APIKey = key
				config.Current.Providers[i].Enabled = true
				viper.Set("providers", config.Current.Providers)
				if err := config.SaveConfig(); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Printf("Updated %s\n", name)
				return nil
			}
		}

		config.Current.Providers = append(config.Current.Providers, config.ProviderConfig{
			Name:    name,
			Type:    "glm",
			APIKey:  key,
			Enabled: true,
		})
		viper.Set("providers", config.Current.Providers)
		if err := config.SaveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Added %s\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
