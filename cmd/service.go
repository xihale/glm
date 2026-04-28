package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
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
	Short: "Manage systemd user units",
}

var installServiceCmd = &cobra.Command{
	Use:   "install",
	Short: "Install systemd user service and timer",
	Run: func(cmd *cobra.Command, args []string) {
		execPath, configPath, err := daemonPaths()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if err := installSystemdUnits(execPath, configPath, initialSystemdTimerSpec()); err != nil {
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

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.AddCommand(installServiceCmd)
	serviceCmd.AddCommand(uninstallServiceCmd)
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

const (
	systemdBootFallback = "OnBootSec=1min"
)

func initialSystemdTimerSpec() string {
	return strings.Join([]string{
		"OnActiveSec=1s",
		systemdBootFallback,
	}, "\n")
}

func scheduledSystemdTimerSpec(nextRun time.Time) string {
	return strings.Join([]string{
		fmt.Sprintf("OnCalendar=%s", nextRun.Local().Format("2006-01-02 15:04:05")),
		systemdBootFallback,
	}, "\n")
}

func scheduleNextSystemdRun(nextRun time.Time, execPath string, configPath string) error {
	serviceFile, timerFile, err := installedSystemdUnitFiles()
	if err != nil {
		return err
	}
	if _, err := os.Stat(serviceFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("systemd service is not installed; run 'glm service install'")
		}
		return err
	}
	if _, err := os.Stat(timerFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("systemd timer is not installed; run 'glm service install'")
		}
		return err
	}

	if err := installSystemdUnits(execPath, configPath, scheduledSystemdTimerSpec(nextRun)); err != nil {
		return err
	}
	if err := runSystemctlUser("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctlUser("restart", "glm.timer"); err != nil {
		return err
	}
	return nil
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
Description=GLM Heartbeat Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart={{.ExecPath}} daemon --scheduler systemd --config {{.ConfigPath}}
StandardOutput=journal
StandardError=journal
`

const timerTemplate = `[Unit]
Description=GLM Heartbeat Timer

[Timer]
Unit=glm.service
{{.TimerSpec}}
Persistent=true
AccuracySec=1s

# OnBootSec provides a boot-time self-healing fallback for missed absolute schedules.
# Note: Do not enable both systemd timer and crontab scheduling.
# Use "systemctl --user is-enabled glm.timer" and "crontab -l" to check.

[Install]
WantedBy=timers.target
`
