package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xihale/glm/pkg/providers"
	pkgutils "github.com/xihale/glm/pkg/utils"

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

// daemonPaths resolves the installed binary path and config path for scheduling.
// Returns (installedPath, configPath, error).
func daemonPaths() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	installedPath := filepath.Join(home, ".local", "bin", "glm")
	if _, err := os.Stat(installedPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("glm is not installed in %s — please run 'glm install' first", installedPath)
	}

	configPath := viper.ConfigFileUsed()
	if configPath == "" {
		configPath = filepath.Join(home, ".config", "glm", "config.yaml")
	}

	return installedPath, configPath, nil
}

// warnIfDifferentBinary checks whether the running binary differs from the
// installed one and prints a warning if so.
func warnIfDifferentBinary(installedPath string) {
	currentExec, err := os.Executable()
	if err != nil {
		return
	}
	realInstalled, err1 := filepath.EvalSymlinks(installedPath)
	realCurrent, err2 := filepath.EvalSymlinks(currentExec)
	if err1 != nil || err2 != nil || realInstalled != realCurrent {
		fmt.Printf("[!] Warning: You are running %s\n", currentExec)
		fmt.Printf("    The scheduled task will use %s\n", installedPath)
	}
}

// activateProviders authenticates and activates all configured providers.
// It returns the earliest future reset time found (or zero if none).
func activateProviders() time.Time {
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

	return earliestReset
}

// resolveNextRun calculates the next daemon run time from the earliest reset.
// Falls back to 1 hour from now if no reset time is available.
func resolveNextRun(earliestReset time.Time) time.Time {
	if earliestReset.IsZero() {
		next := time.Now().Add(1 * time.Hour)
		fmt.Printf("No reset times found. Next check in 1 hour: %s\n",
			next.Local().Format("2006-01-02 15:04:05"))
		return next
	}

	fmt.Printf("Next reset at: %s\n",
		earliestReset.Add(pkgutils.ResetBuffer).Local().Format("2006-01-02 15:04:05"))
	return earliestReset.Add(pkgutils.ResetBuffer).Add(pkgutils.ScheduleExtraDelay)
}

func runDaemonOneShot() {
	earliestReset := activateProviders()

	nextRun := resolveNextRun(earliestReset)

	installedPath, configPath, err := daemonPaths()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	warnIfDifferentBinary(installedPath)

	fmt.Printf("Scheduling next run: %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := pkgutils.ScheduleNextRun(nextRun, installedPath, configPath); err != nil {
		fmt.Printf("Error scheduling next run: %v\n", err)
	}
}
