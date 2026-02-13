package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-daemon/internal/utils"
	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

	// Check for token expiration every 5 minutes
	_, err := c.AddFunc("0 */5 * * * *", func() {
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

	// Refresh Gemini token if it's about to expire (within 5 minutes) or has already expired
	geminiConfig := config.Current.Gemini
	shouldRefresh := geminiConfig.RefreshToken != "" &&
		(geminiConfig.Expiry.IsZero() || time.Now().Add(5*time.Minute).After(geminiConfig.Expiry))

	if shouldRefresh {
		fmt.Printf("Token expiring soon (or expired). Current time: %s, Expiry: %s. Refreshing...\n",
			time.Now().Format(time.RFC3339), geminiConfig.Expiry.Format(time.RFC3339))

		newTokens, err := utils.RefreshGeminiToken(geminiConfig.RefreshToken)
		if err != nil {
			fmt.Printf("Warning: Token refresh failed: %v\n", err)
		} else {
			fmt.Println("Token refreshed successfully.")
			config.Current.Gemini.AccessToken = newTokens.AccessToken
			viper.Set("gemini.access_token", newTokens.AccessToken)

			if newTokens.RefreshToken != "" {
				config.Current.Gemini.RefreshToken = newTokens.RefreshToken
				viper.Set("gemini.refresh_token", newTokens.RefreshToken)
			}

			if newTokens.ExpiresIn > 0 {
				expiry := time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
				config.Current.Gemini.Expiry = expiry
				viper.Set("gemini.expiry", expiry)
			}

			if err := config.SaveConfig(); err != nil {
				fmt.Printf("Warning: Failed to save config after refresh: %v\n", err)
			}
		}
	} else if geminiConfig.RefreshToken != "" {
		fmt.Printf("Token still valid. Expires at: %s. Skipping refresh.\n", geminiConfig.Expiry.Format(time.RFC3339))
	}

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
