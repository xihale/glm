package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	allowedGLMFields = map[string]bool{
		"api_key":  true,
		"base_url": true,
		"enabled":  true,
	}
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value.

Supported keys:
  proxy                    - Set HTTP/SOCKS proxy

  glm.<name>.api_key       - Set provider API key
  glm.<name>.base_url      - Set provider base URL
  glm.<name>.enabled       - Enable/disable provider (true/false)

Examples:
  config set proxy http://127.0.0.1:1080
  config set glm.work.api_key sk-xxxxx
  config set glm.work.enabled false`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeConfigKey,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		value := args[1]

		if err := setConfigValue(key, value); err != nil {
			fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
			os.Exit(1)
		}

		if err := config.SaveConfig(); err != nil {
			fmt.Printf("\033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Printf("\033[32m[+] %s = %s\033[0m\n", key, value)
	},
}

func completeConfigKey(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	parts := strings.Split(toComplete, ".")

	if len(parts) <= 1 {
		suggestions := []string{"proxy", "glm."}
		var filtered []string
		for _, s := range suggestions {
			if strings.HasPrefix(s, toComplete) {
				filtered = append(filtered, s)
			}
		}
		return filtered, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}

	if len(parts) >= 2 {
		providerType := parts[0]
		suggestions := []string{}
		for _, p := range config.Current.Providers {
			if p.Type == providerType || providerType == "glm" {
				prefix := fmt.Sprintf("glm.%s", p.Name)
				if strings.HasPrefix(toComplete, prefix+".") {
					for f := range allowedGLMFields {
						suggestions = append(suggestions, fmt.Sprintf("%s.%s", prefix, f))
					}
				} else {
					suggestions = append(suggestions, prefix+".")
				}
			}
		}
		var filtered []string
		for _, s := range suggestions {
			if strings.HasPrefix(s, toComplete) {
				filtered = append(filtered, s)
			}
		}
		return filtered, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}

func setConfigValue(key, value string) error {
	parts := strings.Split(key, ".")

	if len(parts) == 1 && parts[0] == "proxy" {
		config.Current.Proxy = value
		viper.Set("proxy", value)
		return nil
	}

	// glm.<name>.<field>
	if len(parts) == 3 && parts[0] == "glm" {
		providerName := parts[1]
		field := parts[2]

		if !allowedGLMFields[field] {
			return fmt.Errorf("field '%s' is not configurable. Available: api_key, base_url, enabled", field)
		}

		providerIdx := -1
		for i, p := range config.Current.Providers {
			if p.Name == providerName {
				providerIdx = i
				break
			}
		}

		if providerIdx == -1 {
			return fmt.Errorf("provider '%s' not found. Use 'glm list' to see available providers", providerName)
		}

		p := &config.Current.Providers[providerIdx]
		switch field {
		case "api_key":
			p.APIKey = value
		case "base_url":
			p.BaseURL = value
		case "enabled":
			if value == "true" || value == "1" {
				p.Enabled = true
			} else if value == "false" || value == "0" {
				p.Enabled = false
			} else {
				return fmt.Errorf("invalid boolean value: %s (use true/false)", value)
			}
		}

		viper.Set("providers", config.Current.Providers)
		return nil
	}

	return fmt.Errorf("unknown configuration key: %s", key)
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configSetCmd)
}
