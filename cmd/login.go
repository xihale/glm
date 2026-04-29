package cmd

import (
	"fmt"
	"syscall"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login [name]",
	Short: "Add a provider account",
	Long:  `Add a GLM provider account. Name can be passed as argument or will be prompted.`,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var name string
		if len(args) > 0 {
			name = args[0]
		} else {
			fmt.Print("  [*] Enter provider name: ")
			fmt.Scanln(&name)
		}

		if name == "" {
			fmt.Println("  \033[33m[!] Name cannot be empty.\033[0m")
			return
		}

		// Check if already exists
		for _, p := range config.Current.Providers {
			if p.Name == name {
				fmt.Printf("  \033[33m[!] Provider '%s' already exists. Use 'config set glm.%s.api_key' to update.\033[0m\n", name, name)
				return
			}
		}

		fmt.Printf("\n\033[1;36mLogin: %s\033[0m\n", name)
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")
		fmt.Print("  [*] Enter API Key: ")
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

		config.Current.Providers = append(config.Current.Providers, config.ProviderConfig{
			Name:    name,
			Type:    "glm",
			APIKey:  key,
			Enabled: true,
		})
		viper.Set("providers", config.Current.Providers)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("  \033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Printf("  \033[32m[+] Provider '%s' added.\033[0m\n", name)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
