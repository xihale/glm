package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/ui"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Print the shell completion script",
	Long: `Pass a shell name to print the raw completion script to stdout:
  glm completion zsh > "${fpath[1]}/_glm"

Or let glm place the file for you (detects $SHELL):
  glm completion install`,
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return printCompletionScript(args[0])
		}
		return cmd.Help()
	},
}

var completionInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install completion for the current shell ($SHELL)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := filepath.Base(os.Getenv("SHELL"))
		switch shell {
		case "bash", "zsh", "fish":
			return installCompletion(shell)
		default:
			return fmt.Errorf("cannot auto-install for shell %q — print the script with `glm completion <shell>`", shell)
		}
	},
}

func completionScript(shell string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = rootCmd.GenBashCompletionV2(&buf, true)
	case "zsh":
		err = rootCmd.GenZshCompletion(&buf)
	case "fish":
		err = rootCmd.GenFishCompletion(&buf, true)
	case "powershell":
		err = rootCmd.GenPowerShellCompletionWithDesc(&buf)
	default:
		return nil, fmt.Errorf("unknown shell %q (use bash, zsh, fish, or powershell)", shell)
	}
	return buf.Bytes(), err
}

func printCompletionScript(shell string) error {
	script, err := completionScript(shell)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(script)
	return err
}

func installCompletion(shell string) error {
	var path, hint string
	var err error
	switch shell {
	case "zsh":
		path, hint, err = installZsh()
	case "bash":
		path, err = installUserFile(".local/share/bash-completion/completions/glm", shell)
	case "fish":
		path, err = installUserFile(".config/fish/completions/glm.fish", shell)
	}
	if err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Installed %s completion: %s", shell, path))
	if hint != "" {
		ui.Warn(hint)
	}
	return nil
}

func installZsh() (path, hint string, err error) {
	home, _ := os.UserHomeDir()
	// /usr/local/share/zsh/site-functions sits in the stock fpath but is
	// root-owned; the user dir is writable yet needs a manual fpath entry.
	for i, dir := range []string{
		"/usr/local/share/zsh/site-functions",
		filepath.Join(home, ".local/share/zsh/site-functions"),
	} {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			continue
		}
		p := filepath.Join(dir, "_glm")
		script, genErr := completionScript("zsh")
		if genErr != nil {
			return "", "", genErr
		}
		if writeErr := os.WriteFile(p, script, 0o644); writeErr != nil {
			continue
		}
		if i == 0 {
			return p, "", nil
		}
		return p, "dir is outside the default fpath — rerun with sudo to install to /usr/local/share/zsh/site-functions", nil
	}
	return "", "", fmt.Errorf("no writable zsh completion directory")
}

func installUserFile(rel string, shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, rel)
	if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
		return "", mkErr
	}
	script, err := completionScript(shell)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, script, 0o644)
}

func init() {
	rootCmd.AddCommand(completionCmd)
	completionCmd.AddCommand(completionInstallCmd)
}
