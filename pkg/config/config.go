package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"github.com/xihale/glm/pkg/log"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	// GLM provider section (top level, kept for backward compatibility with
	// single-provider configs).
	APIKey   string         `mapstructure:"api_key" json:"api_key" yaml:"api_key,omitempty"`
	BaseURL  string         `mapstructure:"base_url" json:"base_url" yaml:"base_url,omitempty"`
	Proxy    string         `mapstructure:"proxy" json:"proxy" yaml:"proxy,omitempty"`
	Schedule ScheduleConfig `mapstructure:"schedule" json:"schedule" yaml:"schedule,omitempty"`
	// AGY provider section (Google Antigravity / Gemini) with its own
	// credentials and schedule, so one config file drives both daemons.
	AGY AGYConfig `mapstructure:"agy" json:"agy" yaml:"agy,omitempty"`
}

// AGYConfig holds credentials and policy for the Antigravity (agy) provider.
// Only RefreshToken is required; the rest have working defaults.
type AGYConfig struct {
	RefreshToken string `mapstructure:"refresh_token" json:"refresh_token" yaml:"refresh_token,omitempty"`
	ClientID     string `mapstructure:"client_id" json:"client_id" yaml:"client_id,omitempty"`
	ClientSecret string `mapstructure:"client_secret" json:"client_secret" yaml:"client_secret,omitempty"`
	Project      string `mapstructure:"project" json:"project" yaml:"project,omitempty"`
	Pool         string `mapstructure:"pool" json:"pool" yaml:"pool,omitempty"`
	Model        string `mapstructure:"model" json:"model" yaml:"model,omitempty"`
	Endpoint     string `mapstructure:"endpoint" json:"endpoint" yaml:"endpoint,omitempty"`
	TokenFile    string `mapstructure:"token_file" json:"token_file" yaml:"token_file,omitempty"`
	// Proxy routes only agy traffic (http:// or socks5://); falls back to
	// the shared top-level proxy when empty. Keeps Google traffic on a
	// proxy while glm stays direct.
	Proxy string `mapstructure:"proxy" json:"proxy" yaml:"proxy,omitempty"`
	// Schedule is the agy daemon's own activation policy. When empty the
	// shared top-level schedule applies.
	Schedule ScheduleConfig `mapstructure:"schedule" json:"schedule" yaml:"schedule,omitempty"`
	// Weekly enables passive weekly refresh for the single-pool (legacy)
	// mode: the daemon wakes at the weekly bucket's reset moment and fires a
	// warmup. Weekly buckets otherwise self-refill with no request needed.
	Weekly bool `mapstructure:"weekly" json:"weekly" yaml:"weekly,omitempty"`
	// Pools anchors multiple model pools, each with its own policy. Keys are
	// "gemini" and "3p" (the two server-side pools). When set, the daemon
	// runs one anchor loop per listed pool and `pool` is ignored.
	Pools map[string]AGYPoolConfig `mapstructure:"pools" json:"pools" yaml:"pools,omitempty"`
}

// AGYPoolConfig is one pool's entry under agy.pools.
type AGYPoolConfig struct {
	Model    string         `mapstructure:"model" json:"model" yaml:"model,omitempty"`
	Schedule ScheduleConfig `mapstructure:"schedule" json:"schedule" yaml:"schedule,omitempty"`
	// Weekly enables the passive at-reset weekly refresh for this pool.
	// Nil falls back to agy.weekly.
	Weekly *bool `mapstructure:"weekly" json:"weekly" yaml:"weekly,omitempty"`
	// WeeklyOnly replaces the pool's 5h schedule anchoring with weekly
	// keep-alive: the daemon never anchors the 5h window on a schedule and
	// only fires a warmup to start/roll the weekly bucket (at startup while
	// it is fresh, then at each reported reset). The 5h window stays passive.
	WeeklyOnly bool `mapstructure:"weekly_only" json:"weekly_only" yaml:"weekly_only,omitempty"`
}

// ScheduleFor returns the activation policy governing the given provider.
// agy uses its own section's schedule when configured, falling back to the
// shared top-level schedule.
func (c Config) ScheduleFor(provider string) ScheduleConfig {
	if provider == "agy" && !c.AGY.Schedule.IsEmpty() {
		return c.AGY.Schedule
	}
	return c.Schedule
}

type ScheduleConfig struct {
	Auto     bool     `mapstructure:"auto" json:"auto" yaml:"auto,omitempty"`
	Timezone string   `mapstructure:"timezone" json:"timezone" yaml:"timezone,omitempty"`
	Times    []string `mapstructure:"times" json:"times" yaml:"times,omitempty"`
}

func (s ScheduleConfig) IsEmpty() bool {
	return !s.Auto && (strings.TrimSpace(s.Timezone) == "" || len(s.Times) == 0)
}

// Manual reports whether explicit activation times are configured, as
// opposed to Auto mode or no schedule at all.
func (s ScheduleConfig) Manual() bool {
	return !s.Auto && len(s.Times) > 0
}

// PoolName returns the normalized agy pool id: "gemini" (default) or "3p".
func (a AGYConfig) PoolName() string {
	if strings.EqualFold(strings.TrimSpace(a.Pool), "3p") {
		return "3p"
	}
	return "gemini"
}

// AnchoredPools returns the pools the agy daemon should anchor, in a stable
// order. With a pools map configured, every listed pool is anchored
// (gemini first); otherwise the single `pool` selection applies.
func (a AGYConfig) AnchoredPools() []string {
	if len(a.Pools) > 0 {
		var pools []string
		for _, p := range []string{"gemini", "3p"} {
			if _, ok := a.Pools[p]; ok {
				pools = append(pools, p)
			}
		}
		return pools
	}
	return []string{a.PoolName()}
}

var (
	CfgFile string
	// Provider mirrors the --provider root flag; it selects which section
	// of the shared config (and which systemd unit) a command operates on.
	Provider string
	Current  Config

	// mu guards Current: daemon loops snapshot it while SIGHUP reloads swap it.
	mu sync.RWMutex
)

// EffectiveProvider resolves the active provider from the --provider flag.
func EffectiveProvider() string {
	if strings.EqualFold(strings.TrimSpace(Provider), "agy") {
		return "agy"
	}
	return "glm"
}

// Snapshot returns a consistent copy of the current config for readers that
// run concurrently with Reload (multiple daemon pool loops).
func Snapshot() Config {
	mu.RLock()
	defer mu.RUnlock()
	return Current
}

func InitConfig() {
	if CfgFile != "" {
		viper.SetConfigFile(CfgFile)
	} else {
		configPath := filepath.Join(homeDir(), ".config", "glm")
		if err := os.MkdirAll(configPath, 0700); err != nil {
			log.Errorf("Error creating config directory: %v", err)
		}

		viper.AddConfigPath(configPath)
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if CfgFile != "" && !os.IsNotExist(err) {
			log.Fatalf("Error reading config file %s: %v", CfgFile, err)
		}
		if CfgFile == "" && !os.IsNotExist(err) {
			log.Debugf("Warning: error reading config file: %v", err)
		}
	}

	if err := viper.Unmarshal(&Current); err != nil {
		log.Errorf("Unable to decode into struct, %v", err)
	}
}

// Reload re-reads the config file into Current. On any error it leaves
// Current unchanged so callers keep running with the last good config.
func Reload() error {
	mu.Lock()
	defer mu.Unlock()
	if err := viper.ReadInConfig(); err != nil {
		return err
	}
	var fresh Config
	if err := viper.Unmarshal(&fresh); err != nil {
		return err
	}
	Current = fresh
	return nil
}

func SaveConfig() error {
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		if CfgFile != "" {
			configFile = CfgFile
		} else {
			configFile = DefaultConfigPath()
		}
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(Current)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// DefaultConfigPath returns the shared config file holding both providers'
// sections. When CfgFile is set it overrides this path.
func DefaultConfigPath() string {
	if CfgFile != "" {
		return CfgFile
	}
	return filepath.Join(homeDir(), ".config", "glm", "config.yaml")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error finding home directory: %v", err)
	}
	return home
}
