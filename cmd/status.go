package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/agy"
	"github.com/xihale/glm/pkg/cc"
	"github.com/xihale/glm/pkg/codex"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
	"github.com/xihale/glm/pkg/ui"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show quota status (glm, agy, commandcode, or codex)",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch config.EffectiveProvider() {
		case "agy":
			return statusAGY()
		case "commandcode":
			return statusCC()
		case "codex":
			return statusCodex()
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

// statusCC prints the raw rolling windows from one credits query — no
// remapping, the same data the daemon decides on.
func statusCC() error {
	if config.Current.CommandCode.APIKey == "" {
		return fmt.Errorf("no API key configured. Run 'glm login --provider commandcode' first")
	}

	creds, err := cc.NewClient().GetCredits()
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
		return err
	}

	cap, capErr := cc.NewClient().MonthlyCap(creds.Monthly)

	fmt.Printf(" %s\n", ui.Style("Command Code", ui.Bold))
	printCCWindow("5h", creds.Five)
	printCCWindow("weekly", creds.Weekly)
	if capErr != nil {
		fmt.Printf("     %-8s %.2f credits left (monthly; allotment unavailable: %v)\n",
			"balance:", creds.Monthly, capErr)
	} else {
		printCCBalance(cap, creds.Monthly)
	}
	fmt.Printf("\n%s\n", ui.Dimmed(
		"'glm active --provider commandcode' anchors the 5h window (first request after its reset starts a new one)"))
	return nil
}

func statusCodex() error {
	client := codex.NewClient()
	usage, err := client.Usage()
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to get quota: %v", err))
		return err
	}
	now := time.Now()
	title := "Codex"
	if usage.Plan != "" {
		title += " (" + usage.Plan + ")"
	}
	fmt.Printf(" %s\n", ui.Style(title, ui.Bold))
	printCodexWindow("5h", usage.Five, now)
	printCodexWindow("weekly", usage.Week, now)
	fmt.Printf("\n%s\n", ui.Dimmed(
		"'glm active --service --provider codex' waits for the 5h reset, then sends gpt-6-luna \"hello\" only if that window is not open"))
	return nil
}

func printCodexWindow(label string, w codex.Window, now time.Time) {
	if w.Span <= 0 && w.Reset.IsZero() {
		return
	}
	left := 100 - w.UsedPercent
	if left < 0 {
		left = 0
	}
	state := "not open"
	reset := "no reset"
	if !w.Reset.IsZero() {
		reset = w.Reset.Local().Format("2006-01-02 15:04:05")
	}
	if w.Open(now) {
		state = "open until " + reset
	} else if !w.Reset.IsZero() && w.Reset.After(now) {
		state = "not open (placeholder " + reset + ")"
	} else if !w.Reset.IsZero() {
		state = "not open (reset " + reset + " passed)"
	}
	fmt.Printf("     %-8s %.0f%% left, %s\n", label+":", left, state)
}

func printCCWindow(label string, w cc.Window) {
	if w.Cap <= 0 {
		return
	}
	left := (1 - w.Used/w.Cap) * 100
	if left < 0 {
		left = 0
	}
	color := ui.Green
	if left < 20 {
		color = ui.Red
	} else if left < 50 {
		color = ui.Yellow
	}
	pctStr := ui.Style(fmt.Sprintf("%.1f%%", left), color, ui.Bold)

	note := ""
	if w.Used <= 0 {
		note = ui.Dimmed(" (window not started)")
	}
	reset := ui.Dimmed("N/A")
	if !w.ResetAt.IsZero() {
		until := glm.FormatTimeUntil(w.ResetAt)
		at := w.ResetAt.Local().Format("01-02 15:04:05")
		if until == "Passed" {
			reset = fmt.Sprintf("%s, reset %s", ui.Dimmed("passed"), at)
		} else {
			reset = fmt.Sprintf("%s, reset at %s", until, at)
		}
	}
	fmt.Printf("     %-8s %s (%.2g/%.2g used, %s)%s\n", label+":", pctStr, w.Used, w.Cap, reset, note)
}

// printCCBalance renders the monthly credit balance in the same shape as the
// windows. The allotment is computed from the API (remaining + spent this
// billing period — see cc.Client.MonthlyCap); it follows plan changes and
// renewals with no configuration.
func printCCBalance(cap, remaining float64) {
	used := cap - remaining
	if used < 0 {
		used = 0
	}
	left := remaining / cap * 100
	if left > 100 {
		left = 100
	}
	color := ui.Green
	if left < 20 {
		color = ui.Red
	} else if left < 50 {
		color = ui.Yellow
	}
	pctStr := ui.Style(fmt.Sprintf("%.1f%%", left), color, ui.Bold)
	fmt.Printf("     %-8s %s (%.2g/%.2g used, monthly credits)\n", "balance:", pctStr, used, cap)
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
