package plugins

// registry.go — Provider factory registry for plugin-based provider loading.
//
// Instead of hardcoding provider construction in router_profile.go's
// switch statement, new providers register themselves here at init time.
// The loader reads config and invokes the appropriate factory.
//
// Architecture:
//   - Factory.BuildConfig returns a Config (no provider imports needed)
//   - router_profile.go does the actual provider construction using
//     existing NewXXX functions (no import cycle)
//
// Usage:
//   // In builtin.go:
//   func init() {
//       RegisterProvider(ProtocolAnthropic, Factory{
//           Protocol: ProtocolAnthropic,
//           BuildConfig: func(name string, profile config.ModelConfig) Config {
//               return Config{...}
//           },
//       })
//   }

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// Config holds provider configuration extracted from ModelConfig.
type Config struct {
	Name        string
	Model       string
	Models      []string
	APIKey      string
	BaseURL     string
	MaxTokens   int
	MaxContext  int
	HTTPTimeout int
	Protocol    string
}

// BestModel returns the primary model, falling back to the first in Models.
func (c Config) BestModel() string {
	if c.Model != "" {
		return c.Model
	}
	if len(c.Models) > 0 {
		return c.Models[0]
	}
	return ""
}

// Factory is a registered provider constructor helper.
// It extracts config from ModelConfig; actual provider construction
// happens in router_profile.go to avoid import cycles.
type Factory struct {
	Protocol       string
	DefaultBaseURL string
	SupportsTools  bool

	// BuildConfig extracts plugin.Config from a ModelConfig.
	// Router_profile.go uses this to get config, then constructs providers.
	BuildConfig func(name string, profile config.ModelConfig) Config
}

// globalRegistry is the package-level factory registry.
var globalRegistry = struct {
	mu        sync.RWMutex
	factories map[string]*Factory // protocol -> Factory
}{
	factories: make(map[string]*Factory),
}

// RegisterProvider registers a factory for a protocol.
// Panics if a factory is already registered for the same protocol.
func RegisterProvider(protocol string, f Factory) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if _, exists := globalRegistry.factories[protocol]; exists {
		panic(fmt.Sprintf("provider registry: protocol %q already registered", protocol))
	}
	globalRegistry.factories[protocol] = &f
}

// Get returns the factory for a protocol. Returns nil if not found.
func Get(protocol string) *Factory {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	return globalRegistry.factories[protocol]
}

// NormalizedProtocol maps provider name to protocol.
func NormalizedProtocol(name, protocol string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if protocol != "" {
		return strings.ToLower(strings.TrimSpace(protocol))
	}
	switch name {
	case "anthropic", "minimax":
		return ProtocolAnthropic
	case "openai":
		return ProtocolOpenAI
	case "google", "gemini":
		return ProtocolGoogle
	case "deepseek", "generic", "kimi", "zai", "alibaba", "ollama", "groq":
		return ProtocolOpenAICompatible
	default:
		return ""
	}
}

// Protocol constants
const (
	ProtocolAnthropic        = "anthropic"
	ProtocolGoogle           = "google"
	ProtocolOpenAI           = "openai"
	ProtocolOpenAICompatible = "openai-compatible"
)
