package tui

import "time"

// chatEventLine holds tool-call event data for the chat transcript.
type chatEventLine struct {
	Key      string
	Kind     string
	Status   string
	Title    string
	Detail   string
	At       time.Time
	Duration int

	// Tool call details for rich display
	ToolName      string
	Action        string
	ParamsPreview string
	Reason        string
	Step          int
	Round         int

	// Realtime log lines appended during execution
	RunningLog []string
}

type slashCommandItem struct {
	Command     string
	Template    string
	Description string
}

func mergeChatEventLine(old, next chatEventLine) chatEventLine {
	if next.Key == "" {
		next.Key = old.Key
	}
	if next.Kind == "" {
		next.Kind = old.Kind
	}
	if next.Status == "" {
		next.Status = old.Status
	}
	if next.Title == "" {
		next.Title = old.Title
	}
	if next.Detail == "" {
		next.Detail = old.Detail
	}
	if next.At.IsZero() {
		next.At = old.At
	}
	if next.Duration == 0 {
		next.Duration = old.Duration
	}
	if next.ToolName == "" {
		next.ToolName = old.ToolName
	}
	if next.Action == "" {
		next.Action = old.Action
	}
	if next.ParamsPreview == "" {
		next.ParamsPreview = old.ParamsPreview
	}
	if next.Reason == "" {
		next.Reason = old.Reason
	}
	if next.Step == 0 {
		next.Step = old.Step
	}
	if next.Round == 0 {
		next.Round = old.Round
	}
	if len(next.RunningLog) == 0 && len(old.RunningLog) > 0 {
		next.RunningLog = old.RunningLog
	}
	return next
}
