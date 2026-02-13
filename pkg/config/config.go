package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	GLM         GLMConfig         `mapstructure:"glm" json:"glm"`
	Gemini      GeminiConfig      `mapstructure:"gemini" json:"gemini"`
	Antigravity AntigravityConfig `mapstructure:"antigravity" json:"antigravity"`
}

type GLMConfig struct {
	APIKey  string `mapstructure:"api_key" json:"api_key"`
	BaseURL string `mapstructure:"base_url" json:"base_url"`
}

type GeminiConfig struct {
	Secure1PSID   string `mapstructure:"secure_1psid" json:"secure_1psid"`
	Secure1PSIDTS string `mapstructure:"secure_1psidts" json:"secure_1psidts"`
	AccessToken   string `mapstructure:"access_token" json:"access_token"`
	RefreshToken  string `mapstructure:"refresh_token" json:"refresh_token"`
	ProjectID     string `mapstructure:"project_id" json:"project_id"`
}

type AntigravityConfig struct {
	Enabled bool `mapstructure:"enabled" json:"enabled"`
}

var (
	cfgFile string
	Current Config
)

func InitConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
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
