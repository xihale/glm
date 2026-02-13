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

	// GeminiCLI (Legacy)
	geminiEnabled := config.Current.Gemini.AccessToken != ""
	if geminiEnabled {
		registry = append(registry, geminicli.NewProvider())
		// Only sync antigravity if enabled in legacy config
		if config.Current.Antigravity.Enabled {
			registry = append(registry, antigravity.NewProvider())
		}
	}

	// 2. Load multi-account providers from config.Current.Providers
	for _, pCfg := range config.Current.Providers {
		if !pCfg.Enabled {
			continue
		}

		switch strings.ToLower(pCfg.Type) {
		case "glm":
			registry = append(registry, glm.NewProviderWithConfig(pCfg))
		case "antigravity":
			registry = append(registry, antigravity.NewProviderWithConfig(pCfg))
		case "geminicli", "gemini":
			registry = append(registry, geminicli.NewProviderWithConfig(pCfg))
			// Only sync antigravity if NOT explicitly disabled for this provider
			if !pCfg.DisableAntigravity {
				registry = append(registry, antigravity.NewProviderWithConfig(pCfg))
			}
		default:
			fmt.Printf("Warning: Unknown provider type '%s' for provider '%s'\n", pCfg.Type, pCfg.Name)
		}
	}

	return registry
}
