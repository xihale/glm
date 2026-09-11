package cmd

import (
	"fmt"
	"strings"

	"github.com/xihale/glm/pkg/agy"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/ui"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show quota status (glm or agy provider)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if config.EffectiveProvider() == "agy" {
			return statusAGY()
		}

		if config.Current.APIKey == "" {
			return fmt.Errorf("no API key configured. Run 'glm login' first")
		}

		client := glm.NewClient()
		quota, err := client.GetQuota()

		if err != nil {
			ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
			return err
		}

		printQuotaStatus(quota)
		return nil
	},
}

func statusAGY() error {
	cfg := config.Snapshot()
	client := agy.NewClientFrom(cfg)
	groups, err := client.GetSummary()
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
		return err
	}

	anchored := cfg.AGY.AnchoredPools()
	anchoredSet := map[string]bool{}
	for _, p := range anchored {
		anchoredSet[p] = true
	}

	for _, g := range groups {
		pool := agy.PoolGemini
		if g.Is3P {
			pool = agy.Pool3P
		}
		marker := " "
		if anchoredSet[pool] {
			marker = ui.Style("*", ui.Cyan, ui.Bold)
		}
		fmt.Printf(" %s %s\n", marker, ui.Style(g.Name, ui.Bold))
		printAGYBucket("5h", g.Five)
		printAGYBucket("weekly", g.Weekly)
	}

	// Weekly buckets self-refill on a rolling 7-day window; only the 5h
	// windows are anchored by the daemon. Weekly-only pools never touch
	// their 5h window — the daemon only keeps their weekly bucket rolling.
	parts := make([]string, 0, len(anchored))
	for _, p := range anchored {
		if weeklyOnlyForScope(cfg, p) {
			parts = append(parts, fmt.Sprintf("%s (weekly keepalive)", p))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", p, poolModel(cfg, p)))
	}
	fmt.Printf("\n%s\n", ui.Dimmed(fmt.Sprintf(
		"* = anchored pools: %s — 'glm active --provider agy' refreshes their 5h windows (weekly-keepalive pools never anchor 5h); weekly buckets self-refill",
		strings.Join(parts, ", "))))
	return nil
}

// poolModel resolves the warmup model for a pool: pools.X.model, then
// agy.model, then the pool default.
func poolModel(cfg config.Config, pool string) string {
	if pc, ok := cfg.AGY.Pools[pool]; ok && pc.Model != "" {
		return pc.Model
	}
	if cfg.AGY.Model != "" {
		return cfg.AGY.Model
	}
	if pool == agy.Pool3P {
		return agy.Default3PModel
	}
	return agy.DefaultGeminiModel
}

func printAGYBucket(label string, b agy.Bucket) {
	if b.ID == "" {
		return
	}
	pct := b.RemainingF * 100
	color := ui.Green
	if pct < 20 {
		color = ui.Red
	} else if pct < 50 {
		color = ui.Yellow
	}
	pctStr := ui.Style(fmt.Sprintf("%.1f%%", pct), color, ui.Bold)

	note := ""
	if pct >= 100 {
		note = ui.Dimmed(" (window not started)")
	}
	reset := ui.Dimmed("N/A")
	if !b.ResetTime.IsZero() {
		until := glm.FormatTimeUntil(b.ResetTime)
		at := b.ResetTime.Local().Format("01-02 15:04:05")
		if until == "Passed" {
			reset = fmt.Sprintf("%s, reset at %s", ui.Dimmed("passed"), at)
		} else {
			reset = fmt.Sprintf("%s, reset at %s", until, at)
		}
	}
	fmt.Printf("     %-6s %s (%s)%s\n", label+":", pctStr, reset, note)
}

func printQuotaStatus(q *glm.QuotaStatus) {
	// Remaining color
	color := ui.Green
	if q.Remaining < 20 {
		color = ui.Red
	} else if q.Remaining < 50 {
		color = ui.Yellow
	}
	pct := ui.Style(fmt.Sprintf("%d%%", q.Remaining), color, ui.Bold)

	// Reset time
	reset := ui.Dimmed("N/A")
	if !q.ResetTime.IsZero() {
		until := glm.FormatTimeUntil(q.ResetTime)
		at := q.ResetTime.Local().Format("15:04:05")
		if until == "Passed" {
			reset = fmt.Sprintf("%s, reset at %s", ui.Dimmed("passed"), at)
		} else {
			reset = fmt.Sprintf("%s, reset at %s", until, at)
		}
	}

	fmt.Printf("  %s (%s)\n", pct, reset)
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
