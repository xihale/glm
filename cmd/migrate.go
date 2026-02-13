package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate legacy configuration to new provider format",
	Run: func(cmd *cobra.Command, args []string) {
		reader := bufio.NewReader(os.Stdin)
		migrated := false

		// Migrate GLM
		if config.Current.GLM.APIKey != "" {
			if m, ok := migrateProvider(reader, "Legacy GLM", "legacy-glm", func(name string) config.ProviderConfig {
				apiKey := promptString(reader, "GLM API Key", config.Current.GLM.APIKey)
				baseURL := promptString(reader, "GLM Base URL", config.Current.GLM.BaseURL)
				return config.ProviderConfig{
					Name:    name,
					Type:    "glm",
					APIKey:  apiKey,
					BaseURL: baseURL,
					Enabled: true,
				}
			}); ok {
				config.Current.Providers = append(config.Current.Providers, m)
				migrated = true
				if promptYesNo(reader, "Clear legacy GLM configuration?", true) {
					config.Current.GLM = config.GLMConfig{}
					viper.Set("glm", map[string]interface{}{})
					fmt.Println("Legacy GLM configuration cleared.")
				}
			}
		}

		// Migrate Gemini/Antigravity
		if config.Current.Gemini.AccessToken != "" {
			if m, ok := migrateProvider(reader, "Legacy Gemini", "legacy-gemini", func(name string) config.ProviderConfig {
				pType := "gemini"
				disableAntigravity := !promptYesNo(reader, "Enable Antigravity support?", true)
				fmt.Println("Tokens (Access/Refresh/SID) will be migrated automatically.")
				projectID := promptString(reader, "Project ID", config.Current.Gemini.ProjectID)
				return config.ProviderConfig{
					Name:               name,
					Type:               pType,
					AccessToken:        config.Current.Gemini.AccessToken,
					RefreshToken:       config.Current.Gemini.RefreshToken,
					ProjectID:          projectID,
					Expiry:             config.Current.Gemini.Expiry,
					Secure1PSID:        config.Current.Gemini.Secure1PSID,
					Secure1PSIDTS:      config.Current.Gemini.Secure1PSIDTS,
					Enabled:            true,
					DisableAntigravity: disableAntigravity,
				}
			}); ok {
				config.Current.Providers = append(config.Current.Providers, m)
				migrated = true
				if promptYesNo(reader, "Clear legacy Gemini configuration?", true) {
					config.Current.Gemini = config.GeminiConfig{}
					config.Current.Antigravity = config.AntigravityConfig{}
					viper.Set("gemini", map[string]interface{}{})
					viper.Set("antigravity", map[string]interface{}{})
					fmt.Println("Legacy Gemini configuration cleared.")
				}
			}
		}

		if migrated {
			viper.Set("providers", config.Current.Providers)
			if err := config.SaveConfig(); err != nil {
				fmt.Printf("Error saving config: %v\n", err)
			} else {
				fmt.Println("\n[+] Migration completed successfully. Configuration saved.")
			}
		} else {
			fmt.Println("\nNo migration performed.")
		}
	},
}

func migrateProvider(reader *bufio.Reader, label, defaultName string, createFunc func(name string) config.ProviderConfig) (config.ProviderConfig, bool) {
	fmt.Printf("\n[!] Found %s configuration.\n", label)
	if !promptYesNo(reader, "Migrate to providers list?", true) {
		return config.ProviderConfig{}, false
	}

	name := promptString(reader, "Enter name for this provider", defaultName)
	provider := createFunc(name)
	fmt.Printf("Added provider '%s' (type: %s, antigravity: %v)\n", provider.Name, provider.Type, !provider.DisableAntigravity)
	return provider, true
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	s := "Y/n"
	if !defaultYes {
		s = "y/N"
	}
	fmt.Printf("%s [%s]: ", prompt, s)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "" {
		return defaultYes
	}
	return input == "y" || input == "yes"
}

func promptString(reader *bufio.Reader, prompt string, defaultVal string) string {
	fmt.Printf("%s [%s]: ", prompt, defaultVal)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
