package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	Proxy     string           `mapstructure:"proxy" json:"proxy" yaml:"proxy,omitempty"`
	GLM       GLMConfig        `mapstructure:"glm" json:"glm" yaml:"glm,omitempty"`
	Providers []ProviderConfig `mapstructure:"providers" json:"providers" yaml:"providers,omitempty"`
	Schedule  ScheduleConfig   `mapstructure:"schedule" json:"schedule" yaml:"schedule,omitempty"`
}

type ProviderConfig struct {
	Name    string                 `mapstructure:"name" json:"name" yaml:"name"`
	Type    string                 `mapstructure:"type" json:"type" yaml:"type"`
	APIKey  string                 `mapstructure:"api_key" json:"api_key,omitempty" yaml:"api_key,omitempty"`
	BaseURL string                 `mapstructure:"base_url" json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Enabled bool                   `mapstructure:"enabled" json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Extra   map[string]interface{} `mapstructure:",remain" json:"-" yaml:"-"`
}

type GLMConfig struct {
	APIKey  string `mapstructure:"api_key" json:"api_key" yaml:"api_key,omitempty"`
	BaseURL string `mapstructure:"base_url" json:"base_url" yaml:"base_url,omitempty"`
}

type ScheduleConfig struct {
	Timezone string   `mapstructure:"timezone" json:"timezone" yaml:"timezone,omitempty"`
	Times    []string `mapstructure:"times" json:"times" yaml:"times,omitempty"`
}

func (s ScheduleConfig) IsEmpty() bool {
	return strings.TrimSpace(s.Timezone) == "" || len(s.Times) == 0
}

var (
	CfgFile string
	Current Config
)

func InitConfig() {
	if CfgFile != "" {
		viper.SetConfigFile(CfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		configPath := filepath.Join(home, ".config", "glm")
		if err := os.MkdirAll(configPath, 0700); err != nil {
			fmt.Printf("Error creating config directory: %v\n", err)
		}

		viper.AddConfigPath(configPath)
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if CfgFile != "" {
			fmt.Printf("Error reading config file %s: %v\n", CfgFile, err)
			os.Exit(1)
		}
		if !os.IsNotExist(err) {
			fmt.Printf("Warning: error reading config file: %v\n", err)
		}
	}

	if err := viper.Unmarshal(&Current, func(c *mapstructure.DecoderConfig) {
		c.TagName = "mapstructure"
		c.DecodeHook = mapstructure.StringToTimeHookFunc(time.RFC3339)
	}); err != nil {
		fmt.Printf("Unable to decode into struct, %v", err)
	}

	fixLegacyKeys()
}

func fixLegacyKeys() {
	for i := range Current.Providers {
		p := &Current.Providers[i]
		if p.Extra == nil {
			continue
		}

		moveString := func(legacyKey string, field *string) {
			if val, ok := p.Extra[legacyKey]; ok {
				if strVal, ok := val.(string); ok && strVal != "" && *field == "" {
					*field = strVal
				}
				delete(p.Extra, legacyKey)
			}
		}

		moveString("apikey", &p.APIKey)
		moveString("baseurl", &p.BaseURL)
	}
}

func SaveConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(home, ".config", "glm")
	configFile := filepath.Join(configPath, "config.yaml")

	if err := os.MkdirAll(configPath, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal directly from the Current struct to avoid viper state inconsistency.
	data, err := yaml.Marshal(Current)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func UpdateProvider(cfg ProviderConfig) error {
	found := false
	for i, p := range Current.Providers {
		if p.Name == cfg.Name {
			Current.Providers[i] = cfg
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("provider %s not found", cfg.Name)
	}
	viper.Set("providers", Current.Providers)
	return SaveConfig()
}
