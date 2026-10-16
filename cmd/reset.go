package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xihale/glm/pkg/ui"
	"github.com/xihale/glm/pkg/zproxy"
)

// zproxyBaseURL resolves the gateway URL: --url flag, then ZPROXY_URL, then
// the default local gateway.
func zproxyBaseURL(cmd *cobra.Command) string {
	if u, _ := cmd.Flags().GetString("url"); strings.TrimSpace(u) != "" {
		return u
	}
	if u := strings.TrimSpace(os.Getenv("ZPROXY_URL")); u != "" {
		return u
	}
	return zproxy.DefaultBaseURL
}

func zproxyURLFlag(cmd *cobra.Command) {
	cmd.Flags().String("url", "", "zproxy gateway base URL (default: http://127.0.0.1:8870, env ZPROXY_URL)")
}

// statusReset prints the reset-card engine snapshot: card counts first —
// that is what the command is for — then windows, schedule, and the
// engine's next planned action.
var statusResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Show reset-card status (card count, windows, engine decision)",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := zproxy.NewClient(zproxyBaseURL(cmd))
		s, err := c.Status()
		if err != nil {
			return err
		}
		if !s.Auth.OK {
			fmt.Println(ui.Dimmed("engine auth unavailable: " + s.Auth.Reason))
		}

		fmt.Printf(" %s · %s\n", ui.Style("Reset cards (zproxy "+c.BaseURL+")", ui.Bold), modeColored(s.Mode))
		order := []string{zproxy.WindowFiveHour, zproxy.WindowWeek}
		for _, kind := range order {
			d, ok := s.Windows[kind]
			if !ok || !d.Supported {
				continue // plan has no such window
			}
			label := "5h"
			if kind == zproxy.WindowWeek {
				label = "week"
			}
			rel := func(t int64) string {
				dur := time.Until(time.UnixMilli(t))
				if dur < 0 {
					return "expired"
				}
				return shortDur(dur)
			}
			cards := "0 cards"
			if n := len(d.Cards); n > 0 {
				sort.Slice(d.Cards, func(i, j int) bool { return d.Cards[i] < d.Cards[j] })
				cards = fmt.Sprintf("%d cards (earliest expires in %s)", n, rel(d.Cards[0]))
			}
			win := "no reading"
			if d.Window != nil {
				if d.Window.Percentage != nil {
					win = fmt.Sprintf("%.0f%% used", *d.Window.Percentage)
				}
				if d.Window.NextResetTime != nil {
					win += " · reset at " + time.UnixMilli(*d.Window.NextResetTime).Local().Format("15:04")
				}
			}
			last := "never"
			if t := s.LastUse[kind]; t > 0 {
				last = time.UnixMilli(t).Local().Format("01-02 15:04")
			}
			fmt.Printf("     %-5s %s\n", label+":", ui.Style(cards, ui.Bold))
			fmt.Printf("     %-5s %s · last card %s\n", "", win, last)
		}

		if s.Claim.NextClaimAt > 0 {
			t := time.UnixMilli(s.Claim.NextClaimAt)
			fmt.Printf("     Claim: %s (in %s)\n", t.Local().Format("15:04:05"), shortDur(time.Until(t)))
		} else {
			fmt.Printf("     Claim: unscheduled\n")
		}
		if d5 := s.Windows[zproxy.WindowFiveHour]; d5.Reason != "" {
			verb := ui.Dimmed("hold")
			if d5.Use {
				verb = ui.Style("will use", ui.Green, ui.Bold)
			}
			fmt.Printf("     Engine: %s (%s)\n", verb, d5.Reason)
		}
		fmt.Println()
		fmt.Println(ui.Dimmed("'glm reset use' spends a card to reset the 5h window now · 'glm reset mode manual' pauses automatic card use"))
		return nil
	},
}

// resetCmd drives the reset-card engine: manual use/claim and the policy
// switch. The engine (zproxy, --reset-auto) stays the only decision maker;
// these commands are its remote control.
var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Control zproxy reset cards (use, claim, mode)",
	Long: `Drive zproxy's reset-card engine over its local gateway.

The engine keeps its own policy (auto mode uses cards when a window is
exhausted or a card is about to expire); these commands force actions or
flip the mode:
  glm reset use              spend one card on the 5h window now
  glm reset use --window week
  glm reset claim            ask the server for a new card
  glm reset mode manual      pause automatic card use
  glm reset mode auto        resume it`,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var resetUseCmd = &cobra.Command{
	Use:   "use",
	Short: "Spend one reset card now (default: 5h window)",
	RunE: func(cmd *cobra.Command, args []string) error {
		w, _ := cmd.Flags().GetString("window")
		if w == "5h" {
			w = zproxy.WindowFiveHour
		}
		c := zproxy.NewClient(zproxyBaseURL(cmd))
		used, err := c.Use(w)
		if err != nil {
			return err
		}
		label := "5h"
		if w == zproxy.WindowWeek {
			label = "week"
		}
		if used {
			ui.Success(fmt.Sprintf("Reset card spent — %s window reset", label))
		} else {
			return fmt.Errorf("server did not confirm card use (no card or throttled); try 'glm reset claim' first")
		}
		return nil
	},
}

var resetClaimCmd = &cobra.Command{
	Use:   "claim",
	Short: "Ask the server for a new reset card",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := zproxy.NewClient(zproxyBaseURL(cmd))
		if err := c.Claim(); err != nil {
			return err
		}
		ui.Success("claim request sent (the server hands out cards on its cycle; on throttle the engine retries at next_try_at)")
		return nil
	},
}

var resetModeCmd = &cobra.Command{
	Use:   "mode <auto|manual>",
	Short: "Switch the engine between automatic and manual card use",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"auto", "manual"}, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c := zproxy.NewClient(zproxyBaseURL(cmd))
		p, err := c.SetMode(args[0])
		if err != nil {
			return err
		}
		ui.Success(fmt.Sprintf("engine mode set to %s (thresholds unchanged: exhausted ≥%.0f%% · cooldown %.0fmin)",
			modeColored(p.Mode), p.ExhaustedPct, p.CooldownMin))
		return nil
	},
}

func modeColored(m string) string {
	if m == "auto" {
		return ui.Style("auto", ui.Green, ui.Bold)
	}
	return ui.Style("manual", ui.Yellow, ui.Bold)
}

// shortDur renders a short duration: 45m, 3h, 6d.
func shortDur(d time.Duration) string {
	if m := int(d.Minutes()); m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if h := int(d.Hours()); h < 48 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func init() {
	statusCmd.AddCommand(statusResetCmd)
	zproxyURLFlag(statusResetCmd)

	resetCmd.AddCommand(resetUseCmd)
	resetUseCmd.Flags().String("window", zproxy.WindowFiveHour, "window to reset: five_hour (5h) or week")
	resetUseCmd.RegisterFlagCompletionFunc("window", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"five_hour", "week"}, cobra.ShellCompDirectiveNoFileComp
	})
	resetCmd.AddCommand(resetClaimCmd)
	resetCmd.AddCommand(resetModeCmd)
	for _, c := range []*cobra.Command{resetCmd, resetUseCmd, resetClaimCmd, resetModeCmd} {
		zproxyURLFlag(c)
	}
	rootCmd.AddCommand(resetCmd)
}
