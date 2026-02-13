package cmd

import (
	"fmt"

	"ai-daemon/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cloneType string
)

var authCloneCmd = &cobra.Command{
	Use:   "clone [source_name] [new_name]",
	Short: "Clone an existing provider",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		sourceName := args[0]
		newName := args[1]

		var sourceProvider *config.ProviderConfig
		for _, p := range config.Current.Providers {
			if p.Name == sourceName {
				sourceProvider = &p
				break
			}
		}

		if sourceProvider == nil {
			fmt.Printf("Source provider %s not found\n", sourceName)
			return
		}

		for _, p := range config.Current.Providers {
			if p.Name == newName {
				fmt.Printf("Provider %s already exists\n", newName)
				return
			}
		}

		newProvider := *sourceProvider
		newProvider.Name = newName

		if cloneType != "" {
			newProvider.Type = cloneType
		}

		config.Current.Providers = append(config.Current.Providers, newProvider)
		viper.Set("providers", config.Current.Providers)
		if err := config.SaveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
			return
		}

		fmt.Printf("Provider %s cloned to %s\n", sourceName, newName)
	},
}

func init() {
	authCmd.AddCommand(authCloneCmd)
	authCloneCmd.Flags().StringVar(&cloneType, "type", "", "Change the provider type")
}
