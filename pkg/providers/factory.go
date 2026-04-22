package providers

import (
	"fmt"
	"strings"

	"github.com/xihale/glm/pkg/config"
	"github.com/xihale/glm/pkg/providers/glm"
	"github.com/xihale/glm/pkg/providers/interfaces"
)

// LoadProvidersFromConfig creates provider instances based on the global configuration
func LoadProvidersFromConfig() []interfaces.Provider {
	var registry []interfaces.Provider

	// 1. Load legacy/single-instance provider for backward compatibility
	if config.Current.GLM.APIKey != "" {
		registry = append(registry, glm.NewProvider())
	}

	// 2. Load multi-account providers from config.Current.Providers
	for _, pCfg := range config.Current.Providers {
		if !pCfg.Enabled {
			continue
		}

		switch strings.ToLower(pCfg.Type) {
		case "glm":
			registry = append(registry, glm.NewProviderWithConfig(pCfg))
		default:
			fmt.Printf("Warning: Unknown provider type '%s' for provider '%s'\n", pCfg.Type, pCfg.Name)
		}
	}

	// 3. Set total count so providers can decide display behavior
	for _, p := range registry {
		switch v := p.(type) {
		case interface{ SetTotal(int) }:
			v.SetTotal(len(registry))
		}
	}

	return registry
}
