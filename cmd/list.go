package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/xihale/glm/pkg/config"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured providers",
	Run: func(cmd *cobra.Command, args []string) {
		if len(config.Current.Providers) == 0 {
			fmt.Println("No providers configured. Run 'glm login' first.")
			return
		}
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

func init() {
	rootCmd.AddCommand(listCmd)
}
