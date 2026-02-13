package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"

	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:   "monitor [all|glm|antigravity|cli]",
	Short: "Monitor usage and quotas across all providers",
	Run: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		target := "all"
		if len(args) > 0 {
			target = args[0]
		}

		fmt.Printf("\n\033[1;36m[*] AI-Daemon Quota Dashboard (%s)\033[0m\n", time.Now().Format("15:04:05"))
		fmt.Println("\033[36m────────────────────────────────────────────────────────────\033[0m")

		registry := []interfaces.Provider{
			glm.NewProvider(),
			antigravity.NewProvider(),
			geminicli.NewProvider(),
		}

		for _, p := range registry {
			if target != "all" && p.ID() != target {
				if target == "cli" && p.ID() == "geminicli" {
					// match
				} else {
					continue
				}
			}

			fmt.Printf("\n\033[1m[ %s ]\033[0m\n", p.Name())
			p.SetDebug(debug)

			if err := p.Authenticate(); err != nil {
				fmt.Printf("  \033[33m[!] %v\033[0m\n", err)
				continue
			}

			quota, err := p.GetQuota()
			if err != nil {
				fmt.Printf("  \033[31m[-] Error: %v\033[0m\n", err)
				continue
			}

			displayQuota(p.ID(), quota)
		}
		fmt.Println()
	},
}

func displayQuota(id string, q *interfaces.QuotaStatus) {
	switch id {
	case "glm":
		color := "\033[32m"
		if q.Remaining < 20 { color = "\033[31m" }
		fmt.Printf("  [+] Quota: %s%d%%\033[0m Remaining | Used: %d%%\n", color, q.Remaining, q.Used)
	case "antigravity":
		models := []struct{ID, Label string}{
			{"gemini-3-flash", "Gemini 3 Flash"},
			{"gemini-3-pro-low", "Gemini 3 Pro"},
			{"claude-sonnet-4-5", "Claude Sonnet 4.5"},
		}
		for _, m := range models {
			rem, reset := extractModelQuota(q.Raw, m.ID)
			color := "\033[32m"
			if rem < 20 { color = "\033[31m" }
			fmt.Printf("    [+] %-18s: %s%3.0f%%\033[0m", m.Label, color, rem)
			if !reset.IsZero() {
				fmt.Printf("  (%s / %s left)", reset.Local().Format("15:04"), formatTimeUntil(reset))
			}
			fmt.Println()
		}
	case "geminicli":
		cliModels := extractCliQuota(q.Raw)
		for _, cm := range cliModels {
			color := "\033[32m"
			rem := cm.RemainingFraction * 100
			if rem < 20 { color = "\033[31m" }
			fmt.Printf("    [+] %-18s: %s%3.0f%%\033[0m", cm.ModelID, color, rem)
			if cm.ResetTime != "" {
				reset, _ := time.Parse(time.RFC3339, cm.ResetTime)
				fmt.Printf("  (%s / %s left)", reset.Local().Format("15:04"), formatTimeUntil(reset))
			}
			fmt.Println()
		}
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
	return 0, time.Time{}
}

type CliQuotaModel struct {
	ModelID           string
	RemainingFraction float64
	ResetTime         string
}

func extractCliQuota(raw string) []CliQuotaModel {
	var data struct {
		Buckets []struct {
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
			ModelID           string  `json:"modelId"`
		} `json:"buckets"`
	}

	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil
	}

	type modelGroup struct {
		quota     float64
		resetTime string
		models    []string
	}

	groups := make(map[string]*modelGroup)

	for _, b := range data.Buckets {
		if b.ModelID == "" || strings.HasSuffix(b.ModelID, "_vertex") {
			continue
		}

		key := fmt.Sprintf("%.2f-%s", b.RemainingFraction, b.ResetTime)
		if _, exists := groups[key]; !exists {
			groups[key] = &modelGroup{
				quota:     b.RemainingFraction,
				resetTime: b.ResetTime,
				models:    []string{},
			}
		}
		groups[key].models = append(groups[key].models, b.ModelID)
	}

	var result []CliQuotaModel
	for _, group := range groups {
		representative := selectRepresentativeModel(group.models)
		result = append(result, CliQuotaModel{
			ModelID:           representative,
			RemainingFraction: group.quota,
			ResetTime:         group.resetTime,
		})
	}

	return result
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().Bool("debug", false, "Enable debug output")
}
