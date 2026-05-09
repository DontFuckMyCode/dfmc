package theme

// palette.go — colour palette and lipgloss style definitions.
//
// Extracted from the parent ui/tui/theme.go to allow the visual primitives
// to be imported without pulling in the heavier rendering helpers.

import "github.com/charmbracelet/lipgloss"

// --- palette --------------------------------------------------------------

var (
	ColorPanelBorder = lipgloss.Color("#2A3241")
	ColorPanelBg     = lipgloss.Color("#0D1117")
	ColorTitleBg     = lipgloss.Color("#3B82F6")
	ColorTitleFg     = lipgloss.Color("#F8FAFC")
	ColorMuted       = lipgloss.Color("#64748B")
	ColorTabActiveBg = lipgloss.Color("#1E293B")
	ColorStatusBg    = lipgloss.Color("#161B22")
	ColorStatusFg    = lipgloss.Color("#94A3B8")
	ColorHeaderBg    = lipgloss.Color("#1E293B")
	ColorHeaderFg    = lipgloss.Color("#F8FAFC")

	ColorRoleUser      = lipgloss.Color("#60A5FA")
	ColorRoleAssistant = lipgloss.Color("#34D399")
	ColorRoleSystem    = lipgloss.Color("#FBBF24")
	ColorRoleTool      = lipgloss.Color("#A78BFA")
	ColorRoleCoach     = lipgloss.Color("#F472B6")

	ColorOk     = lipgloss.Color("#10B981")
	ColorFail   = lipgloss.Color("#EF4444")
	ColorWarn   = lipgloss.Color("#F59E0B")
	ColorInfo   = lipgloss.Color("#0EA5E9")
	ColorAccent = lipgloss.Color("#818CF8")
	ColorCode   = lipgloss.Color("#E2E8F0")

	// Tab-accent colours used by the TUI's per-tab palette in
	// ui/tui/tui_palette.go. Defined here so every hex literal lives
	// under theme/, which is the P14 invariant: zero hex literals
	// outside theme/. Names match the tab labels they paint so a
	// retune is one-grep-away.
	ColorTabPatch         = lipgloss.Color("#FF9F6A")
	ColorTabCodeMap       = lipgloss.Color("#5EEAD4")
	ColorTabConversations = lipgloss.Color("#FFB4B4")
	ColorTabPlans         = lipgloss.Color("#A5B4FC")
	ColorTabContext       = lipgloss.Color("#BEF264")
	ColorTabProviders     = lipgloss.Color("#F0ABFC")
	ColorTabOrchestrate   = lipgloss.Color("#FDE68A")
	ColorTabShortcuts     = lipgloss.Color("#A7F3D0")

	// Misc one-off colours that previously lived as inline literals.
	ColorInputForeground = lipgloss.Color("#E5F2FF")
	ColorDisabledFg      = lipgloss.Color("#5A6A82")
)

// --- styles ---------------------------------------------------------------

var (
	TitleStyle = lipgloss.NewStyle().
			Foreground(ColorTitleFg).
			Background(ColorTitleBg).
			Padding(0, 1).
			Bold(true)

	SubtleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	SectionTitleStyle = lipgloss.NewStyle().
				Foreground(ColorInfo).
				Bold(true)

	StatusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(ColorStatusFg).
			Background(ColorStatusBg)

	UserLineStyle      = lipgloss.NewStyle().Foreground(ColorRoleUser)
	AssistantLineStyle = lipgloss.NewStyle().Foreground(ColorRoleAssistant)
	SystemLineStyle    = lipgloss.NewStyle().Foreground(ColorRoleSystem)
	CoachLineStyle     = lipgloss.NewStyle().Foreground(ColorRoleCoach).Italic(true)
	InputLineStyle     = lipgloss.NewStyle().Foreground(ColorInputForeground)

	BoldStyle     = lipgloss.NewStyle().Bold(true)
	CodeStyle     = lipgloss.NewStyle().Foreground(ColorCode)
	AccentStyle   = lipgloss.NewStyle().Foreground(ColorAccent)
	OkStyle       = lipgloss.NewStyle().Foreground(ColorOk)
	FailStyle     = lipgloss.NewStyle().Foreground(ColorFail)
	WarnStyle     = lipgloss.NewStyle().Foreground(ColorWarn)
	InfoStyle     = lipgloss.NewStyle().Foreground(ColorInfo)
	ToolStyle     = lipgloss.NewStyle().Foreground(ColorRoleTool)
	ToolLineStyle = lipgloss.NewStyle().Foreground(ColorPanelBg).Background(ColorRoleTool)
	DisabledStyle = lipgloss.NewStyle().Foreground(ColorDisabledFg)

	BadgeUserStyle      = lipgloss.NewStyle().Foreground(ColorTitleFg).Background(ColorRoleUser).Padding(0, 1).Bold(true)
	BadgeAssistantStyle = lipgloss.NewStyle().Foreground(ColorTitleFg).Background(ColorRoleAssistant).Padding(0, 1).Bold(true)
	BadgeSystemStyle    = lipgloss.NewStyle().Foreground(ColorTitleFg).Background(ColorRoleSystem).Padding(0, 1).Bold(true)
	BadgeToolStyle      = lipgloss.NewStyle().Foreground(ColorTitleFg).Background(ColorRoleTool).Padding(0, 1).Bold(true)
	BadgeCoachStyle     = lipgloss.NewStyle().Foreground(ColorTitleFg).Background(ColorRoleCoach).Padding(0, 1).Bold(true)

	InputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorRoleUser).
			Padding(0, 1)

	ResumeBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorWarn).
				Padding(0, 1)

	MentionPickerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorAccent).
				Padding(0, 1)

	MentionSelectedRowStyle = lipgloss.NewStyle().
				Foreground(ColorTitleFg).
				Background(ColorAccent).
				Bold(true).
				Padding(0, 1)

	DividerStyle = lipgloss.NewStyle().Foreground(ColorPanelBorder)

	BannerStyle = lipgloss.NewStyle().
			Foreground(ColorTitleBg).
			Bold(true)

	DoneStyle    = lipgloss.NewStyle().Foreground(ColorOk)
	PendingStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	RunningStyle = lipgloss.NewStyle().Foreground(ColorAccent)
	BlockedStyle = lipgloss.NewStyle().Foreground(ColorFail)
	SkippedStyle = lipgloss.NewStyle().Foreground(ColorWarn)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(ColorHeaderFg).
			Background(ColorHeaderBg).
			Padding(0, 1)
)
