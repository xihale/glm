package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// daemonPaths resolves the current executable path and config path for scheduling.
// Returns (binaryPath, configPath, error).
func daemonPaths() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine current executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		resolved = execPath
	}

	configPath := viper.ConfigFileUsed()
	if configPath == "" {
		configPath = filepath.Join(home, ".config", "glm", "config.yaml")
	}

	return resolved, configPath, nil
}

// warnIfSchedulerConflict warns if both systemd service and crontab are active,
// which would cause duplicate daemon runs.
func warnIfSchedulerConflict() {
	// Check if systemd user service is active.
	out, err := exec.Command("systemctl", "--user", "is-active", "glm").Output()
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		fmt.Println("[!] Warning: systemd service 'glm' is active.")
		fmt.Println("    Running both systemd and crontab scheduling may cause duplicate runs.")
		fmt.Println("    Consider disabling one: systemctl --user stop glm && systemctl --user disable glm")
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

		// Activate handles its own quota check internally (skip if still active).
		// We only skip here if quota is clearly still active to avoid redundant work.
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

	binaryPath, configPath, err := daemonPaths()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	warnIfSchedulerConflict()

	fmt.Printf("Scheduling next run: %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := pkgutils.ScheduleNextRun(nextRun, binaryPath, configPath); err != nil {
		fmt.Printf("Error scheduling next run: %v\n", err)
	}
}
