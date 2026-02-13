package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	GLM         GLMConfig         `mapstructure:"glm" json:"glm" yaml:"glm,omitempty"`
	Gemini      GeminiConfig      `mapstructure:"gemini" json:"gemini" yaml:"gemini,omitempty"`
	Antigravity AntigravityConfig `mapstructure:"antigravity" json:"antigravity" yaml:"antigravity,omitempty"`
	Providers   []ProviderConfig  `mapstructure:"providers" json:"providers" yaml:"providers,omitempty"`
}

type ProviderConfig struct {
	Name          string                 `mapstructure:"name" json:"name" yaml:"name"`
	Type          string                 `mapstructure:"type" json:"type" yaml:"type"`
	APIKey        string                 `mapstructure:"api_key" json:"api_key,omitempty" yaml:"api_key,omitempty"`
	BaseURL       string                 `mapstructure:"base_url" json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Enabled       bool                   `mapstructure:"enabled" json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Secure1PSID   string                 `mapstructure:"secure_1psid" json:"secure_1psid,omitempty" yaml:"secure_1psid,omitempty"`
	Secure1PSIDTS string                 `mapstructure:"secure_1psidts" json:"secure_1psidts,omitempty" yaml:"secure_1psidts,omitempty"`
	AccessToken   string                 `mapstructure:"access_token" json:"access_token,omitempty" yaml:"access_token,omitempty"`
	RefreshToken  string                 `mapstructure:"refresh_token" json:"refresh_token,omitempty" yaml:"refresh_token,omitempty"`
	ProjectID     string                 `mapstructure:"project_id" json:"project_id,omitempty" yaml:"project_id,omitempty"`
	Expiry        time.Time              `mapstructure:"expiry" json:"expiry,omitempty" yaml:"expiry,omitempty"`
	Extra         map[string]interface{} `mapstructure:",remain" json:"-" yaml:"-"`
}

type GLMConfig struct {
	APIKey  string `mapstructure:"api_key" json:"api_key" yaml:"api_key,omitempty"`
	BaseURL string `mapstructure:"base_url" json:"base_url" yaml:"base_url,omitempty"`
}

type GeminiConfig struct {
	Secure1PSID   string    `mapstructure:"secure_1psid" json:"secure_1psid" yaml:"secure_1psid,omitempty"`
	Secure1PSIDTS string    `mapstructure:"secure_1psidts" json:"secure_1psidts" yaml:"secure_1psidts,omitempty"`
	AccessToken   string    `mapstructure:"access_token" json:"access_token" yaml:"access_token,omitempty"`
	RefreshToken  string    `mapstructure:"refresh_token" json:"refresh_token" yaml:"refresh_token,omitempty"`
	ProjectID     string    `mapstructure:"project_id" json:"project_id" yaml:"project_id,omitempty"`
	Expiry        time.Time `mapstructure:"expiry" json:"expiry" yaml:"expiry,omitempty"`
}

type AntigravityConfig struct {
	Enabled bool `mapstructure:"enabled" json:"enabled" yaml:"enabled,omitempty"`
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

		configPath := filepath.Join(home, ".config", "ai-daemon")
		if err := os.MkdirAll(configPath, 0700); err != nil {
			fmt.Printf("Error creating config directory: %v\n", err)
		}

		viper.AddConfigPath(configPath)
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
	}

	if err := viper.Unmarshal(&Current); err != nil {
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

		moveTime := func(legacyKey string, field *time.Time) {
			if val, ok := p.Extra[legacyKey]; ok {
				if strVal, ok := val.(string); ok && field.IsZero() {
					if t, err := time.Parse(time.RFC3339, strVal); err == nil {
						*field = t
					}
				} else if tVal, ok := val.(time.Time); ok && field.IsZero() {
					*field = tVal
				}
				delete(p.Extra, legacyKey)
			}
		}

		moveString("apikey", &p.APIKey)
		moveString("baseurl", &p.BaseURL)
		moveString("secure1psid", &p.Secure1PSID)
		moveString("secure1psidts", &p.Secure1PSIDTS)
		moveString("accesstoken", &p.AccessToken)
		moveString("refreshtoken", &p.RefreshToken)
		moveString("projectid", &p.ProjectID)
		moveTime("expiry", &p.Expiry)
	}
}

func SaveConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(home, ".config", "ai-daemon")
	configFile := filepath.Join(configPath, "config.yaml")

	if err := os.MkdirAll(configPath, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := viper.WriteConfigAs(configFile); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	if err := os.Chmod(configFile, 0600); err != nil {
		return fmt.Errorf("failed to set secure permissions: %w", err)
	}

	return nil
}
