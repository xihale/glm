package utils

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	CronIdentifier   = "# glm Scheduled Task"
	RebootIdentifier = "# glm Boot Recovery"
)

// isNoCrontabError returns true if the error from "crontab -l" indicates
// that the user simply has no crontab (as opposed to a real system error).
func isNoCrontabError(err error, output []byte) bool {
	if err == nil {
		return false
	}
	// "crontab -l" returns exit code 1 with "no crontab for <user>" when empty.
	// This is a normal condition, not a real system error.
	msg := strings.ToLower(err.Error() + " " + string(output))
	return strings.Contains(msg, "no crontab")
}

func readCrontabLines() ([]string, error) {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if isNoCrontabError(err, out) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read crontab: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.Split(string(out), "\n"), nil
}

func HasScheduledRun() (bool, error) {
	lines, err := readCrontabLines()
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, CronIdentifier) || strings.Contains(trimmed, RebootIdentifier) {
			return true, nil
		}
	}
	return false, nil
}

func ScheduleNextRun(nextRun time.Time, execPath string, configPath string) error {
	lines, err := readCrontabLines()
	if err != nil {
		return err
	}

	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, CronIdentifier) || strings.Contains(trimmed, RebootIdentifier) {
			continue
		}
		newLines = append(newLines, line)
	}

	if !nextRun.IsZero() {
		local := nextRun.Local()
		cronTime := fmt.Sprintf("%d %d %d %d *",
			local.Minute(),
			local.Hour(),
			local.Day(),
			int(local.Month()))

		taskLine := fmt.Sprintf("%s %s daemon --scheduler cron --config %s %s",
			cronTime,
			execPath,
			configPath,
			CronIdentifier)
		newLines = append(newLines, taskLine)

		rebootLine := fmt.Sprintf("@reboot %s daemon --scheduler cron --config %s %s",
			execPath,
			configPath,
			RebootIdentifier)
		newLines = append(newLines, rebootLine)
	}

	newCrontab := strings.Join(newLines, "\n")
	if !strings.HasSuffix(newCrontab, "\n") {
		newCrontab += "\n"
	}

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newCrontab)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("crontab update failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func RemoveScheduledRun() error {
	return ScheduleNextRun(time.Time{}, "", "")
}
