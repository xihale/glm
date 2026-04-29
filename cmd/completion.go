package cmd

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate completion script",
	Long: `To load completions:

Bash:
  $ source <(glm completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ glm completion bash > /etc/bash_completion.d/glm
  # macOS:
  $ glm completion bash > /usr/local/etc/bash_completion.d/glm

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it.  You can execute the following once:

  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ glm completion zsh > "${fpath[1]}/_glm"

  # You will need to start a new shell for this setup to take effect.

fish:
  $ glm completion fish | source

  # To load completions for each session, execute once:
  $ glm completion fish > ~/.config/fish/completions/glm.fish

PowerShell:
  PS> glm completion powershell | Out-String | Invoke-Expression

  # To load completions for each session, execute once:
  PS> glm completion powershell > glm.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	Hidden:                true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		install, _ := cmd.Flags().GetBool("install")

		switch args[0] {
		case "bash":
			if install {
				path := getCompletionPath("bash")
				if err := ensureDir(path); err != nil {
					fmt.Printf("Failed to create directory: %v\n", err)
					return
				}
				if err := cmd.Root().GenBashCompletionFile(path); err != nil {
					fmt.Printf("Failed to install completion: %v\n", err)
				} else {
					fmt.Printf("Installed bash completion to %s\n", path)
				}
			} else {
				cmd.Root().GenBashCompletion(os.Stdout)
			}
		case "zsh":
			if install {
				path := getCompletionPath("zsh")
				if err := ensureDir(path); err != nil {
					fmt.Printf("Failed to create directory: %v\n", err)
					return
				}
				if err := cmd.Root().GenZshCompletionFile(path); err != nil {
					fmt.Printf("Failed to install completion: %v\n", err)
				} else {
					fmt.Printf("Installed zsh completion to %s\n", path)
				}
			} else {
				cmd.Root().GenZshCompletion(os.Stdout)
			}
		case "fish":
			if install {
				path := getCompletionPath("fish")
				if err := ensureDir(path); err != nil {
					fmt.Printf("Failed to create directory: %v\n", err)
					return
				}
				if err := cmd.Root().GenFishCompletionFile(path, true); err != nil {
					fmt.Printf("Failed to install completion: %v\n", err)
				} else {
					fmt.Printf("Installed fish completion to %s\n", path)
				}
			} else {
				cmd.Root().GenFishCompletion(os.Stdout, true)
			}
		case "powershell":
			if install {
				path := getCompletionPath("powershell")
				if err := ensureDir(path); err != nil {
					fmt.Printf("Failed to create directory: %v\n", err)
					return
				}
				if err := cmd.Root().GenPowerShellCompletionFile(path); err != nil {
					fmt.Printf("Failed to install completion: %v\n", err)
				} else {
					fmt.Printf("Installed powershell completion to %s\n", path)
				}
			} else {
				cmd.Root().GenPowerShellCompletion(os.Stdout)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
	completionCmd.Flags().BoolP("install", "i", false, "Automatically install completion script")
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755)
}

func getCompletionPath(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		usr, _ := user.Current()
		home = usr.HomeDir
	}

	switch shell {
	case "bash":
		xdgData := os.Getenv("XDG_DATA_HOME")
		if xdgData == "" {
			xdgData = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(xdgData, "bash-completion", "completions", "glm")
	case "zsh":
		return filepath.Join(home, ".zfunc", "_glm")
	case "fish":
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(home, ".config")
		}
		return filepath.Join(xdgConfig, "fish", "completions", "glm.fish")
	case "powershell":
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(home, ".config")
		}
		return filepath.Join(xdgConfig, "powershell", "glm.ps1")
	}
	return ""
}
