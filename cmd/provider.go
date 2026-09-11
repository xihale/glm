package cmd

import (
	"fmt"

	"github.com/xihale/glm/pkg/agy"
	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/glm"
)

// providerClient is the quota backend contract shared by the glm and agy
// providers. Both 5h-window backends report the same shape — Remaining 0-100
// (100 = window fresh/unused) plus the window's ResetTime — so the daemon
// state machine drives either one unchanged.
type providerClient interface {
	GetQuota() (*glm.QuotaStatus, error)
	Activate(force bool, serviceMode bool) (*glm.QuotaStatus, error)
	SetDebug(d bool)
}

// newProviderClientFor builds a client for the active provider from a config
// snapshot. pool pins the agy pool ("gemini"/"3p"); it is ignored for glm.
func newProviderClientFor(cfg config.Config, pool string) (providerClient, error) {
	switch config.EffectiveProvider() {
	case "agy":
		c := agy.NewClientFrom(cfg)
		if pool != "" {
			c.SetPool(pool)
			if pc, ok := cfg.AGY.Pools[pool]; ok && pc.Model != "" {
				c.SetModel(pc.Model)
			}
		}
		return c, nil
	default:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("no API key configured. Run 'glm login' first")
		}
		return glm.NewClientFrom(cfg), nil
	}
}

func newProviderClient() (providerClient, error) {
	return newProviderClientFor(config.Snapshot(), "")
}

// newProviderClientWithPool is newProviderClient with an explicit agy pool
// override ("gemini"/"3p"); ignored for glm.
func newProviderClientWithPool(pool string) (providerClient, error) {
	return newProviderClientFor(config.Snapshot(), pool)
}

// providerArg is the flag baked into a systemd unit so the daemon resolves
// the same section the operator installed.
func providerArg() string {
	if config.EffectiveProvider() == "agy" {
		return "--provider agy"
	}
	return ""
}

// daemonPools returns the anchor loops the daemon should run for the active
// provider: one unnamed loop for glm, or one per configured agy pool
// (pools map wins over the single `pool` setting).
func daemonPools(cfg config.Config) []string {
	if config.EffectiveProvider() != "agy" {
		return []string{""}
	}
	return cfg.AGY.AnchoredPools()
}

// scheduleForScope resolves the schedule for a daemon loop: per-pool schedule
// (when the pool is listed in agy.pools), then agy.schedule, then the shared
// top-level schedule.
func scheduleForScope(cfg config.Config, pool string) config.ScheduleConfig {
	if pool != "" {
		if pc, ok := cfg.AGY.Pools[pool]; ok && !pc.Schedule.IsEmpty() {
			return pc.Schedule
		}
		if !cfg.AGY.Schedule.IsEmpty() {
			return cfg.AGY.Schedule
		}
	}
	return cfg.Schedule
}

// weeklyForScope resolves the passive weekly-refresh flag: pools.X.weekly
// when the pool is listed in agy.pools (nil falls back), else agy.weekly.
// glm never has one.
func weeklyForScope(cfg config.Config, pool string) bool {
	if config.EffectiveProvider() != "agy" || pool == "" {
		return false
	}
	if pc, ok := cfg.AGY.Pools[pool]; ok && pc.Weekly != nil {
		return *pc.Weekly
	}
	return cfg.AGY.Weekly
}

// weeklyOnlyForScope reports whether a pool runs in weekly keep-alive mode
// (pools.X.weekly_only): no 5h schedule anchoring, only weekly-bucket fires.
// Only explicit pools entries can opt in; the unnamed glm loop never does.
func weeklyOnlyForScope(cfg config.Config, pool string) bool {
	if config.EffectiveProvider() != "agy" || pool == "" {
		return false
	}
	return cfg.AGY.Pools[pool].WeeklyOnly
}
