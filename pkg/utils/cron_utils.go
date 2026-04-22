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

func ScheduleNextRun(nextRun time.Time, execPath string, configPath string) error {
	out, err := exec.Command("crontab", "-l").Output()
	var lines []string
	if err == nil {
		lines = strings.Split(string(out), "\n")
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

		taskLine := fmt.Sprintf("%s %s daemon --config %s %s",
			cronTime,
			execPath,
			configPath,
			CronIdentifier)
		newLines = append(newLines, taskLine)

		rebootLine := fmt.Sprintf("@reboot %s daemon --config %s %s",
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
