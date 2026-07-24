package cmd

import (
	"fmt"

	"github.com/xihale/glm/pkg/ui"
	"github.com/spf13/cobra"
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
		if err := systemctlUser("kill", "--signal=SIGHUP", serviceUnit); err != nil {
			return fmt.Errorf("reload %s: %w\n(Is the service installed and running? Run 'glm install' first.)", serviceUnit, err)
		}
		ui.Success(fmt.Sprintf("Sent reload signal to %s", serviceUnit))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}
