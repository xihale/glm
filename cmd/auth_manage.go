package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var authListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured providers",
	Run: func(cmd *cobra.Command, args []string) {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSTATUS")
		for _, p := range config.Current.Providers {
			status := "\033[32mEnabled\033[0m"
			if !p.Enabled {
				status = "\033[31mDisabled\033[0m"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Type, status)
		}
		w.Flush()
	},
}

var authEnableCmd = &cobra.Command{
	Use:   "enable [name]",
	Short: "Enable a provider",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return getProviderNames(), cobra.ShellCompDirectiveNoFileComp
	},
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		found := false
		for i, p := range config.Current.Providers {
			if p.Name == name {
				config.Current.Providers[i].Enabled = true
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("Provider %s not found\n", name)
			return
		}
		viper.Set("providers", config.Current.Providers)
		config.SaveConfig()
		fmt.Printf("Provider %s enabled\n", name)
	},
}

var authDisableCmd = &cobra.Command{
	Use:   "disable [name]",
	Short: "Disable a provider",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return getProviderNames(), cobra.ShellCompDirectiveNoFileComp
	},
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		found := false
		for i, p := range config.Current.Providers {
			if p.Name == name {
				config.Current.Providers[i].Enabled = false
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("Provider %s not found\n", name)
			return
		}
		viper.Set("providers", config.Current.Providers)
		config.SaveConfig()
		fmt.Printf("Provider %s disabled\n", name)
	},
}

var authRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a provider",
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
			fmt.Printf("Provider %s not found\n", name)
			return
		}
		config.Current.Providers = append(config.Current.Providers[:idx], config.Current.Providers[idx+1:]...)
		viper.Set("providers", config.Current.Providers)
		config.SaveConfig()
		fmt.Printf("Provider %s removed\n", name)
	},
}

func getProviderNames() []string {
	names := make([]string, 0, len(config.Current.Providers))
	for _, p := range config.Current.Providers {
		names = append(names, p.Name)
	}
	return names
}

func init() {
	authCmd.AddCommand(authListCmd)
	authCmd.AddCommand(authEnableCmd)
	authCmd.AddCommand(authDisableCmd)
	authCmd.AddCommand(authRemoveCmd)
}
