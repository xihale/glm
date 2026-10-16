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

		fmt.Printf(" %s · %s\n", ui.Style("重置卡 (zproxy "+c.BaseURL+")", ui.Bold), modeColored(s.Mode))
		order := []string{zproxy.WindowFiveHour, zproxy.WindowWeek}
		for _, kind := range order {
			d, ok := s.Windows[kind]
			if !ok || !d.Supported {
				continue // 套餐没有该窗口/卡种
			}
			label := "5h"
			if kind == zproxy.WindowWeek {
				label = "周"
			}
			rel := func(t int64) string {
				dur := time.Until(time.UnixMilli(t))
				if dur < 0 {
					return "已过期"
				}
				return shortDur(dur)
			}
			cards := "0张"
			if n := len(d.Cards); n > 0 {
				sort.Slice(d.Cards, func(i, j int) bool { return d.Cards[i] < d.Cards[j] })
				cards = fmt.Sprintf("%d张（最早 %s后过期）", n, rel(d.Cards[0]))
			}
			win := "无读数"
			if d.Window != nil {
				if d.Window.Percentage != nil {
					win = fmt.Sprintf("已用 %.0f%%", *d.Window.Percentage)
				}
				if d.Window.NextResetTime != nil {
					win += " · " + time.UnixMilli(*d.Window.NextResetTime).Local().Format("15:04") + "重置"
				}
			}
			last := "从未"
			if t := s.LastUse[kind]; t > 0 {
				last = time.UnixMilli(t).Local().Format("01-02 15:04")
			}
			fmt.Printf("     %-4s %s\n", label+":", ui.Style(cards, ui.Bold))
			fmt.Printf("     %-4s 窗口 %s · 上次用卡 %s\n", "", win, last)
		}

		if s.Claim.NextClaimAt > 0 {
			t := time.UnixMilli(s.Claim.NextClaimAt)
			fmt.Printf("     领卡:  %s（%s后）\n", t.Local().Format("15:04:05"), shortDur(time.Until(t)))
		} else {
			fmt.Printf("     领卡:  待调度\n")
		}
		if d5 := s.Windows[zproxy.WindowFiveHour]; d5.Reason != "" {
			verb := ui.Dimmed("暂不用卡")
			if d5.Use {
				verb = ui.Style("将用卡", ui.Green, ui.Bold)
			}
			fmt.Printf("     引擎:  %s（%s）\n", verb, d5.Reason)
		}
		fmt.Println()
		fmt.Println(ui.Dimmed("'glm reset use' 用一张卡立即重置 5h 窗口 · 'glm reset mode manual' 暂停自动用卡"))
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
		label := "5 小时"
		if w == zproxy.WindowWeek {
			label = "周"
		}
		if used {
			ui.Success(fmt.Sprintf("已用一张重置卡，%s窗口已重置", label))
		} else {
			return fmt.Errorf("服务端未确认用卡（无可用卡或已节流）；可先 'glm reset claim'")
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
		ui.Success("领取请求已发出（服务端按周期放号，节流时引擎会按 next_try_at 重试）")
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
		ui.Success(fmt.Sprintf("引擎模式已切换为 %s（阈值不变：耗尽≥%.0f%% · 冷却 %.0fmin）",
			modeColored(p.Mode), p.ExhaustedPct, p.CooldownMin))
		return nil
	},
}

func modeColored(m string) string {
	if m == "auto" {
		return ui.Style("auto（自动）", ui.Green, ui.Bold)
	}
	return ui.Style("manual（手动）", ui.Yellow, ui.Bold)
}

// shortDur renders a duration the way the old magpie row did: 45m, 3h, 6d.
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
