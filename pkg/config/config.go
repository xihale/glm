package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"github.com/xihale/glm/pkg/log"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	APIKey   string         `mapstructure:"api_key" json:"api_key" yaml:"api_key"`
	BaseURL  string         `mapstructure:"base_url" json:"base_url" yaml:"base_url,omitempty"`
	Proxy    string         `mapstructure:"proxy" json:"proxy" yaml:"proxy,omitempty"`
	Schedule ScheduleConfig `mapstructure:"schedule" json:"schedule" yaml:"schedule,omitempty"`
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
			log.Fatalf("Error finding home directory: %v", err)
		}

		configPath := filepath.Join(home, ".config", "glm")
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

func SaveConfig() error {
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		if CfgFile != "" {
			configFile = CfgFile
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			configFile = filepath.Join(home, ".config", "glm", "config.yaml")
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

func DefaultConfigPath() string {
	if CfgFile != "" {
		return CfgFile
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "glm", "config.yaml")
}
