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
	Short: "Install glm binary to ~/.local/bin",
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		destDir := filepath.Join(home, ".local", "bin")
		destPath := filepath.Join(destDir, "glm")

		if err := os.MkdirAll(destDir, 0755); err != nil {
			fmt.Printf("Error creating directory: %v\n", err)
			os.Exit(1)
		}

		srcPath, err := os.Executable()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if err := copyFile(srcPath, destPath); err != nil {
			fmt.Printf("Error copying binary: %v\n", err)
			os.Exit(1)
		}

		if err := os.Chmod(destPath, 0755); err != nil {
			fmt.Printf("Error setting permissions: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Installed to %s\n", destPath)
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
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
