package config

import "strings"

// ProviderProfileAdvisories returns non-fatal warnings for known provider
// profile misconfigurations. These are deliberately advisories rather than
// hard validation errors because the runtime may be able to self-heal them,
// but doctor/status surfaces should still tell the user what is off.
func ProviderProfileAdvisories(name string, prof ModelConfig) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "zai":
		protocol := strings.ToLower(strings.TrimSpace(prof.Protocol))
		baseURL := strings.ToLower(strings.TrimSpace(prof.BaseURL))
		if protocol == "anthropic" || strings.Contains(baseURL, "/api/anthropic") {
			return []string{"zai anthropic-style config is unreliable in DFMC; prefer protocol=openai-compatible and base_url=https://api.z.ai/api/paas/v4"}
		}
	}
	return nil
}
