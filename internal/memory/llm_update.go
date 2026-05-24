package memory

import (
	"context"
	"encoding/json"
	"strings"
)

// SuggestedEntry is what the LLM proposes to remember from a turn.
type SuggestedEntry struct {
	Key        string  `json:"key"`
	Value     string  `json:"value"`
	Category  string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Tier      string  `json:"tier"` // "episodic" or "semantic"
}

// parseSuggestedEntries extracts a JSON array from the LLM's text response.
// The model may return the array bare or wrapped in markdown fences.
// Returns an empty slice (no error) when nothing parseable is found.
func parseSuggestedEntries(text string) ([]SuggestedEntry, error) {
	trimmed := strings.TrimSpace(text)

	// Strip markdown code fences
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	// Try direct parse first
	var entries []SuggestedEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err == nil {
		return entries, nil
	}

	// Try to find JSON array brackets within the text
	start := strings.Index(trimmed, "[")
	end := strings.LastIndex(trimmed, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, nil // no parseable JSON array found
	}

	var entries2 []SuggestedEntry
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &entries2); err == nil {
		return entries2, nil
	}

	return nil, nil // no parseable JSON array found
}

// CallWithPrompt calls the LLM updater with a reflection prompt built
// from the turn's question and answer, then parses any suggested entries.
// threshold is the minimum confidence (0.0–1.0) to accept an entry.
// Returns the number of entries added (0 if nothing worthwhile).
// The caller provides the LLMUpdater so this method stays provider-independent.
func CallWithPrompt(ctx context.Context, llmUpdater LLMUpdater, question, answer string, threshold float64) ([]SuggestedEntry, error) {
	prompt := "You just answered this question in a coding session:\n\n" +
		"Q: " + question + "\n\n" +
		"A: " + answer + "\n\n" +
		"Should any of this be remembered for future sessions? " +
		"Respond ONLY with a JSON array of memory entries to add to persistent memory, " +
		"or [] if nothing is worth remembering. " +
		"Each entry must have: key (short question/topic phrase), value (concise answer or finding), " +
		"category (one word: pattern|fact|todo|decision|context), confidence (0.0-1.0). " +
		`Example: [{"key":"postgres jsonb performance","value":"Use GIN indexes for jsonb contains queries","category":"fact","confidence":0.85}]`

	resp, err := llmUpdater.Call(ctx, "", "", prompt)
	if err != nil || resp == "" {
		return nil, nil // best-effort
	}

	entries, err := parseSuggestedEntries(resp)
	if err != nil || len(entries) == 0 {
		return nil, nil // best-effort
	}

	valid := make([]SuggestedEntry, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Key) == "" || strings.TrimSpace(e.Value) == "" {
			continue
		}
		if e.Tier != "episodic" && e.Tier != "semantic" {
			e.Tier = "episodic"
		}
		if e.Confidence == 0 {
			e.Confidence = 0.7
		}
		if e.Confidence < threshold {
			continue
		}
		valid = append(valid, e)
	}

	return valid, nil
}