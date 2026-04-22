package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// ResetBuffer is the delay after a quota reset time before we attempt activation.
	// The server-side reset may not take effect at the exact second, so we wait a bit.
	ResetBuffer = 5 * time.Second
	// ScheduleExtraDelay is the additional delay beyond ResetBuffer when scheduling
	// the next daemon run, to ensure the reset has fully propagated.
	ScheduleExtraDelay = 1 * time.Minute
)

func FormatTimeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "Passed"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return "0m"
}

func FormatActivationError(err error, debug bool) {
	errStr := err.Error()
	if strings.Contains(errStr, "429") {
		if strings.Contains(errStr, "reset after") {
			re := regexp.MustCompile(`reset after ([\w.]+)`)
			match := re.FindStringSubmatch(errStr)
			if len(match) > 1 {
				fmt.Printf("\033[31m[-] Exhausted (reset after %s)\033[0m\n", match[1])
			} else {
				fmt.Println("\033[31m[-] Exhausted\033[0m")
			}
		} else {
			fmt.Println("\033[31m[-] Busy (429)\033[0m")
		}
		if debug {
			fmt.Printf("      \033[31m[DEBUG] %v\033[0m\n", err)
		}
	} else {
		fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
	}
}
