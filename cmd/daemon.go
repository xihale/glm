package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the daemon to schedule heartbeats",
	Long:  `Starts the daemon process that periodically sends heartbeats to configured providers.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDaemon()
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon() {
	fmt.Println("Starting ai-daemon scheduler...")
	
	c := cron.New(cron.WithSeconds())

	_, err := c.AddFunc("0 0 * * * *", func() {
		runRefreshWithJitter()
	})
	if err != nil {
		fmt.Printf("Error scheduling cron: %v\n", err)
		return
	}

	c.Start()
	fmt.Println("Scheduler started. Press Ctrl+C to stop.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nStopping scheduler...")
	c.Stop()
}

func runRefreshWithJitter() {
	jitter := time.Duration(rand.Intn(300)) * time.Second
	fmt.Printf("[%s] Job triggered. Waiting %v jitter...\n", time.Now().Format(time.RFC3339), jitter)
	time.Sleep(jitter)

	fmt.Printf("[%s] Starting scheduled refresh...\n", time.Now().Format(time.RFC3339))

	registry := []interfaces.Provider{
		glm.NewProvider(),
		antigravity.NewProvider(),
		geminicli.NewProvider(),
	}

	for _, p := range registry {
		if err := p.Authenticate(); err != nil {
			fmt.Printf("[%s] %s: Auth skipped: %v\n", time.Now().Format(time.TimeOnly), p.Name(), err)
			continue
		}

		// Use Activate as the background refresh mechanism
		if err := p.Activate(false, false); err != nil {
			fmt.Printf("[%s] %s: Refresh ERROR: %v\n", time.Now().Format(time.TimeOnly), p.Name(), err)
		} else {
			fmt.Printf("[%s] %s: Refresh SUCCESS\n", time.Now().Format(time.TimeOnly), p.Name())
		}
	}

	fmt.Printf("[%s] Refresh cycle completed.\n", time.Now().Format(time.RFC3339))
}
