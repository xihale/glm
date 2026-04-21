package cmd

import (
	"fmt"
	"os"

	"glm/pkg/config"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "glm",
	Short: "A system-level daemon for GLM quota management",
	Long: `glm manages GLM (ChatGLM) API quota monitoring and 
automated heartbeat scheduling.`,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(config.InitConfig)

	rootCmd.PersistentFlags().StringVar(&config.CfgFile, "config", "", "config file (default is $HOME/.config/glm/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&config.Current.Proxy, "proxy", "", "HTTP/SOCKS proxy URL (e.g. http://localhost:1080)")
}
