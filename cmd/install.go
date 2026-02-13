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
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	destDir := filepath.Join(home, ".local", "bin")
	destPath := filepath.Join(destDir, "ai-daemon")

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	srcPath, err := os.Executable()
	if err != nil {
		return err
	}

	if err := copyFile(srcPath, destPath); err != nil {
		return err
	}

	if err := os.Chmod(destPath, 0755); err != nil {
		return err
	}

	fmt.Printf("Successfully installed ai-daemon to %s\n", destPath)
	fmt.Println("Please ensure ~/.local/bin is in your PATH.")
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
