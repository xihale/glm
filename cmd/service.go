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
		if err := installService(); err != nil {
			fmt.Printf("Error installing service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service installed successfully.")
		fmt.Println("Run 'systemctl --user start ai-daemon' to start it.")
		fmt.Println("Run 'systemctl --user enable ai-daemon' to start on login.")
	},
}

var uninstallServiceCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall systemd user service",
	Run: func(cmd *cobra.Command, args []string) {
		if err := uninstallService(); err != nil {
			fmt.Printf("Error uninstalling service: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service uninstalled successfully.")
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.AddCommand(installServiceCmd)
	serviceCmd.AddCommand(uninstallServiceCmd)
}

const serviceTemplate = `[Unit]
Description=AI Daemon Heartbeat Service
After=network.target

[Service]
ExecStart={{.ExecPath}} daemon
Restart=always
RestartSec=60
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`

func installService() error {
	home, _ := os.UserHomeDir()
	execPath := filepath.Join(home, ".local", "bin", "ai-daemon")

	if _, err := os.Stat(execPath); os.IsNotExist(err) {
		return fmt.Errorf("ai-daemon is not installed in %s. Please run 'ai-daemon install' first", execPath)
	}

	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return err
	}

	serviceFile := filepath.Join(serviceDir, "ai-daemon.service")

	// Check if exists
	if _, err := os.Stat(serviceFile); err == nil {
		fmt.Println("Service file already exists, overwriting...")
	}

	f, err := os.Create(serviceFile)
	if err != nil {
		return err
	}
	defer f.Close()

	tmpl, err := template.New("service").Parse(serviceTemplate)
	if err != nil {
		return err
	}

	data := struct {
		ExecPath string
	}{
		ExecPath: execPath,
	}

	if err := tmpl.Execute(f, data); err != nil {
		return err
	}

	// Reload daemon
	// We can't easily call systemctl from here portably/reliably without exec
	// but we can instruct user.
	fmt.Println("Created:", serviceFile)
	fmt.Println("Please run: systemctl --user daemon-reload")

	return nil
}

func uninstallService() error {
	home, _ := os.UserHomeDir()
	serviceFile := filepath.Join(home, ".config", "systemd", "user", "ai-daemon.service")

	if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
		return fmt.Errorf("service file not found: %s", serviceFile)
	}

	if err := os.Remove(serviceFile); err != nil {
		return err
	}

	fmt.Println("Removed:", serviceFile)
	fmt.Println("Please run: systemctl --user daemon-reload")
	return nil
}
