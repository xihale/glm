package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
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
	Short: "Install systemd service for scheduled activation",
	Long: `Install a systemd service for GLM quota activation.

The service runs as a self-driven daemon: activate, sleep until next run, repeat.

By default it installs a SYSTEM service to /etc/systemd/system, which starts at
boot and keeps running regardless of logins — the right choice for servers.
A system install requires root (run via sudo); the service then runs as the
real invoking user (resolved from SUDO_USER) and reads that user's config.
Pass --user to install a user service under ~/.config/systemd/user instead
(legacy behavior; needs lingering to survive logout).

Modes:
  --auto    Auto-schedule: calculate next run from API quota reset time.
            No arguments needed.

Manual    Pass timezone and times:
            glm install +8 5:00 10:00 15:00 20:00

Timezone accepts UTC offsets like +8 or UTC+8, or IANA names like Asia/Shanghai.
Times accept H, H:M, or H:M:S format.`,
	Args: cobra.MaximumNArgs(10),
	RunE: func(cmd *cobra.Command, args []string) error {
		auto, _ := cmd.Flags().GetBool("auto")
		userFlag, _ := cmd.Flags().GetBool("user")

		scope, err := resolveScope(userFlag)
		if err != nil {
			return err
		}
		if scope == scopeSystem {
			// Under sudo, fix HOME so the real user's config is read/written
			// (else os.UserHomeDir() resolves to /root).
			if err := reinitUnderSudo(); err != nil {
				return err
			}
		}

		if auto {
			return installAuto(scope)
		}

		if len(args) < 2 {
			// No mode specified: reuse an existing valid schedule from the
			// config file (default path or --config) instead of forcing the
			// user to reconfigure.
			if !config.Current.Schedule.IsEmpty() {
				return installExisting(scope)
			}
			return fmt.Errorf("manual mode requires <timezone> <time> [time...], or use --auto")
		}

		return installManual(scope, args[0], args[1:])
	},
}

// applyInstall is the shared tail of all install paths: write the unit,
// daemon-reload, enable --now. It returns the path of the written unit file so
// the caller can echo it back to the user.
func applyInstall(scope installScope) (string, error) {
	execPath, configPath, err := servicePaths()
	if err != nil {
		return "", err
	}
	unitPath, err := installServiceUnit(scope, execPath, configPath)
	if err != nil {
		return "", err
	}
	if err := systemctl(scope, "daemon-reload"); err != nil {
		return "", err
	}
	if err := systemctl(scope, "enable", "--now", serviceUnit); err != nil {
		return "", err
	}
	return unitPath, nil
}

func installExisting(scope installScope) error {
	sched := config.Current.Schedule

	unitPath, err := applyInstall(scope)
	if err != nil {
		return err
	}

	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		configFile = config.DefaultConfigPath()
	}
	ui.Success("Detected existing schedule, using it")
	fmt.Printf("  Scope:  %s\n", ui.Accent(string(scope)))
	fmt.Printf("  Config: %s\n", ui.Accent(configFile))
	if sched.Auto {
		fmt.Printf("  Mode:   %s\n", ui.Accent("auto (self-driven daemon)"))
	} else {
		fmt.Printf("  Timezone: %s\n", ui.Accent(sched.Timezone))
		fmt.Printf("  Times:    %s\n", ui.Accent(strings.Join(sched.Times, ", ")))
	}
	fmt.Printf("  Unit:     %s\n", ui.Accent(unitPath))
	return nil
}

func installAuto(scope installScope) error {
	config.Current.Schedule = config.ScheduleConfig{
		Auto: true,
	}
	viper.Set("schedule", config.Current.Schedule)
	if err := config.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	unitPath, err := applyInstall(scope)
	if err != nil {
		return err
	}

	ui.Success("Installed auto-schedule service")
	fmt.Printf("  Scope: %s\n", ui.Accent(string(scope)))
	fmt.Printf("  Mode:  %s\n", ui.Accent("auto (self-driven daemon)"))
	fmt.Printf("  Unit:  %s\n", ui.Accent(unitPath))
	return nil
}

func installManual(scope installScope, zoneSpec string, timeStrs []string) error {
	_, err := parseTimezone(zoneSpec)
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

	unitPath, err := applyInstall(scope)
	if err != nil {
		return err
	}

	ui.Success("Installed scheduled service")
	fmt.Printf("  Scope:    %s\n", ui.Accent(string(scope)))
	fmt.Printf("  Timezone: %s\n", ui.Accent(zoneSpec))
	fmt.Printf("  Times:    %s\n", ui.Accent(strings.Join(times, ", ")))
	fmt.Printf("  Unit:     %s\n", ui.Accent(unitPath))
	return nil
}

// --- systemd ---

const serviceUnit = "glm.service"

// installScope selects which systemd bus a command targets.
type installScope string

const (
	scopeSystem installScope = "system"
	scopeUser   installScope = "user"
)

// String renders a scope for user-facing output (used by ui.Accent).
func (s installScope) String() string { return string(s) }

// resolveScope decides the install scope from the --user flag and privilege.
//
// Default is system (boot-persistent, login-independent). User is only used
// when explicitly requested, or when system install is impossible (not root).
func resolveScope(userFlag bool) (installScope, error) {
	if userFlag {
		return scopeUser, nil
	}
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("system install needs root; re-run with sudo, or use 'glm install --user'")
	}
	return scopeSystem, nil
}

// detectScope finds an already-installed unit so uninstall/reload can target
// the right systemd bus without a flag. system takes precedence over user on
// the off chance both exist.
func detectScope() (installScope, error) {
	if _, err := os.Stat(systemUnitPath(scopeSystem)); err == nil {
		return scopeSystem, nil
	}
	if _, err := os.Stat(systemUnitPath(scopeUser)); err == nil {
		return scopeUser, nil
	}
	return "", fmt.Errorf("no glm service installed; run 'glm install' first")
}

// realUser returns info for the user the service should run as under a system
// install. With sudo it prefers SUDO_USER (the invoking user); otherwise the
// current user (which, for system install, is root itself).
func realUser() (*user.User, error) {
	if name := os.Getenv("SUDO_USER"); name != "" {
		return user.Lookup(name)
	}
	return user.Current()
}

// reinitUnderSudo fixes up HOME and re-reads config so a `sudo glm install`
// writes schedule/config to the REAL user's paths instead of /root. It only
// adjusts when running under sudo; a true root shell keeps its own HOME.
func reinitUnderSudo() error {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		return nil // not running under sudo; nothing to fix
	}
	u, err := user.Lookup(sudoUser)
	if err != nil {
		return fmt.Errorf("resolve sudo user %q: %w", sudoUser, err)
	}
	if u.HomeDir != "" {
		os.Setenv("HOME", u.HomeDir)
	}
	// Re-resolve config with the corrected HOME (viper may have already
	// latched onto /root during OnInitialize).
	config.InitConfig()
	return nil
}

type unitData struct {
	ExecPath   string
	ConfigPath string
	UserLine   string // "User=foo" or "" (omit directive)
	WantedBy   string
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

func systemdUnitDir(scope installScope) string {
	switch scope {
	case scopeSystem:
		return "/etc/systemd/system"
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "systemd", "user")
	}
}

func systemUnitPath(scope installScope) string {
	return filepath.Join(systemdUnitDir(scope), serviceUnit)
}

func installServiceUnit(scope installScope, execPath, configPath string) (string, error) {
	dir := systemdUnitDir(scope)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	data := unitData{
		ExecPath:   execPath,
		ConfigPath: configPath,
		WantedBy:   "default.target",
	}
	if scope == scopeSystem {
		data.WantedBy = "multi-user.target"
		// Run the daemon as the real (non-root) user so it reads that user's
		// config. Under a true root shell (no SUDO_USER), omit User= and let
		// the service run as root.
		if os.Getenv("SUDO_USER") != "" {
			if u, err := realUser(); err == nil {
				data.UserLine = "User=" + u.Username
			}
		}
	}

	path := filepath.Join(dir, serviceUnit)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	t, err := template.New(serviceUnit).Parse(serviceTmpl)
	if err != nil {
		return "", err
	}
	if err := t.Execute(f, data); err != nil {
		return "", err
	}
	return path, nil
}

func systemctl(scope installScope, args ...string) error {
	full := args
	if scope == scopeUser {
		full = append([]string{"--user"}, args...)
	}
	cmd := exec.Command("systemctl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
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

const serviceTmpl = `[Unit]
Description=GLM Activation Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
{{if .UserLine}}{{.UserLine}}
{{end}}ExecStart={{.ExecPath}} active --service --config {{.ConfigPath}}
Restart=on-failure
RestartSec=30
StandardOutput=journal
StandardError=journal

[Install]
WantedBy={{.WantedBy}}
`

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().Bool("auto", false, "Auto-schedule: calculate next run from quota reset time")
	installCmd.Flags().Bool("user", false, "Install a user service (~/.config/systemd/user) instead of a system service")
}
