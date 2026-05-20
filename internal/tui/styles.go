package tui

import "charm.land/lipgloss/v2"

// Shared color palette for ds4go TUI components. All hex colors used by the
// CLI live here so the theme stays consistent across pickers, prompts, and
// progress rendering.
var (
	// ColorAccent is the brand mint used for titles, active items, and progress fills.
	ColorAccent = lipgloss.Color("#5FBE9E")
	// ColorPrimary is the default body-text color on dark terminals.
	ColorPrimary = lipgloss.Color("#C9D1D9")
	// ColorMuted is the dimmed gray used for help text and secondary info.
	// Kept readable on dark terminals (Ghostty, etc.) — clearly subordinate to
	// ColorPrimary but not so dim it disappears.
	ColorMuted = lipgloss.Color("#8a929b")
	// ColorDark is the near-black used as foreground when sitting on top of ColorAccent.
	ColorDark = lipgloss.Color("#0B1411")
	// ColorSurface is the dark gray used as a filler/background for unfilled regions.
	ColorSurface = lipgloss.Color("#30363D")
)

// Shared styles. Define once here so pickers, confirms, progress bars, and
// any future components stay visually consistent.
var (
	TitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	ActiveStyle  = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	PrimaryStyle = lipgloss.NewStyle().Foreground(ColorPrimary)
	MutedStyle   = lipgloss.NewStyle().Foreground(ColorMuted)

	// SelectedStyle renders a filled "chip" — dark text on accent background —
	// so the focused option is immediately obvious even on low-contrast terminals.
	SelectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorDark).
			Background(ColorAccent).
			Padding(0, 2)

	// UnselectedBox keeps the same width/padding as SelectedStyle so toggling
	// between options doesn't cause layout shift.
	UnselectedBox = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Padding(0, 2)

	// ProgressDone is the filled portion of a progress bar.
	ProgressDone = lipgloss.NewStyle().Bold(true).Foreground(ColorDark).Background(ColorAccent)
	// ProgressRest is the unfilled portion of a progress bar.
	ProgressRest = lipgloss.NewStyle().Foreground(ColorPrimary).Background(ColorSurface)
)
