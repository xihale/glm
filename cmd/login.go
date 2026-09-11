package cmd

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/agy"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/ui"
	"golang.org/x/term"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Set credentials (GLM API key or Antigravity refresh token)",
	Long: `Set or update the provider credentials.

glm provider (default):
  glm login                  # interactive prompt for the GLM API key
  glm login -k <api-key>

agy provider (Google Antigravity / Gemini):
  glm login --provider agy            # auto-import the refresh token from
                                      # the local agy CLI login, or prompt
  glm login --provider agy -k <refresh-token>   # manual, e.g. on a server
  glm login --provider agy --pool 3p  # anchor the Claude/GPT pool instead

The agy refresh token never rotates: it is read once and stored in the
config; access tokens are refreshed automatically afterwards.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.EffectiveProvider() == "agy" {
			return loginAGY()
		}
		return loginGLM()
	},
}

func loginGLM() error {
	if apiKeyFlag != "" {
		config.Current.APIKey = apiKeyFlag
	} else {
		// Interactive prompt
		if config.Current.APIKey != "" {
			masked := config.Current.APIKey
			if len(masked) > 8 {
				masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
			}
			fmt.Printf("  Current: %s\n", ui.Dimmed(masked))
		}

		fmt.Printf("%s API key: ", ui.Style("?", ui.Cyan, ui.Bold))
		bytePassword, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fmt.Println()
			return fmt.Errorf("read API key: %w", err)
		}
		fmt.Println()

		key := strings.TrimSpace(string(bytePassword))
		if key == "" {
			return fmt.Errorf("API key cannot be empty")
		}
		config.Current.APIKey = key
	}

	if err := config.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	ui.Success("API key saved")
	return nil
}

func loginAGY() error {
	agyCfg := config.Current.AGY

	if poolFlag != "" {
		if poolFlag != agy.PoolGemini && poolFlag != agy.Pool3P {
			return fmt.Errorf("invalid pool %q (use %q or %q)", poolFlag, agy.PoolGemini, agy.Pool3P)
		}
		agyCfg.Pool = poolFlag
	}
	if projectFlag != "" {
		agyCfg.Project = projectFlag
	}

	switch {
	case apiKeyFlag != "":
		agyCfg.RefreshToken = strings.TrimSpace(apiKeyFlag)
		agyCfg.TokenFile = "" // manual token: no file linkage
		ui.Info("Using refresh token from --key")
	case strings.TrimSpace(agyCfg.RefreshToken) != "":
		fmt.Printf("  Current: %s\n", ui.Dimmed(maskToken(agyCfg.RefreshToken)))
		ui.Info("Refresh token kept (it never rotates); run with -k to replace")
	default:
		// Try the local agy CLI login before prompting.
		if err := agy.ImportTokenFile(&agyCfg); err == nil {
			ui.Info(fmt.Sprintf("Imported refresh token from %s", agyCfg.TokenFile))
		} else {
			fmt.Printf("%s refresh token (%s): ",
				ui.Style("?", ui.Cyan, ui.Bold),
				ui.Dimmed("paste from ~/.gemini/antigravity-cli/antigravity-oauth-token"))
			byteToken, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				fmt.Println()
				return fmt.Errorf("read refresh token: %w", err)
			}
			fmt.Println()
			token := strings.TrimSpace(string(byteToken))
			if token == "" {
				return fmt.Errorf("refresh token cannot be empty")
			}
			agyCfg.RefreshToken = token
		}
	}

	config.Current.AGY = agyCfg
	if err := config.SaveConfig(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	ui.Success(fmt.Sprintf("agy credentials saved (pool: %s)", agyCfg.PoolName()))
	return nil
}

func maskToken(t string) string {
	if len(t) < 12 {
		return strings.Repeat("*", len(t))
	}
	return t[:6] + strings.Repeat("*", len(t)-10) + t[len(t)-4:]
}

var (
	apiKeyFlag  string
	poolFlag    string
	projectFlag string
)

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().StringVarP(&apiKeyFlag, "key", "k", "", "GLM API key, or agy refresh token with --provider agy")
	loginCmd.Flags().StringVar(&poolFlag, "pool", "", "agy pool to anchor: gemini (default) or 3p")
	loginCmd.Flags().StringVar(&projectFlag, "project", "", "agy cloudaicompanionProject id (auto-discovered by default)")
}
