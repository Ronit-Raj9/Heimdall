package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/heimdall"
)

// initHeimdallPlugin builds the Heimdall plugin from its PluginConfigs entry.
// The plugin is built in (no .so path) and is only registered when the config
// entry named "heimdall" is enabled. A missing config block means defaults.
// String values written as "env.NAME" (for example usage.secret) are read
// from the environment, the same syntax config.json uses elsewhere.
func initHeimdallPlugin(_ context.Context, pluginConfig any) (schemas.BasePlugin, error) {
	if pluginConfig == nil {
		return heimdall.Init(heimdall.Config{})
	}
	if m, ok := pluginConfig.(map[string]any); ok {
		pluginConfig = resolveHeimdallEnv(m)
	}
	cfg, err := MarshalPluginConfig[heimdall.Config](pluginConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal heimdall plugin config: %w", err)
	}
	if cfg == nil {
		cfg = &heimdall.Config{}
	}
	return heimdall.Init(*cfg)
}

// resolveHeimdallEnv returns a copy of v with every "env.NAME" string
// replaced by the value of NAME (empty when unset).
func resolveHeimdallEnv(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = resolveHeimdallEnv(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = resolveHeimdallEnv(val)
		}
		return out
	case string:
		if name, ok := strings.CutPrefix(t, "env."); ok && name != "" {
			return os.Getenv(name)
		}
		return t
	default:
		return v
	}
}
