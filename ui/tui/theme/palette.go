package theme

// palette.go — colour palette and lipgloss style definitions.
//
// Extracted from the parent ui/tui/theme.go to allow the visual primitives
// to be imported without pulling in the heavier rendering helpers.

import "github.com/charmbracelet/lipgloss"

// --- palette --------------------------------------------------------------

var (
	ColorPanelBorder = lipgloss.Color("#2F4F6A")
	ColorPanelBg     = lipgloss.Color("#0B1220")
	ColorTitleBg     = lipgloss.Color("#11B981")
	ColorTitleFg     = lipgloss.Color("#041014")
	ColorMuted       = lipgloss.Color("#93A4BF")
	ColorTabActiveBg = lipgloss.Color("#1E3A8A")
	ColorStatusBg    = lipgloss.Color("#111A2A")
	ColorStatusFg    = lipgloss.Color("#D9E6FF")

	ColorRoleUser      = lipgloss.Color("#8BC7FF")
	ColorRoleAssistant = lipgloss.Color("#8AF0CF")
	ColorRoleSystem    = lipgloss.Color("#F6D38A")
	ColorRoleTool      = lipgloss.Color("#C4A7FF")
	ColorRoleCoach     = lipgloss.Color("#F4B8D6")

	ColorOk     = lipgloss.Color("#6EE7A7")
	ColorFail   = lipgloss.Color("#FF8A8A")
	ColorWarn   = lipgloss.Color("#F6D38A")
	ColorInfo   = lipgloss.Color("#67E8F9")
	ColorAccent = lipgloss.Color("#BFA9FF")
	ColorCode   = lipgloss.Color("#F2E5A1")
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
	InputLineStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5F2FF"))

	BoldStyle   = lipgloss.NewStyle().Bold(true)
	CodeStyle   = lipgloss.NewStyle().Foreground(ColorCode)
	AccentStyle = lipgloss.NewStyle().Foreground(ColorAccent)
	OkStyle     = lipgloss.NewStyle().Foreground(ColorOk)
	FailStyle   = lipgloss.NewStyle().Foreground(ColorFail)
	WarnStyle   = lipgloss.NewStyle().Foreground(ColorWarn)
	InfoStyle   = lipgloss.NewStyle().Foreground(ColorInfo)
	ToolStyle   = lipgloss.NewStyle().Foreground(ColorRoleTool)

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

	DoneStyle     = lipgloss.NewStyle().Foreground(ColorOk)
	PendingStyle  = lipgloss.NewStyle().Foreground(ColorMuted)
	RunningStyle  = lipgloss.NewStyle().Foreground(ColorAccent)
	BlockedStyle  = lipgloss.NewStyle().Foreground(ColorFail)
	SkippedStyle  = lipgloss.NewStyle().Foreground(ColorWarn)
)
