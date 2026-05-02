package main

import (
	"os"
	"os/exec"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/xihale/glm/cmd"
)

func init() {
	tz := detectTimezone()
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			time.Local = loc
		}
	}
}

func detectTimezone() string {
	// 1. TZ environment variable (standard)
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	// 2. /etc/TZ (common on Android/Termux)
	if data, err := os.ReadFile("/etc/TZ"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	// 3. getprop persist.sys.timezone (Android)
	if out, err := exec.Command("getprop", "persist.sys.timezone").Output(); err == nil {
		if tz := strings.TrimSpace(string(out)); tz != "" {
			return tz
		}
	}
	return ""
}

func main() {
	cmd.Execute()
}
