package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/ui"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload daemon config without restarting",
	Long: `Send SIGHUP to the running systemd GLM daemon so it re-reads
config.yaml and recomputes the next activation time.

Only affects the systemd-managed daemon (installed via 'glm install').
For ad-hoc runs, send SIGHUP manually: kill -HUP <pid>.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, err := detectScope()
		if err != nil {
			return err
		}
		if err := systemctl(scope, "kill", "--signal=SIGHUP", serviceUnitName()); err != nil {
			return fmt.Errorf("reload %s (%s): %w\n(Is the service installed and running? Run 'glm install' first.)", serviceUnitName(), scope, err)
		}
		ui.Success(fmt.Sprintf("Sent reload signal to %s (%s)", serviceUnitName(), scope))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}
