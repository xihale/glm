package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ai-daemon/internal/utils"
	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers"
	pkgutils "ai-daemon/pkg/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Perform scheduled activation and schedule next run via cron",
	Long:  `Checks for expired quotas, activates them, and schedules the next run based on the earliest future reset time.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDaemonOneShot()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Remove scheduled daemon task from crontab",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Removing scheduled daemon task from crontab...")
		if err := pkgutils.RemoveScheduledRun(); err != nil {
			fmt.Printf("Error: %v\n", err)
		} else {
			fmt.Println("Stopped.")
		}
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(stopCmd)
}

func runDaemonOneShot() {
	fmt.Printf("\n\033[1;36mOne-Shot Daemon Task (%s)\033[0m\n", time.Now().Format("15:04:05"))
	fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

	refreshAllTokens()

	registry := providers.LoadProvidersFromConfig()

	var earliestReset time.Time
	now := time.Now()

	for _, p := range registry {
		if err := p.Authenticate(); err != nil {
			fmt.Printf("Warning: %s Auth failed: %v\n", p.Name(), err)
			continue
		}

		quota, err := p.GetQuota()
		if err != nil {
			fmt.Printf("Warning: %s failed to get quota: %v\n", p.Name(), err)
			continue
		}

		if err := p.Activate(false, false); err != nil {
			fmt.Printf("Warning: %s activation error: %v\n", p.Name(), err)
		}

		var modelResets map[string]pkgutils.ModelQuota
		switch p.ID() {
		case "antigravity":
			modelResets = pkgutils.ExtractAllModelQuotas(quota.Raw)
		case "geminicli":
			modelResets = pkgutils.ExtractAllCliQuotas(quota.Raw)
		default:
			if !quota.ResetTime.IsZero() && quota.ResetTime.After(now) {
				if earliestReset.IsZero() || quota.ResetTime.Before(earliestReset) {
					earliestReset = quota.ResetTime
				}
			}
		}

		if len(modelResets) > 0 {
			pEarliest := pkgutils.GetEarliestFutureResetTime(modelResets)
			if !pEarliest.IsZero() {
				if earliestReset.IsZero() || pEarliest.Before(earliestReset) {
					earliestReset = pEarliest
				}
			}
		}
	}

	if earliestReset.IsZero() {
		earliestReset = now.Add(1 * time.Hour)
		fmt.Printf("No upcoming reset times found. Scheduling next check in 1 hour: %s\n",
			earliestReset.Local().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("Earliest upcoming reset found at (Local): %s\n",
			earliestReset.Local().Format("2006-01-02 15:04:05"))
	}

	nextRun := earliestReset.Add(1 * time.Minute)

	home, _ := os.UserHomeDir()
	installedPath := filepath.Join(home, ".local", "bin", "ai-daemon")

	if _, err := os.Stat(installedPath); os.IsNotExist(err) {
		fmt.Printf("Error: ai-daemon is not installed in %s\n", installedPath)
		fmt.Println("Please run 'ai-daemon install' first.")
		return
	}

	currentExec, _ := os.Executable()
	realInstalledPath, _ := filepath.EvalSymlinks(installedPath)
	realCurrentExec, _ := filepath.EvalSymlinks(currentExec)

	if realInstalledPath != realCurrentExec {
		fmt.Printf("\n\033[33m[!] Warning: You are running %s\033[0m\n", currentExec)
		fmt.Printf("\033[33m    The scheduled task will use %s\033[0m\n\n", installedPath)
	}

	configPath := viper.ConfigFileUsed()
	if configPath == "" {
		configPath = filepath.Join(home, ".config", "ai-daemon", "config.yaml")
	}

	fmt.Printf("Scheduling next run via cron for (Local): %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := pkgutils.ScheduleNextRun(nextRun, installedPath, configPath); err != nil {
		fmt.Printf("Error scheduling next run: %v\n", err)
	}

	fmt.Println("Daemon task completed.")
}

func refreshAllTokens() {
	geminiConfig := config.Current.Gemini
	if geminiConfig.RefreshToken != "" {
		if geminiConfig.Expiry.IsZero() || time.Now().Add(5*time.Minute).After(geminiConfig.Expiry) {
			fmt.Printf("Refreshing Global Gemini token...\n")
			newTokens, err := utils.RefreshGeminiToken(geminiConfig.RefreshToken)
			if err != nil {
				fmt.Printf("Warning: Global Token refresh failed: %v\n", err)
			} else {
				config.Current.Gemini.AccessToken = newTokens.AccessToken
				viper.Set("gemini.access_token", newTokens.AccessToken)

				if newTokens.RefreshToken != "" {
					config.Current.Gemini.RefreshToken = newTokens.RefreshToken
					viper.Set("gemini.refresh_token", newTokens.RefreshToken)
				}

				if newTokens.ExpiresIn > 0 {
					expiry := time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
					config.Current.Gemini.Expiry = expiry
					viper.Set("gemini.expiry", expiry)
				}
				fmt.Println("Global Token refreshed successfully.")
			}
		}
	}

	var updated bool
	for i, pCfg := range config.Current.Providers {
		if pCfg.RefreshToken == "" {
			continue
		}

		if pCfg.Expiry.IsZero() || time.Now().Add(5*time.Minute).After(pCfg.Expiry) {
			fmt.Printf("Refreshing token for provider '%s' (%s)...\n", pCfg.Name, pCfg.Type)
			newTokens, err := utils.RefreshGeminiToken(pCfg.RefreshToken)
			if err != nil {
				fmt.Printf("Warning: Token refresh failed for '%s': %v\n", pCfg.Name, err)
				continue
			}

			config.Current.Providers[i].AccessToken = newTokens.AccessToken
			if newTokens.RefreshToken != "" {
				config.Current.Providers[i].RefreshToken = newTokens.RefreshToken
			}
			if newTokens.ExpiresIn > 0 {
				config.Current.Providers[i].Expiry = time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
			}
			updated = true
			fmt.Printf("Token refreshed for '%s'.\n", pCfg.Name)
		}
	}

	if updated || geminiConfig.RefreshToken != "" {
		if updated {
			viper.Set("providers", config.Current.Providers)
		}

		if err := config.SaveConfig(); err != nil {
			fmt.Printf("Warning: Failed to save config: %v\n", err)
		}
	}
}
