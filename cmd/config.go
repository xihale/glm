package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	allowedGLMFields = map[string]bool{
		"api_key":  true,
		"base_url": true,
	}
	allowedDaemonFields = map[string]bool{
		"activation_retry.max_attempts":  true,
		"activation_retry.initial_delay": true,
		"activation_retry.delay_step":    true,
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
  proxy                                   - Set HTTP/SOCKS proxy

  daemon.activation_retry.max_attempts    - Total activation attempts including the first run
  daemon.activation_retry.initial_delay   - First retry delay (Go duration, e.g. 5s)
  daemon.activation_retry.delay_step      - Arithmetic increment between retries

  {type}.{name}.api_key                   - Set provider API key
  {type}.{name}.base_url                  - Set provider base URL
  {type}.{name}.enabled                   - Enable/disable provider (true/false)

  GLM-specific:
    glm.{name}.api_key    - GLM API key
    glm.{name}.base_url   - GLM base URL

Examples:
  config set proxy http://127.0.0.1:1080
  config set daemon.activation_retry.max_attempts 4
  config set daemon.activation_retry.initial_delay 5s
  config set glm.glm.api_key sk-xxxxx`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeConfigKey,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n\033[1;36mConfigure Setting\033[0m\n")
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		key := args[0]
		value := args[1]

		if err := setConfigValue(key, value); err != nil {
			fmt.Printf("  \033[31m[-] Error: %v\033[0m\n\n", err)
			os.Exit(1)
		}

		if err := config.SaveConfig(); err != nil {
			fmt.Printf("  \033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Printf("  \033[32m[+] %s set to %s\033[0m\n\n", key, value)
	},
}

func completeConfigKey(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	current := strings.Join([]string{toComplete}, "")

	parts := strings.Split(current, ".")

	if len(parts) == 1 || (len(parts) == 2 && toComplete[len(toComplete)-1] != '.') {
		suggestions := []string{"proxy", "daemon."}

		providerTypes := make(map[string]bool)
		for _, p := range config.Current.Providers {
			if !providerTypes[p.Type] {
				providerTypes[p.Type] = true
			}
		}

		for providerType := range providerTypes {
			suggestions = append(suggestions, providerType+".")
		}

		var filtered []string
		for _, s := range suggestions {
			if strings.HasPrefix(s, toComplete) {
				filtered = append(filtered, s)
			}
		}

		return filtered, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}

	if len(parts) == 2 || (len(parts) == 3 && toComplete[len(toComplete)-1] != '.') {
		if parts[0] == "daemon" {
			suggestions := []string{}
			for field := range allowedDaemonFields {
				fullKey := "daemon." + field
				if strings.HasPrefix(fullKey, toComplete) {
					suggestions = append(suggestions, fullKey)
				}
			}
			return suggestions, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
		}

		providerType := parts[0]

		suggestions := []string{}

		for _, p := range config.Current.Providers {
			if p.Type == providerType {
				prefix := fmt.Sprintf("%s.%s", p.Type, p.Name)
				if strings.HasPrefix(toComplete, prefix+".") {
					providerFields := getProviderFields(p.Type)
					for _, f := range providerFields {
						suggestions = append(suggestions, fmt.Sprintf("%s.%s.%s", p.Type, p.Name, f))
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

	if len(parts) >= 3 {
		if parts[0] == "daemon" {
			suggestions := []string{}
			for field := range allowedDaemonFields {
				fullKey := "daemon." + field
				if strings.HasPrefix(fullKey, toComplete) {
					suggestions = append(suggestions, fullKey)
				}
			}
			return suggestions, cobra.ShellCompDirectiveNoFileComp
		}

		suggestions := []string{}

		for _, p := range config.Current.Providers {
			prefix := fmt.Sprintf("%s.%s", p.Type, p.Name)
			if strings.HasPrefix(toComplete, prefix+".") {
				providerFields := getProviderFields(p.Type)
				for _, f := range providerFields {
					fullKey := fmt.Sprintf("%s.%s.%s", p.Type, p.Name, f)
					if strings.HasPrefix(fullKey, toComplete) {
						suggestions = append(suggestions, fullKey)
					}
				}
			}
		}

		return suggestions, cobra.ShellCompDirectiveNoFileComp
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}

func getProviderFields(providerType string) []string {
	var fields []string

	switch providerType {
	case "glm":
		for f := range allowedGLMFields {
			fields = append(fields, f)
		}
	default:
		for f := range allowedGLMFields {
			fields = append(fields, f)
		}
	}

	return fields
}

func setConfigValue(key, value string) error {
	parts := strings.Split(key, ".")

	if len(parts) == 1 && parts[0] == "proxy" {
		config.Current.Proxy = value
		viper.Set("proxy", value)
		return nil
	}

	if len(parts) == 3 && parts[0] == "daemon" && parts[1] == "activation_retry" {
		field := parts[2]
		if !allowedDaemonFields[parts[1]+"."+field] {
			return fmt.Errorf("field '%s' is not configurable for daemon settings", field)
		}

		retry := &config.Current.Daemon.ActivationRetry
		switch field {
		case "max_attempts":
			maxAttempts, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid integer value: %s", value)
			}
			retry.MaxAttempts = maxAttempts
		case "initial_delay":
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("invalid duration value: %s", value)
			}
			retry.InitialDelay = value
		case "delay_step":
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("invalid duration value: %s", value)
			}
			retry.DelayStep = value
		}

		viper.Set("daemon", config.Current.Daemon)
		return nil
	}

	if len(parts) == 3 {
		providerType := parts[0]
		providerName := parts[1]
		field := parts[2]

		providerIdx := -1
		for i, p := range config.Current.Providers {
			if p.Type == providerType && p.Name == providerName {
				providerIdx = i
				break
			}
		}

		if providerIdx == -1 {
			return fmt.Errorf("provider '%s' of type '%s' not found. Use 'auth list' to see available providers", providerName, providerType)
		}

		p := &config.Current.Providers[providerIdx]

		allowedFields := allowedGLMFields

		if !allowedFields[field] {
			return fmt.Errorf("field '%s' is not configurable for %s providers. Use 'config set --help' to see available fields", field, p.Type)
		}

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
