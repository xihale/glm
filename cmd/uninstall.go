package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall systemd timer",
	Long:  `Stop, disable, and remove the systemd user timer. Clears the schedule from config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := systemdUnitDir()
		serviceFile := filepath.Join(dir, serviceUnit)
		timerFile := filepath.Join(dir, timerUnit)

		// Stop units
		if err := systemctlUser("disable", "--now", timerUnit); err != nil {
			if !strings.Contains(err.Error(), "not loaded") {
				ui.Warn(fmt.Sprintf("Stop timer: %v", err))
			}
		}
		if err := systemctlUser("disable", "--now", serviceUnit); err != nil {
			if !strings.Contains(err.Error(), "not loaded") {
				ui.Warn(fmt.Sprintf("Stop service: %v", err))
			}
		}

		// Remove unit files
		removed := 0
		for _, path := range []string{serviceFile, timerFile} {
			if _, err := os.Stat(path); err == nil {
				if err := os.Remove(path); err != nil {
					ui.Warn(fmt.Sprintf("Remove %s: %v", filepath.Base(path), err))
				} else {
					removed++
				}
			}
		}

		// Daemon reload
		if removed > 0 {
			if err := systemctlUser("daemon-reload"); err != nil {
				ui.Warn(fmt.Sprintf("Daemon reload: %v", err))
			}
		}

		// Clear schedule from config
		config.Current.Schedule = config.ScheduleConfig{}
		viper.Set("schedule", config.Current.Schedule)
		if err := config.SaveConfig(); err != nil {
			ui.Warn(fmt.Sprintf("Save config: %v", err))
		}

		ui.Success(fmt.Sprintf("Uninstalled (%d unit(s) removed)", removed))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
