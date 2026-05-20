// subagent_memory.go — per-role journal that lets a delegate_task
// sub-agent see what previous delegations to the same role have
// already concluded. Closes the dfmc_next_step.md item "Sub-agent
// memory persistence."
//
// Why per-role and not per-conversation: a "researcher" delegation
// kicked off twice on the same project benefits from seeing the
// first run's findings whether it happened five turns ago or in a
// previous CLI invocation; the role label is the stable handle the
// model already uses to invoke the sub-agent. Per-conversation
// scoping would force the model to re-derive the same context every
// session, which is exactly what this feature exists to avoid.
//
// The journal is best-effort: a missing Storage handle, an open-tx
// failure, or a corrupt JSON value all silently degrade to "no
// prior context." Sub-agents must still work without it — the
// feature is a memory aid, not a correctness requirement.

package engine

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

const (
	// subagentJournalBucket holds one JSON list per role key.
	subagentJournalBucket = "subagent_journal"
	// subagentJournalCap caps the on-disk list length per role. Keep
	// it small: each entry is injected verbatim into the next
	// sub-agent's user prompt, so the cap is also a token budget on
	// the prompt overhead. Five entries × ~1 KiB each ≈ 1.5K tokens
	// worst-case, which is acceptable for any context window we
	// target.
	subagentJournalCap = 5
	// subagentJournalSummaryMax bounds the summary excerpt; longer
	// completion text is truncated with "...". Models routinely
	// produce 5-10 KiB summaries, which would dominate a fresh
	// sub-agent's context if injected raw.
	subagentJournalSummaryMax = 1000
	// subagentJournalTaskMax bounds the prior task line — we only
	// need a recognisable handle, not the full prompt.
	subagentJournalTaskMax = 200
)

// subagentJournalEntry is one row in the per-role journal. Stored
// as JSON inside the subagent_journal bucket; the schema is
// deliberately loose (additive fields are tolerated by the standard
// json decoder) so we can add fields without a migration.
type subagentJournalEntry struct {
	Timestamp time.Time `json:"ts"`
	Task      string    `json:"task,omitempty"`
	Summary   string    `json:"summary"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Parked    bool      `json:"parked,omitempty"`
}

// loadSubagentJournal returns prior journal entries for the given
// role (lowercased, trimmed). Returns nil when the role is empty,
// Storage is unset (tests), or no entries exist yet — callers must
// treat all three as the same "no prior context" case.
func (e *Engine) loadSubagentJournal(role string) []subagentJournalEntry {
	key := strings.ToLower(strings.TrimSpace(role))
	if key == "" || e == nil || e.Storage == nil || e.Storage.DB() == nil {
		return nil
	}
	var out []subagentJournalEntry
	data, err := e.Storage.BucketGet(subagentJournalBucket, key)
	if err != nil {
		return nil
	}
	if data == nil {
		return nil
	}
	// Corrupt JSON → silently fall back to no entries; a future
	// append will overwrite the bad row. We deliberately don't
	// log: the journal is best-effort and noise here would be
	// worse than the missing context.
	_ = json.Unmarshal(data, &out)
	return out
}

// appendSubagentJournal records one delegation outcome and trims
// the per-role list to subagentJournalCap (oldest entries fall off).
// Empty role or empty summary skips the write — there is nothing
// useful to inject from those cases.
func (e *Engine) appendSubagentJournal(role string, entry subagentJournalEntry) {
	key := strings.ToLower(strings.TrimSpace(role))
	if key == "" || e == nil || e.Storage == nil || e.Storage.DB() == nil {
		return
	}
	entry.Task = truncateString(strings.TrimSpace(entry.Task), subagentJournalTaskMax)
	entry.Summary = truncateString(strings.TrimSpace(entry.Summary), subagentJournalSummaryMax)
	if entry.Summary == "" {
		return
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	var existing []subagentJournalEntry
	if data, _ := e.Storage.BucketGet(subagentJournalBucket, key); data != nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing = append(existing, entry)
	if len(existing) > subagentJournalCap {
		existing = existing[len(existing)-subagentJournalCap:]
	}
	encoded, err := json.Marshal(existing)
	if err != nil {
		return
	}
	_ = e.Storage.BucketPut(subagentJournalBucket, key, encoded)
}

// formatSubagentJournalSection renders prior entries as a single
// prompt block. Returns "" when entries is empty so callers can
// blindly concatenate without a length check.
func formatSubagentJournalSection(entries []subagentJournalEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Prior delegations to this role (oldest first; you wrote these summaries on earlier turns — use them, don't redo the work):\n")
	for i, ent := range entries {
		b.WriteString("- #")
		b.WriteString(itoaInt(i + 1))
		if !ent.Timestamp.IsZero() {
			b.WriteString(" [")
			b.WriteString(ent.Timestamp.UTC().Format("2006-01-02 15:04 UTC"))
			b.WriteString("]")
		}
		if ent.Parked {
			b.WriteString(" [parked]")
		}
		if ent.Task != "" {
			b.WriteString(" task: ")
			b.WriteString(ent.Task)
		}
		b.WriteString("\n  → ")
		b.WriteString(security.RedactSecrets(ent.Summary))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
