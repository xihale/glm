package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/gemini"
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
	fmt.Printf("Config loaded from: %s\n", config.Current.GLM.BaseURL) // Just logging something from config

	c := cron.New(cron.WithSeconds())

	// Schedule jobs
	// Refresh every 1 hour (as requested)
	// We add some jitter inside the job execution, not the schedule itself for simplicity,
	// or we can schedule it and then sleep a random amount.

	_, err := c.AddFunc("0 0 * * * *", func() {
		// Run every hour on the hour
		runRefreshWithJitter()
	})
	if err != nil {
		fmt.Printf("Error scheduling cron: %v\n", err)
		return
	}

	c.Start()
	fmt.Println("Scheduler started. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nStopping scheduler...")
	c.Stop()
}

func runRefreshWithJitter() {
	// Add jitter: sleep between 0 and 5 minutes
	jitter := time.Duration(rand.Intn(300)) * time.Second
	fmt.Printf("[%s] Job triggered. Waiting %v jitter...\n", time.Now().Format(time.RFC3339), jitter)
	time.Sleep(jitter)

	fmt.Printf("[%s] Starting scheduled refresh...\n", time.Now().Format(time.RFC3339))

	providers := []interfaces.Provider{}

	// Add providers if configured
	if config.Current.GLM.APIKey != "" {
		providers = append(providers, glm.NewProvider())
	}
	if config.Current.Gemini.AccessToken != "" || config.Current.Gemini.Secure1PSID != "" {
		providers = append(providers, gemini.NewProvider())
	}
	// Antigravity is always added as it depends on local process
	providers = append(providers, antigravity.NewProvider())

	for _, p := range providers {
		// Authenticate
		if err := p.Authenticate(); err != nil {
			// Log error but don't stop others
			// Silence "process not found" for Antigravity in logs to keep them clean?
			// Maybe log it as info/debug.
			fmt.Printf("[%s] %s: Auth skipped/failed: %v\n", time.Now().Format(time.TimeOnly), p.Name(), err)
			continue
		}

		// Send Heartbeat
		if err := p.SendHeartbeat(); err != nil {
			fmt.Printf("[%s] %s: Heartbeat ERROR: %v\n", time.Now().Format(time.TimeOnly), p.Name(), err)
		} else {
			fmt.Printf("[%s] %s: Heartbeat SUCCESS\n", time.Now().Format(time.TimeOnly), p.Name())
		}
	}

	fmt.Printf("[%s] Refresh cycle completed.\n", time.Now().Format(time.RFC3339))
}
