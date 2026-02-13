package providers

import (
	"fmt"
	"strings"

	"ai-daemon/pkg/config"
	"ai-daemon/pkg/providers/antigravity"
	"ai-daemon/pkg/providers/geminicli"
	"ai-daemon/pkg/providers/glm"
	"ai-daemon/pkg/providers/interfaces"
)

// LoadProvidersFromConfig creates provider instances based on the global configuration
func LoadProvidersFromConfig() []interfaces.Provider {
	var registry []interfaces.Provider

	// 1. Load legacy/single-instance providers for backward compatibility
	// GLM
	if config.Current.GLM.APIKey != "" {
		registry = append(registry, glm.NewProvider())
	}

	// Antigravity (Legacy)
	if config.Current.Gemini.AccessToken != "" && config.Current.Antigravity.Enabled {
		registry = append(registry, antigravity.NewProvider())
	}

	// GeminiCLI (Legacy)
	if config.Current.Gemini.AccessToken != "" {
		registry = append(registry, geminicli.NewProvider())
	}

	// 2. Load multi-account providers from config.Current.Providers
	for _, pCfg := range config.Current.Providers {
		if !pCfg.Enabled {
			continue
		}

		var p interfaces.Provider

		switch strings.ToLower(pCfg.Type) {
		case "glm":
			p = glm.NewProviderWithConfig(pCfg)
		case "antigravity":
			p = antigravity.NewProviderWithConfig(pCfg)
		case "geminicli", "gemini":
			p = geminicli.NewProviderWithConfig(pCfg)
			registry = append(registry, p)
			if !pCfg.DisableAntigravity {
				registry = append(registry, antigravity.NewProviderWithConfig(pCfg))
			}
			continue
		default:
			fmt.Printf("Warning: Unknown provider type '%s' for provider '%s'\n", pCfg.Type, pCfg.Name)
			continue
		}

		if p != nil {
			registry = append(registry, p)
		}
	}

	return registry
}
