package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install ai-daemon binary to ~/.local/bin",
	Run: func(cmd *cobra.Command, args []string) {
		if err := runInstall(); err != nil {
			fmt.Printf("Error during installation: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall() error {
	fmt.Printf("\n\033[1;36mInstalling AI-Daemon to Local Path\033[0m\n")
	fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	destDir := filepath.Join(home, ".local", "bin")
	destPath := filepath.Join(destDir, "ai-daemon")

	fmt.Printf("  [*] Preparing directory: %s ... ", destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
		return err
	}
	fmt.Printf("\033[32m[+] Done\033[0m\n")

	srcPath, err := os.Executable()
	if err != nil {
		return err
	}

	fmt.Printf("  [*] Copying binary to: %s ... ", destPath)
	if err := copyFile(srcPath, destPath); err != nil {
		fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
		return err
	}
	fmt.Printf("\033[32m[+] Done\033[0m\n")

	fmt.Printf("  [*] Setting executable permissions ... ")
	if err := os.Chmod(destPath, 0755); err != nil {
		fmt.Printf("\033[31m[-] Error: %v\033[0m\n", err)
		return err
	}
	fmt.Printf("\033[32m[+] Done\033[0m\n")

	fmt.Printf("\n\033[32m[✔] Successfully installed ai-daemon to %s\033[0m\n", destPath)
	fmt.Println("\033[34m[!] Please ensure ~/.local/bin is in your $PATH.\033[0m\n")
	return nil
}

func copyFile(src, dst string) error {
	if src == dst {
		return nil
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}
