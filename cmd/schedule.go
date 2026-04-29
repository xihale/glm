package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage activation schedule",
	Long: `Manage the daily activation schedule.

Set one or more daily activation times with a UTC offset, and the values are
stored with the timezone you specify. Bare offsets like +8 are accepted.`,
}

var scheduleSetCmd = &cobra.Command{
	Use:   "set <UTC+X|+X> <time> [time...]",
	Short: "Set activation schedule",
	Long: `Set the daily activation schedule with a UTC offset and one or more times.

The times are stored as entered and interpreted at runtime using the provided
UTC+X timezone. Bare offsets like +8 are accepted.

Example:
  glm schedule set +8 0:0 12:0 18:30`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		zoneSpec := args[0]
		timeStrs := args[1:]

		if _, err := parseScheduleLocation(zoneSpec); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", zoneSpec, err)
		}

		// Parse and normalize times without converting them.
		var times []string
		for _, t := range timeStrs {
			normalized, err := normalizeTime(t)
			if err != nil {
				return fmt.Errorf("invalid time %q: %w", t, err)
			}
			times = append(times, normalized)
		}

		// Sort times.
		sort.Strings(times)

		config.Current.Schedule = config.ScheduleConfig{
			Timezone: zoneSpec,
			Times:    times,
		}
		viper.Set("schedule", config.Current.Schedule)
		if err := config.SaveConfig(); err != nil {
			return fmt.Errorf("error saving config: %w", err)
		}

		fmt.Printf("\033[32m[+] Schedule set:\033[0m\n")
		fmt.Printf("    Timezone: %s\n", zoneSpec)
		fmt.Printf("    Times:    %s\n", strings.Join(times, ", "))
		return nil
	},
}

var scheduleShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current schedule",
	Run: func(cmd *cobra.Command, args []string) {
		if config.Current.Schedule.IsEmpty() {
			fmt.Println("No schedule configured. Use 'glm schedule set' to set one.")
			return
		}

		fmt.Printf("Timezone: %s\n", config.Current.Schedule.Timezone)
		fmt.Printf("Times:    %s\n", strings.Join(config.Current.Schedule.Times, ", "))
	},
}

var scheduleClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove activation schedule",
	Run: func(cmd *cobra.Command, args []string) {
		if config.Current.Schedule.IsEmpty() {
			fmt.Println("No schedule configured.")
			return
		}

		config.Current.Schedule = config.ScheduleConfig{}
		viper.Set("schedule", config.Current.Schedule)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("\033[31m[-] Error saving config: %v\033[0m\n", err)
			return
		}
		fmt.Println("\033[32m[+] Schedule cleared.\033[0m")
	},
}

func init() {
	rootCmd.AddCommand(scheduleCmd)
	scheduleCmd.AddCommand(scheduleSetCmd)
	scheduleCmd.AddCommand(scheduleShowCmd)
	scheduleCmd.AddCommand(scheduleClearCmd)
}

// utcScheduleZone is the stored timezone for converted schedule entries.
const utcScheduleZone = "UTC"

// parseScheduleLocation parses UTC+X offsets and falls back to IANA timezones.
func parseScheduleLocation(spec string) (*time.Location, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("timezone is required")
	}

	if isUTCOffsetSpec(spec) {
		offset, err := parseUTCTimeOffset(spec)
		if err != nil {
			return nil, err
		}
		return time.FixedZone(formatUTCOffsetLabel(offset), offset), nil
	}

	if loc, err := time.LoadLocation(spec); err == nil {
		return loc, nil
	}

	return nil, fmt.Errorf("must be an offset like +8 or UTC+8, or a valid IANA timezone")
}

func parseUTCTimeOffset(spec string) (int, error) {
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
		return 0, fmt.Errorf("expected an offset like +8 or UTC+8")
	}

	parts := strings.Split(upper, ":")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, fmt.Errorf("expected offset like +8 or +8:30")
	}

	hours, err := parseRange(parts[0], 0, 23)
	if err != nil {
		return 0, fmt.Errorf("invalid UTC hour: %w", err)
	}

	minutes := 0
	if len(parts) == 2 {
		minutes, err = parseRange(parts[1], 0, 59)
		if err != nil {
			return 0, fmt.Errorf("invalid UTC minute: %w", err)
		}
	}

	return sign * ((hours * 3600) + (minutes * 60)), nil
}

func formatUTCOffsetLabel(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return utcScheduleZone
	}

	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}

	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	if minutes == 0 {
		return fmt.Sprintf("UTC%s%d", sign, hours)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, hours, minutes)
}

func normalizeTimeToUTC(input string, loc *time.Location) (string, error) {
	normalized, err := normalizeTime(input)
	if err != nil {
		return "", err
	}

	parsed, err := time.ParseInLocation("15:04:05", normalized, loc)
	if err != nil {
		return "", err
	}

	return parsed.In(time.UTC).Format("15:04:05"), nil
}

// normalizeTime converts short time forms to HH:MM:SS.
// "5" → "05:00:00", "5:30" → "05:30:00", "5:30:0" → "05:30:00", "12:00:00" → "12:00:00"
func normalizeTime(s string) (string, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		h, err := parseHour(parts[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%02d:00:00", h), nil
	case 2:
		h, err := parseHour(parts[0])
		if err != nil {
			return "", err
		}
		m, err := parseRange(parts[1], 0, 59)
		if err != nil {
			return "", fmt.Errorf("invalid minute")
		}
		return fmt.Sprintf("%02d:%02d:00", h, m), nil
	case 3:
		h, err := parseHour(parts[0])
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

func parseHour(s string) (int, error) {
	return parseRange(s, 0, 23)
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

func isUTCOffsetSpec(spec string) bool {
	upper := strings.ToUpper(strings.TrimSpace(spec))
	if upper == "UTC" || upper == "UTC+0" || upper == "UTC+00" || upper == "UTC+00:00" || upper == "UTC-0" || upper == "UTC-00" || upper == "UTC-00:00" {
		return true
	}
	return strings.HasPrefix(upper, "UTC+") || strings.HasPrefix(upper, "UTC-") || strings.HasPrefix(upper, "+") || strings.HasPrefix(upper, "-")
}
