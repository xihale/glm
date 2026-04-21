package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"glm/pkg/providers"
	pkgutils "glm/pkg/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run scheduled activation and schedule next run via cron",
	Run: func(cmd *cobra.Command, args []string) {
		runDaemonOneShot()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Remove scheduled daemon task from crontab",
	Run: func(cmd *cobra.Command, args []string) {
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
	registry := providers.LoadProvidersFromConfig()
	now := time.Now()
	var earliestReset time.Time

	for _, p := range registry {
		if err := p.Authenticate(); err != nil {
			fmt.Printf("%s auth failed: %v\n", p.Name(), err)
			continue
		}

		quota, err := p.GetQuota()
		if err != nil {
			fmt.Printf("%s quota check failed: %v\n", p.Name(), err)
			continue
		}

		// Track earliest reset time
		if !quota.ResetTime.IsZero() && quota.ResetTime.After(now) {
			if earliestReset.IsZero() || quota.ResetTime.Before(earliestReset) {
				earliestReset = quota.ResetTime
			}
		}

		// Only activate if quota is not full or reset time has passed
		needsActivation := quota.Remaining < 100 ||
			quota.ResetTime.IsZero() ||
			!quota.ResetTime.After(now)

		if !needsActivation {
			at := quota.ResetTime.Local().Format("15:04:05")
			fmt.Printf("%s quota full (%d%%), next reset at %s. Skipping.\n", p.Name(), quota.Remaining, at)
			continue
		}

		if _, err := p.Activate(nil, false, false); err != nil {
			fmt.Printf("%s activation error: %v\n", p.Name(), err)
		}
	}

	if earliestReset.IsZero() {
		earliestReset = now.Add(1 * time.Hour)
		fmt.Printf("No reset times found. Next check in 1 hour: %s\n",
			earliestReset.Local().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("Next reset at: %s\n",
			earliestReset.Add(5 * time.Second).Local().Format("2006-01-02 15:04:05"))
	}

	nextRun := earliestReset.Add(5 * time.Second).Add(1 * time.Minute)
	home, _ := os.UserHomeDir()
	installedPath := filepath.Join(home, ".local", "bin", "glm")

	if _, err := os.Stat(installedPath); os.IsNotExist(err) {
		fmt.Printf("Error: glm is not installed in %s\n", installedPath)
		fmt.Println("Please run 'glm install' first.")
		return
	}

	currentExec, _ := os.Executable()
	realInstalledPath, _ := filepath.EvalSymlinks(installedPath)
	realCurrentExec, _ := filepath.EvalSymlinks(currentExec)

	if realInstalledPath != realCurrentExec {
		fmt.Printf("[!] Warning: You are running %s\n", currentExec)
		fmt.Printf("    The scheduled task will use %s\n", installedPath)
	}

	configPath := viper.ConfigFileUsed()
	if configPath == "" {
		configPath = filepath.Join(home, ".config", "glm", "config.yaml")
	}

	fmt.Printf("Scheduling next run: %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := pkgutils.ScheduleNextRun(nextRun, installedPath, configPath); err != nil {
		fmt.Printf("Error scheduling next run: %v\n", err)
	}
}
