package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/glm"

	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:   "monitor [all|glm|antigravity]",
	Short: "Monitor usage and quotas",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		target := "all"
		if len(args) > 0 {
			target = args[0]
		}

		fmt.Printf("\n🚀 AI-Daemon Quota Dashboard (%s)\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		if target == "all" || target == "glm" {
			displayGLM(debug)
		}

		if target == "all" || target == "antigravity" {
			displayAntigravity(debug)
		}
		fmt.Println()
	},
}

func displayGLM(debug bool) {
	p := glm.NewProvider()
	p.SetDebug(debug)
	fmt.Printf("\n[GLM Coding Plan]\n")
	if config.Current.GLM.APIKey == "" {
		fmt.Println("  ⚠️  Not Configured")
		return
	}

	if err := p.Authenticate(); err != nil {
		fmt.Printf("  ❌ Auth Failed: %v\n", err)
		return
	}

	quota, err := p.GetQuota()
	if err != nil {
		fmt.Printf("  ❌ Error: %v\n", err)
		return
	}

	color := "\033[32m" // Green
	if quota.Remaining < 20 {
		color = "\033[31m" // Red
	} else if quota.Remaining < 50 {
		color = "\033[33m" // Yellow
	}

	fmt.Printf("  Quota: %s%d%%\033[0m Remaining  |  Used: %d%%\n", color, quota.Remaining, quota.Used)
	if !quota.ResetTime.IsZero() {
		fmt.Printf("  Next Reset: %s (%s remaining)\n", quota.ResetTime.Local().Format("2006-01-02 15:04:05"), formatTimeUntil(quota.ResetTime))
	}
}

func displayAntigravity(debug bool) {
	p := antigravity.NewProvider()
	p.SetDebug(debug)
	fmt.Printf("\n[Antigravity IDE]\n")

	if err := p.Authenticate(); err != nil {
		fmt.Printf("  ⚠️  %v\n", err)
		return
	}

	quota, err := p.GetQuota()
	if err != nil {
		fmt.Printf("  ❌ Error: %v\n", err)
		return
	}

	fmt.Printf("  Status: %s (Remote Mode)\n", quota.Type)

	models := []struct {
		Name  string
		Label string
	}{
		{"gemini-3-flash", "Gemini 3 Flash"},
		{"gemini-3-pro-high", "Gemini 3 Pro"},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5"},
	}

	for _, m := range models {
		rem, reset := extractModelQuota(quota.Raw, m.Name)
		color := "\033[32m"
		if rem < 20 {
			color = "\033[31m"
		} else if rem < 50 {
			color = "\033[33m"
		}

		fmt.Printf("  %-18s: %s%3.0f%%\033[0m", m.Label, color, rem)
		if !reset.IsZero() {
			fmt.Printf("  (%s / %s remaining)", reset.Local().Format("15:04"), formatTimeUntil(reset))
		}
		fmt.Println()
	}
}

func formatTimeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "Soon"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func extractModelQuota(raw string, modelID string) (float64, time.Time) {
	// Simple path extraction since we don't want to import heavy JSONPath libs
	// We'll use the existing QuotaResponse struct but generic
	var data struct {
		Models map[string]struct {
			QuotaInfo struct {
				RemainingFraction float64 `json:"remainingFraction"`
				ResetTime         string  `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}

	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return 0, time.Time{}
	}

	if m, ok := data.Models[modelID]; ok {
		reset, _ := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime)
		return m.QuotaInfo.RemainingFraction * 100, reset
	}
	return 100, time.Time{} // Default to 100 if not found
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().Bool("heartbeat", false, "Send a heartbeat ping")
	monitorCmd.Flags().Bool("debug", false, "Enable debug output")
}
