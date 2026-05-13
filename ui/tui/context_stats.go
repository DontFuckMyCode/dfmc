package tui

import (
	"fmt"
	"strings"
)

func (m Model) contextConversationStats() (messages int, toolCalls int, tokens int) {
	if m.eng == nil || m.eng.ConversationActive() == nil {
		return 0, 0, 0
	}
	for _, msg := range m.eng.ConversationActive().Messages() {
		messages++
		toolCalls += len(msg.ToolCalls)
		tokens += contextMessageTokens(msg)
	}
	return messages, toolCalls, tokens
}

func (m Model) contextWindowLimitSource(providerName, modelName string, maxContext int) string {
	providerName = strings.TrimSpace(providerName)
	modelName = strings.TrimSpace(modelName)
	if providerName == "" {
		return ""
	}
	if m.eng == nil || m.eng.Config == nil {
		if maxContext > 0 {
			return "provider runtime"
		}
		return ""
	}
	prof, ok := m.eng.Config.Providers.Profiles[providerName]
	if !ok {
		if maxContext > 0 {
			return "provider runtime"
		}
		return ""
	}
	if modelName == "" {
		modelName = strings.TrimSpace(prof.Model)
	}
	if catalogID := strings.TrimSpace(prof.CatalogID); catalogID != "" && modelName != "" {
		if meta, ok := catalogModelForRef(catalogID, modelName); ok && meta.Limit.Context > 0 {
			if maxContext <= 0 || meta.Limit.Context == maxContext {
				return fmt.Sprintf("models.dev %s/%s", catalogID, modelName)
			}
			if prof.MaxContext > 0 && prof.MaxContext == maxContext {
				return fmt.Sprintf("profile override %s; models.dev %s", compactTokenForSource(maxContext), compactTokenForSource(meta.Limit.Context))
			}
			return fmt.Sprintf("runtime %s; models.dev %s", compactTokenForSource(maxContext), compactTokenForSource(meta.Limit.Context))
		}
		return "models.dev catalog ref"
	}
	if prof.MaxContext > 0 && (modelName == "" || strings.EqualFold(strings.TrimSpace(prof.Model), modelName)) {
		return "profile max_context"
	}
	if maxContext > 0 {
		return "provider runtime"
	}
	return ""
}

func compactTokenForSource(tokens int) string {
	if tokens <= 0 {
		return "0"
	}
	switch {
	case tokens%1_000_000 == 0:
		return fmt.Sprintf("%dM", tokens/1_000_000)
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens%1000 == 0:
		return fmt.Sprintf("%dk", tokens/1000)
	case tokens >= 10_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}
