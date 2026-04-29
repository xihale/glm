package cmd

import (
	"fmt"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var logoutCmd = &cobra.Command{
	Use:   "logout [name]",
	Short: "Remove a provider account",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return getProviderNames(), cobra.ShellCompDirectiveNoFileComp
	},
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		idx := -1
		for i, p := range config.Current.Providers {
			if p.Name == name {
				idx = i
				break
			}
		}
		if idx == -1 {
			fmt.Printf("Provider '%s' not found\n", name)
			return
		}
		config.Current.Providers = append(config.Current.Providers[:idx], config.Current.Providers[idx+1:]...)
		viper.Set("providers", config.Current.Providers)
		config.SaveConfig()
		fmt.Printf("Provider '%s' removed\n", name)
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}

func getProviderNames() []string {
	names := make([]string, 0, len(config.Current.Providers))
	for _, p := range config.Current.Providers {
		names = append(names, p.Name)
	}
	return names
}
