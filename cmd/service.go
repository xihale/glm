package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/xihale/glm/pkg/config"
	pkgutils "github.com/xihale/glm/pkg/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	systemdServiceUnit = "glm.service"
	systemdTimerUnit   = "glm.timer"
)

type systemdUnitTemplateData struct {
	ExecPath   string
	ConfigPath string
	TimerSpec  string
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage systemd user units for scheduled activation",
	Long: `Manage systemd user units for scheduled activation.

When a schedule is configured (via 'glm schedule set'), this command installs
systemd user units that activate at the scheduled times. Uses OnCalendar for
absolute time scheduling.`,
}

var installServiceCmd = &cobra.Command{
	Use:   "install",
	Short: "Install systemd user service and timer",
	Run: func(cmd *cobra.Command, args []string) {
		if config.Current.Schedule.IsEmpty() {
			fmt.Println("\033[31m[-] No schedule configured. Run 'glm schedule set' first.\033[0m")
			os.Exit(1)
		}

		execPath, configPath, err := servicePaths()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		timerSpec := buildOnCalendarSpec(config.Current.Schedule)
		if err := installSystemdUnits(execPath, configPath, timerSpec); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		serviceDir, _, timerFile := systemdUnitPaths()
		fmt.Printf("Systemd units installed to %s\n", serviceDir)
		fmt.Printf("- %s\n", filepath.Base(timerFile))
		fmt.Printf("- %s\n", systemdServiceUnit)
		fmt.Println("Run: systemctl --user daemon-reload")
		fmt.Println("     systemctl --user enable --now glm.timer")
	},
}

var uninstallServiceCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall systemd user service and timer",
	Run: func(cmd *cobra.Command, args []string) {
		serviceFile, timerFile, err := installedSystemdUnitFiles()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		removed, err := removeSystemdUnits(serviceFile, timerFile)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if removed == 0 {
			fmt.Printf("Systemd unit files not found: %s, %s\n", serviceFile, timerFile)
			os.Exit(1)
		}

		fmt.Printf("Removed %d systemd unit(s)\n", removed)
		fmt.Println("Run: systemctl --user daemon-reload")
		fmt.Println("     systemctl --user disable --now glm.timer")
	},
}

var serviceRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run scheduled activation (called by systemd timer)",
	Long:  `Run one activation cycle for all providers. Intended to be called by systemd timer.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		if !waitForScheduledReset(debug) {
			return nil
		}
		runActivation(debug, false)
		return nil
	},
}

const scheduleResetWaitThreshold = 10 * time.Minute

func waitForScheduledReset(debug bool) bool {
	earliestReset := collectEarliestReset(debug)
	if earliestReset.IsZero() {
		return true
	}

	resetAt := earliestReset.Add(pkgutils.ResetBuffer)
	until := time.Until(resetAt)
	if until <= 0 {
		return true
	}

	if until <= scheduleResetWaitThreshold {
		fmt.Printf("Next reset is in %s at %s; sleeping until then.\n",
			pkgutils.FormatTimeUntil(resetAt),
			resetAt.Local().Format("15:04:05"))
		time.Sleep(until)
		return true
	}

	fmt.Printf("Next reset is %s away; exiting and waiting for the next schedule.\n", pkgutils.FormatTimeUntil(resetAt))
	return false
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.AddCommand(installServiceCmd)
	serviceCmd.AddCommand(uninstallServiceCmd)
	serviceCmd.AddCommand(serviceRunCmd)
	serviceRunCmd.Flags().Bool("debug", false, "Show raw API response")
}

// servicePaths resolves the current executable path and config path for systemd service runs.
func servicePaths() (string, string, error) {
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

// buildOnCalendarSpec builds OnCalendar entries from schedule config.
// Each time becomes an OnCalendar= entry with the specified timezone.
func buildOnCalendarSpec(sched config.ScheduleConfig) string {
	var lines []string
	lines = append(lines, "RandomizedDelaySec=0")

	loc, err := parseScheduleLocation(sched.Timezone)
	if err != nil {
		loc = time.UTC
	}

	for _, t := range sched.Times {
		parts := strings.Split(t, ":")
		if len(parts) == 3 {
			lines = append(lines, fmt.Sprintf("OnCalendar=*-*-* %s:%s:%s %s",
				parts[0], parts[1], parts[2], loc.String()))
		}
	}

	// Fallback: start 30s after boot
	lines = append(lines, "OnBootSec=30")

	return strings.Join(lines, "\n")
}

// parseScheduleNextActivation calculates the next activation time from schedule config.
// Used only for display purposes.
func parseScheduleNextActivation() time.Time {
	sched := config.Current.Schedule
	if sched.IsEmpty() {
		return time.Time{}
	}

	loc, err := parseScheduleLocation(sched.Timezone)
	if err != nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	var earliest time.Time

	for _, t := range sched.Times {
		parts := strings.Split(t, ":")
		if len(parts) != 3 {
			continue
		}

		var h, m, s int
		fmt.Sscanf(parts[0], "%d", &h)
		fmt.Sscanf(parts[1], "%d", &m)
		fmt.Sscanf(parts[2], "%d", &s)

		candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, s, 0, loc)
		if !candidate.After(now) {
			candidate = candidate.Add(24 * time.Hour)
		}

		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}

	return earliest
}

func systemdUnitPaths() (serviceDir string, serviceFile string, timerFile string) {
	home, _ := os.UserHomeDir()
	serviceDir = filepath.Join(home, ".config", "systemd", "user")
	serviceFile = filepath.Join(serviceDir, systemdServiceUnit)
	timerFile = filepath.Join(serviceDir, systemdTimerUnit)
	return serviceDir, serviceFile, timerFile
}

func installedSystemdUnitFiles() (string, string, error) {
	_, serviceFile, timerFile := systemdUnitPaths()
	return serviceFile, timerFile, nil
}

func installSystemdUnits(execPath string, configPath string, timerSpec string) error {
	serviceDir, serviceFile, timerFile := systemdUnitPaths()
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return err
	}

	data := systemdUnitTemplateData{
		ExecPath:   execPath,
		ConfigPath: configPath,
		TimerSpec:  timerSpec,
	}
	if err := writeSystemdUnitFile(serviceFile, "service", serviceTemplate, data); err != nil {
		return err
	}
	if err := writeSystemdUnitFile(timerFile, "timer", timerTemplate, data); err != nil {
		return err
	}
	return nil
}

func writeSystemdUnitFile(path string, name string, tmpl string, data systemdUnitTemplateData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	parsed, err := template.New(name).Parse(tmpl)
	if err != nil {
		return err
	}
	return parsed.Execute(f, data)
}

func removeSystemdUnits(serviceFile string, timerFile string) (int, error) {
	removed := 0
	for _, path := range []string{serviceFile, timerFile} {
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return removed, err
			}
			removed++
			continue
		} else if !os.IsNotExist(err) {
			return removed, err
		}
	}
	return removed, nil
}

func runSystemctlUser(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

const serviceTemplate = `[Unit]
Description=GLM Activation Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart={{.ExecPath}} service run --config {{.ConfigPath}}
StandardOutput=journal
StandardError=journal
`

const timerTemplate = `[Unit]
Description=GLM Activation Timer

[Timer]
Unit=glm.service
{{.TimerSpec}}
Persistent=true
AccuracySec=1s

[Install]
WantedBy=timers.target
`
