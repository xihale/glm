package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage systemd service",
}

var installServiceCmd = &cobra.Command{
	Use:   "install",
	Short: "Install systemd user service",
	Run: func(cmd *cobra.Command, args []string) {
		home, _ := os.UserHomeDir()
		execPath, err := os.Executable()
		if err != nil {
			fmt.Printf("Error: cannot determine current executable: %v\n", err)
			os.Exit(1)
		}
		resolved, err := filepath.EvalSymlinks(execPath)
		if err != nil {
			resolved = execPath
		}
		execPath = resolved

		serviceDir := filepath.Join(home, ".config", "systemd", "user")
		if err := os.MkdirAll(serviceDir, 0755); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		serviceFile := filepath.Join(serviceDir, "glm.service")

		f, err := os.Create(serviceFile)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		tmpl, _ := template.New("service").Parse(serviceTemplate)
		data := struct{ ExecPath string }{ExecPath: execPath}
		if err := tmpl.Execute(f, data); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Service installed to %s\n", serviceFile)
		fmt.Println("Run: systemctl --user daemon-reload")
		fmt.Println("     systemctl --user start glm")
	},
}

var uninstallServiceCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall systemd user service",
	Run: func(cmd *cobra.Command, args []string) {
		home, _ := os.UserHomeDir()
		serviceFile := filepath.Join(home, ".config", "systemd", "user", "glm.service")

		if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
			fmt.Printf("Service file not found: %s\n", serviceFile)
			os.Exit(1)
		}

		if err := os.Remove(serviceFile); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Removed %s\n", serviceFile)
		fmt.Println("Run: systemctl --user daemon-reload")
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.AddCommand(installServiceCmd)
	serviceCmd.AddCommand(uninstallServiceCmd)
}

const serviceTemplate = `[Unit]
Description=GLM Heartbeat Service
After=network.target

[Service]
ExecStart={{.ExecPath}} daemon --config %h/.config/glm/config.yaml
Restart=always
RestartSec=60
StandardOutput=journal
StandardError=journal

# Note: Do not enable both systemd service and crontab scheduling.
# Use "systemctl --user is-enabled glm" and "crontab -l" to check.

[Install]
WantedBy=default.target
`
