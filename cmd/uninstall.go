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
	Short: "Uninstall systemd service",
	Long:  `Stop, disable, and remove the systemd user service. Clears the schedule from config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := systemdUnitDir()
		serviceFile := filepath.Join(dir, serviceUnit)

		// Stop service
		if err := systemctlUser("disable", "--now", serviceUnit); err != nil {
			if !strings.Contains(err.Error(), "not loaded") {
				ui.Warn(fmt.Sprintf("Stop service: %v", err))
			}
		}

		// Remove unit file
		removed := 0
		if _, err := os.Stat(serviceFile); err == nil {
			if err := os.Remove(serviceFile); err != nil {
				ui.Warn(fmt.Sprintf("Remove %s: %v", filepath.Base(serviceFile), err))
			} else {
				removed++
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
