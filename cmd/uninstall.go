package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/ui"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall systemd service",
	Long:  `Stop, disable, and remove the systemd service. Config is left untouched.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, err := detectScope()
		if err != nil {
			return err
		}
		// Under sudo (system scope), fix HOME so the schedule-clear targets the
		// real user's config rather than /root.
		if scope == scopeSystem {
			if err := reinitUnderSudo(); err != nil {
				return err
			}
		}

		dir := systemdUnitDir(scope)
		serviceFile := filepath.Join(dir, serviceUnitName())

		// Stop service
		if err := systemctl(scope, "disable", "--now", serviceUnitName()); err != nil {
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
			if err := systemctl(scope, "daemon-reload"); err != nil {
				ui.Warn(fmt.Sprintf("Daemon reload: %v", err))
			}
		}

		ui.Success(fmt.Sprintf("Uninstalled %s service (%d unit(s) removed)", scope, removed))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
