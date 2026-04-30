package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var installCmd = &cobra.Command{
	Use:   "install [timezone] [time...]",
	Short: "Install systemd timer for scheduled activation",
	Long: `Install systemd user timer for scheduled GLM quota activation.

Modes:
  --auto    Auto-schedule: active calculates next run from quota reset time.
            No arguments needed.

  Manual    Pass timezone and times:
            glm install +8 5:00 10:00 15:00 20:00

Timezone accepts UTC offsets like +8 or UTC+8, or IANA names like Asia/Shanghai.
Times accept H, H:M, or H:M:S format.`,
	Args: cobra.MaximumNArgs(10),
	RunE: func(cmd *cobra.Command, args []string) error {
		auto, _ := cmd.Flags().GetBool("auto")

		if auto {
			return installAuto()
		}

		if len(args) < 2 {
			return fmt.Errorf("manual mode requires <timezone> <time> [time...], or use --auto")
		}

		return installManual(args[0], args[1:])
	},
}

func installAuto() error {
	// Save auto schedule to config
	config.Current.Schedule = config.ScheduleConfig{
		Auto: true,
	}
	viper.Set("schedule", config.Current.Schedule)
	if err := config.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Install systemd units with a placeholder timer (will be updated by active)
	execPath, configPath, err := servicePaths()
	if err != nil {
		return err
	}

	// Initial timer: run active once after boot
	timerSpec := "OnBootSec=1min"
	if err := installSystemdUnits(execPath, configPath, timerSpec); err != nil {
		return err
	}
	if err := systemctlUser("daemon-reload"); err != nil {
		return err
	}
	if err := systemctlUser("enable", timerUnit); err != nil {
		return err
	}
	if err := systemctlUser("restart", timerUnit); err != nil {
		return err
	}

	ui.Success("Installed auto-schedule timer")
	fmt.Printf("  Mode: %s\n", ui.Accent("auto (calculates next run after each activation)"))
	fmt.Printf("  Unit: %s\n", ui.Accent(timerUnit))
	return nil
}

func installManual(zoneSpec string, timeStrs []string) error {
	loc, err := parseTimezone(zoneSpec)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", zoneSpec, err)
	}

	var times []string
	for _, t := range timeStrs {
		normalized, err := normalizeTime(t)
		if err != nil {
			return fmt.Errorf("invalid time %q: %w", t, err)
		}
		times = append(times, normalized)
	}
	sort.Strings(times)

	config.Current.Schedule = config.ScheduleConfig{
		Timezone: zoneSpec,
		Times:    times,
	}
	viper.Set("schedule", config.Current.Schedule)
	if err := config.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	execPath, configPath, err := servicePaths()
	if err != nil {
		return err
	}

	timerSpec, err := buildOnCalendarSpec(loc, times)
	if err != nil {
		return err
	}

	if err := installSystemdUnits(execPath, configPath, timerSpec); err != nil {
		return err
	}
	if err := systemctlUser("daemon-reload"); err != nil {
		return err
	}
	if err := systemctlUser("enable", timerUnit); err != nil {
		return err
	}
	if err := systemctlUser("restart", timerUnit); err != nil {
		return err
	}

	ui.Success("Installed systemd timer")
	fmt.Printf("  Timezone: %s\n", ui.Accent(zoneSpec))
	fmt.Printf("  Times:    %s\n", ui.Accent(strings.Join(times, ", ")))
	fmt.Printf("  Unit:     %s\n", ui.Accent(timerUnit))
	return nil
}

// --- systemd unit management ---

const (
	serviceUnit = "glm.service"
	timerUnit   = "glm.timer"
)

type unitData struct {
	ExecPath   string
	ConfigPath string
	TimerSpec  string
}

func servicePaths() (execPath, configPath string, err error) {
	execPath, err = os.Executable()
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		resolved = execPath
	}
	return resolved, config.DefaultConfigPath(), nil
}

func systemdUnitDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func buildOnCalendarSpec(loc *time.Location, times []string) (string, error) {
	var lines []string
	lines = append(lines, "RandomizedDelaySec=0")

	for _, t := range times {
		parts := strings.Split(t, ":")
		if len(parts) == 3 {
			lines = append(lines, fmt.Sprintf("OnCalendar=*-*-* %s:%s:%s %s",
				parts[0], parts[1], parts[2], loc.String()))
		}
	}

	lines = append(lines, "OnBootSec=30")
	return strings.Join(lines, "\n"), nil
}

func installSystemdUnits(execPath, configPath, timerSpec string) error {
	dir := systemdUnitDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data := unitData{
		ExecPath:   execPath,
		ConfigPath: configPath,
		TimerSpec:  timerSpec,
	}

	serviceFile := filepath.Join(dir, serviceUnit)
	timerFile := filepath.Join(dir, timerUnit)

	if err := writeUnitFile(serviceFile, serviceTmpl, data); err != nil {
		return err
	}
	return writeUnitFile(timerFile, timerTmpl, data)
}

// RescheduleTimer rewrites the timer unit with a single OnCalendar for nextRun
// and restarts the timer. Used by active --service in auto mode.
func RescheduleTimer(nextRun time.Time) error {
	execPath, configPath, err := servicePaths()
	if err != nil {
		return err
	}

	// Single OnCalendar with exact date-time
	spec := fmt.Sprintf("OnCalendar=%s\nPersistent=true", nextRun.Format("2006-01-02 15:04:05"))

	dir := systemdUnitDir()
	data := unitData{
		ExecPath:   execPath,
		ConfigPath: configPath,
		TimerSpec:  spec,
	}

	timerFile := filepath.Join(dir, timerUnit)
	if err := writeUnitFile(timerFile, timerTmpl, data); err != nil {
		return err
	}

	if err := systemctlUser("daemon-reload"); err != nil {
		return err
	}
	return systemctlUser("restart", timerUnit)
}

func writeUnitFile(path, tmpl string, data unitData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	t, err := template.New(filepath.Base(path)).Parse(tmpl)
	if err != nil {
		return err
	}
	return t.Execute(f, data)
}

func systemctlUser(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- time parsing ---

func parseTimezone(spec string) (*time.Location, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("timezone is required")
	}

	if isUTCOffset(spec) {
		offset, err := parseUTCOffset(spec)
		if err != nil {
			return nil, err
		}
		return time.FixedZone(fmtOffsetLabel(offset), offset), nil
	}

	if loc, err := time.LoadLocation(spec); err == nil {
		return loc, nil
	}
	return nil, fmt.Errorf("must be an offset like +8 or a valid IANA timezone")
}

func isUTCOffset(spec string) bool {
	upper := strings.ToUpper(strings.TrimSpace(spec))
	if upper == "UTC" || strings.HasPrefix(upper, "UTC+") || strings.HasPrefix(upper, "UTC-") {
		return true
	}
	return strings.HasPrefix(upper, "+") || strings.HasPrefix(upper, "-")
}

func parseUTCOffset(spec string) (int, error) {
	upper := strings.ToUpper(strings.TrimSpace(spec))
	if upper == "UTC" {
		return 0, nil
	}
	if strings.HasPrefix(upper, "UTC") {
		upper = strings.TrimSpace(upper[3:])
	}
	if upper == "" {
		return 0, fmt.Errorf("timezone is required")
	}

	sign := 1
	switch upper[0] {
	case '+':
		upper = upper[1:]
	case '-':
		sign = -1
		upper = upper[1:]
	default:
		return 0, fmt.Errorf("expected an offset like +8")
	}

	parts := strings.Split(upper, ":")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, fmt.Errorf("expected offset like +8 or +8:30")
	}

	hours, err := parseRange(parts[0], 0, 23)
	if err != nil {
		return 0, err
	}

	minutes := 0
	if len(parts) == 2 {
		minutes, err = parseRange(parts[1], 0, 59)
		if err != nil {
			return 0, err
		}
	}

	return sign*((hours*3600)+(minutes*60)), nil
}

func fmtOffsetLabel(offset int) string {
	if offset == 0 {
		return "UTC"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	h := offset / 3600
	m := (offset % 3600) / 60
	if m == 0 {
		return fmt.Sprintf("UTC%s%d", sign, h)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
}

func normalizeTime(s string) (string, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		h, err := parseRange(parts[0], 0, 23)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%02d:00:00", h), nil
	case 2:
		h, err := parseRange(parts[0], 0, 23)
		if err != nil {
			return "", err
		}
		m, err := parseRange(parts[1], 0, 59)
		if err != nil {
			return "", fmt.Errorf("invalid minute")
		}
		return fmt.Sprintf("%02d:%02d:00", h, m), nil
	case 3:
		h, err := parseRange(parts[0], 0, 23)
		if err != nil {
			return "", err
		}
		m, err := parseRange(parts[1], 0, 59)
		if err != nil {
			return "", fmt.Errorf("invalid minute")
		}
		sec, err := parseRange(parts[2], 0, 59)
		if err != nil {
			return "", fmt.Errorf("invalid second")
		}
		return fmt.Sprintf("%02d:%02d:%02d", h, m, sec), nil
	default:
		return "", fmt.Errorf("expected H, H:M, or H:M:S format")
	}
}

func parseRange(s string, min, max int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("not a number")
	}
	if v < min || v > max {
		return 0, fmt.Errorf("must be between %d and %d", min, max)
	}
	return v, nil
}

// --- systemd unit templates ---

const serviceTmpl = `[Unit]
Description=GLM Activation Service
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart={{.ExecPath}} active --service --config {{.ConfigPath}}
StandardOutput=journal
StandardError=journal
`

const timerTmpl = `[Unit]
Description=GLM Activation Timer

[Timer]
Unit=glm.service
{{.TimerSpec}}
Persistent=true
AccuracySec=1s

[Install]
WantedBy=timers.target
`

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().Bool("auto", false, "Auto-schedule: calculate next run from quota reset time")
}
