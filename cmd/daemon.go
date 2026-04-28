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

type schedulerMode string

const (
	schedulerModeAuto    schedulerMode = "auto"
	schedulerModeCron    schedulerMode = "cron"
	schedulerModeSystemd schedulerMode = "systemd"
)

var daemonScheduler string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run scheduled activation",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemon()
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
	daemonCmd.Flags().StringVar(&daemonScheduler, "scheduler", string(schedulerModeAuto), "scheduler backend: auto, cron, or systemd")
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

func runningUnderSystemd() bool {
	return os.Getenv("INVOCATION_ID") != "" || os.Getenv("JOURNAL_STREAM") != ""
}

func resolveSchedulerMode() (schedulerMode, error) {
	switch schedulerMode(strings.ToLower(strings.TrimSpace(daemonScheduler))) {
	case schedulerModeAuto:
		if runningUnderSystemd() {
			return schedulerModeSystemd, nil
		}
		return schedulerModeCron, nil
	case schedulerModeCron:
		return schedulerModeCron, nil
	case schedulerModeSystemd:
		return schedulerModeSystemd, nil
	default:
		return "", fmt.Errorf("invalid scheduler %q (expected: auto, cron, systemd)", daemonScheduler)
	}
}

func isSystemdTimerActive() bool {
	out, err := exec.Command("systemctl", "--user", "is-active", systemdTimerUnit).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

// warnIfSystemdConflict warns if both systemd timer and crontab are active,
// which would cause duplicate daemon runs.
func warnIfSystemdConflict() {
	if isSystemdTimerActive() {
		fmt.Println("[!] Warning: systemd timer 'glm.timer' is active.")
		fmt.Println("    Running both systemd timer and crontab scheduling may cause duplicate runs.")
		fmt.Println("    Consider disabling one: systemctl --user stop glm.timer && systemctl --user disable glm.timer")
	}
}

// warnIfCrontabConflict warns if systemd mode is enabled while cron entries still exist.
func warnIfCrontabConflict() {
	hasScheduledRun, err := pkgutils.HasScheduledRun()
	if err != nil {
		fmt.Printf("Could not inspect crontab for scheduler conflict: %v\n", err)
		return
	}
	if hasScheduledRun {
		fmt.Println("[!] Warning: crontab scheduling for 'glm' is still installed.")
		fmt.Println("    Running both systemd timer and crontab scheduling may cause duplicate runs.")
		fmt.Println("    Consider disabling one: glm stop")
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

func runActivationCycle() time.Time {
	fmt.Printf("Starting activation cycle at: %s\n", time.Now().Local().Format("2006-01-02 15:04:05"))
	return resolveNextRun(activateProviders())
}

func runDaemon() error {
	mode, err := resolveSchedulerMode()
	if err != nil {
		return err
	}

	fmt.Printf("Daemon scheduler mode: %s\n", mode)

	switch mode {
	case schedulerModeCron:
		return runDaemonCronOnce()
	case schedulerModeSystemd:
		return runDaemonSystemdOnce()
	default:
		return fmt.Errorf("unsupported scheduler mode: %s", mode)
	}
}

func runDaemonCronOnce() error {
	nextRun := runActivationCycle()

	binaryPath, configPath, err := daemonPaths()
	if err != nil {
		return err
	}

	warnIfSystemdConflict()

	fmt.Printf("Scheduling next run via crontab: %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := pkgutils.ScheduleNextRun(nextRun, binaryPath, configPath); err != nil {
		return fmt.Errorf("schedule next run: %w", err)
	}
	return nil
}

func runDaemonSystemdOnce() error {
	nextRun := runActivationCycle()

	execPath, configPath, err := daemonPaths()
	if err != nil {
		return err
	}

	warnIfCrontabConflict()

	fmt.Printf("Scheduling next run via systemd timer: %s\n",
		nextRun.Local().Format("2006-01-02 15:04:05"))

	if err := scheduleNextSystemdRun(nextRun, execPath, configPath); err != nil {
		return fmt.Errorf("schedule next run: %w", err)
	}
	return nil
}
