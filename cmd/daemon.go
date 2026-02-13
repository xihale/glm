package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-daemon/pkg/providers"
	"ai-daemon/pkg/providers/interfaces"
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

	registry := providers.LoadProvidersFromConfig()

	var earliestReset time.Time
	var wg sync.WaitGroup
	var mu sync.Mutex
	now := time.Now()

	for _, p := range registry {
		wg.Add(1)
		go func(prov interfaces.Provider) {
			defer wg.Done()

			// Capture output to buffer
			var output strings.Builder

			if err := prov.Authenticate(); err != nil {
				output.WriteString(fmt.Sprintf("Warning: %s Auth failed: %v\n", prov.Name(), err))
				mu.Lock()
				fmt.Print(output.String())
				mu.Unlock()
				return
			}

			quota, err := prov.GetQuota()
			if err != nil {
				output.WriteString(fmt.Sprintf("Warning: %s failed to get quota: %v\n", prov.Name(), err))
				mu.Lock()
				fmt.Print(output.String())
				mu.Unlock()
				return
			}

			// Pass nil writer to Activate for daemon, as we capture errors differently or rely on internal logging?
			// Actually, let's capture Activate logs too if possible, but Activate logs are verbose "Activating..."
			// For daemon, maybe we want silent unless error?
			// The original code printed "Warning: ... activation error" if it failed.
			// Let's use a buffer to capture standard activation logs too, so they appear in cron logs atomically.
			if err := prov.Activate(&output, false, false); err != nil {
				output.WriteString(fmt.Sprintf("Warning: %s activation error: %v\n", prov.Name(), err))
			}

			// Calculate reset time
			var modelResets map[string]pkgutils.ModelQuota
			id := prov.ID()
			if strings.HasPrefix(id, "antigravity") {
				modelResets = pkgutils.ExtractAllModelQuotas(quota.Raw)
			} else if strings.HasPrefix(id, "geminicli") {
				modelResets = pkgutils.ExtractAllCliQuotas(quota.Raw)
			} else {
				if !quota.ResetTime.IsZero() && quota.ResetTime.After(now) {
					mu.Lock()
					if earliestReset.IsZero() || quota.ResetTime.Before(earliestReset) {
						earliestReset = quota.ResetTime
					}
					mu.Unlock()
				}
			}

			if len(modelResets) > 0 {
				pEarliest := pkgutils.GetEarliestFutureResetTime(modelResets)
				if !pEarliest.IsZero() {
					mu.Lock()
					if earliestReset.IsZero() || pEarliest.Before(earliestReset) {
						earliestReset = pEarliest
					}
					mu.Unlock()
				}
			}

			// Atomic print
			mu.Lock()
			if output.Len() > 0 {
				fmt.Print(output.String())
			}
			mu.Unlock()
		}(p)
	}

	wg.Wait()

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
