package styles

import "github.com/charmbracelet/lipgloss"

// Colors - matching coolify-tui palette
var (
	// Primary colors
	HighlightColor = lipgloss.Color("#00d4aa")
	WhiteColor     = lipgloss.Color("#ffffff")
	BlackColor     = lipgloss.Color("#000000")
	MutedColor     = lipgloss.Color("#6b7280")
	BorderColor    = lipgloss.Color("#3f3f46")

	// Background colors
	BreadcrumbBg = lipgloss.Color("#303030")
	CursorBg     = lipgloss.Color("#2d4f67")
	HeaderBg     = lipgloss.Color("#1a1a2e")

	// Status colors
	RunningColor   = lipgloss.Color("#00d4aa")
	StoppedColor   = lipgloss.Color("#6b7280")
	ErrorColor     = lipgloss.Color("#ef4444")
	PendingColor   = lipgloss.Color("#f59e0b")
	CompletedColor = lipgloss.Color("#10b981")

	// Git status tag colors
	DirtyColor  = lipgloss.Color("#f59e0b") // Yellow/orange
	BehindColor = lipgloss.Color("#3b82f6") // Blue

	// Tab colors
	NumKeyColor = lipgloss.Color("#00d4aa")
	FilterColor = lipgloss.Color("#3b82f6")
)

// Styles struct holds all styles
type Styles struct {
	// App
	App lipgloss.Style

	// Header
	Header     lipgloss.Style
	Logo       lipgloss.Style
	HeaderInfo lipgloss.Style

	// Title bar
	TitleBar    lipgloss.Style
	TitleText   lipgloss.Style
	TitleCount  lipgloss.Style
	TitleFilter lipgloss.Style
	TitleDash   lipgloss.Style

	// Table
	TableHeader      lipgloss.Style
	TableRow         lipgloss.Style
	TableRowSelected lipgloss.Style
	TableBorder      lipgloss.Style

	// Tabs
	Tab       lipgloss.Style
	ActiveTab lipgloss.Style
	TabNumKey lipgloss.Style

	// Footer/Breadcrumb
	Footer     lipgloss.Style
	Breadcrumb lipgloss.Style

	// Status
	StatusRunning   lipgloss.Style
	StatusStopped   lipgloss.Style
	StatusError     lipgloss.Style
	StatusPending   lipgloss.Style
	StatusCompleted lipgloss.Style

	// Modal
	Modal      lipgloss.Style
	ModalTitle lipgloss.Style

	// Input
	Input        lipgloss.Style
	InputFocused lipgloss.Style

	// Help
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style

	// Cursor
	Cursor lipgloss.Style

	// Muted
	Muted lipgloss.Style

	// Git status tags
	TagDirty  lipgloss.Style
	TagBehind lipgloss.Style

	// Key hints (subtle bar above footer)
	KeyHints lipgloss.Style

	// Command bar styles
	CommandKey    lipgloss.Style
	CommandAction lipgloss.Style

	// Status bar
	StatusBar       lipgloss.Style
	StatusSeparator lipgloss.Style
}

// DefaultStyles returns the default coolify-tui inspired styles
func DefaultStyles() *Styles {
	s := &Styles{}

	// App
	s.App = lipgloss.NewStyle()

	// Header
	s.Header = lipgloss.NewStyle().
		Background(HeaderBg).
		Padding(0, 1)

	s.Logo = lipgloss.NewStyle().
		Bold(true).
		Foreground(HighlightColor)

	s.HeaderInfo = lipgloss.NewStyle().
		Foreground(MutedColor)

	// Title bar
	s.TitleBar = lipgloss.NewStyle().
		Foreground(MutedColor)

	s.TitleText = lipgloss.NewStyle().
		Bold(true).
		Foreground(WhiteColor)

	s.TitleCount = lipgloss.NewStyle().
		Foreground(MutedColor)

	s.TitleFilter = lipgloss.NewStyle().
		Foreground(FilterColor)

	s.TitleDash = lipgloss.NewStyle().
		Foreground(BorderColor)

	// Table
	s.TableHeader = lipgloss.NewStyle().
		Bold(true).
		Foreground(MutedColor).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(BorderColor)

	s.TableRow = lipgloss.NewStyle().
		Foreground(WhiteColor)

	s.TableRowSelected = lipgloss.NewStyle().
		Foreground(WhiteColor).
		Background(CursorBg).
		Bold(true)

	s.TableBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderColor).
		Padding(0, 1)

	// Tabs
	s.Tab = lipgloss.NewStyle().
		Foreground(MutedColor)

	s.ActiveTab = lipgloss.NewStyle().
		Foreground(HighlightColor).
		Bold(true)

	s.TabNumKey = lipgloss.NewStyle().
		Foreground(NumKeyColor).
		Bold(true)

	// Footer/Breadcrumb
	s.Footer = lipgloss.NewStyle().
		Foreground(MutedColor)

	s.Breadcrumb = lipgloss.NewStyle().
		Foreground(HighlightColor).
		Background(BreadcrumbBg).
		Padding(0, 1).
		Bold(true)

	// Status
	s.StatusRunning = lipgloss.NewStyle().
		Foreground(RunningColor)

	s.StatusStopped = lipgloss.NewStyle().
		Foreground(StoppedColor)

	s.StatusError = lipgloss.NewStyle().
		Foreground(ErrorColor)

	s.StatusPending = lipgloss.NewStyle().
		Foreground(PendingColor)

	s.StatusCompleted = lipgloss.NewStyle().
		Foreground(CompletedColor)

	// Modal
	s.Modal = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(HighlightColor).
		Padding(1, 2)

	s.ModalTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(HighlightColor)

	// Input
	s.Input = lipgloss.NewStyle().
		Foreground(WhiteColor)

	s.InputFocused = lipgloss.NewStyle().
		Foreground(HighlightColor)

	// Help
	s.HelpKey = lipgloss.NewStyle().
		Foreground(HighlightColor).
		Bold(true)

	s.HelpDesc = lipgloss.NewStyle().
		Foreground(MutedColor)

	// Cursor
	s.Cursor = lipgloss.NewStyle().
		Foreground(HighlightColor).
		Bold(true)

	// Muted
	s.Muted = lipgloss.NewStyle().
		Foreground(MutedColor)

	// Git status tags
	s.TagDirty = lipgloss.NewStyle().
		Foreground(DirtyColor)

	s.TagBehind = lipgloss.NewStyle().
		Foreground(BehindColor)

	// Key hints (subtle, muted style)
	s.KeyHints = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4b5563")). // Dim gray - more subtle than MutedColor
		Padding(0, 0)

	// Command bar styles
	s.CommandKey = lipgloss.NewStyle().
		Foreground(HighlightColor).
		Bold(true)

	s.CommandAction = lipgloss.NewStyle().
		Foreground(MutedColor)

	// Status bar
	s.StatusBar = lipgloss.NewStyle().
		Foreground(MutedColor)

	s.StatusSeparator = lipgloss.NewStyle().
		Foreground(BorderColor)

	return s
}

// RenderKeyHelp renders a key and its description in coolify-tui style
func (s *Styles) RenderKeyHelp(key, desc string) string {
	return s.HelpKey.Render("<"+key+">") + " " + s.HelpDesc.Render(desc)
}

// RenderCommand renders a command key for the command bar (no brackets)
func (s *Styles) RenderCommand(key, action string) string {
	return s.CommandKey.Render(key) + " " + s.CommandAction.Render(action)
}

// RenderTab renders a tab in coolify-tui style
func (s *Styles) RenderTab(key, name string, active bool) string {
	numKey := s.TabNumKey.Render("<" + key + ">")
	if active {
		return numKey + " " + s.ActiveTab.Render(name)
	}
	return numKey + " " + s.Tab.Render(name)
}
