package keys

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines all keybindings
type KeyMap struct {
	Up              key.Binding
	Down            key.Binding
	Enter           key.Binding
	Back            key.Binding
	Quit            key.Binding
	Help            key.Binding
	Filter          key.Binding
	Create          key.Binding
	Archive         key.Binding
	Open            key.Binding
	OpenCursor      key.Binding
	OpenVSCode      key.Binding
	OpenTerminal    key.Binding
	Refresh         key.Binding
	Add             key.Binding
	Delete          key.Binding
	Tab             key.Binding
	Ports                key.Binding
	MergeReqs            key.Binding
	AllPRs               key.Binding
	AutoSetupClaude      key.Binding
	Retry                key.Binding
	CreateWorktreeFromPR key.Binding
}

// DefaultKeyMap returns the default keybindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc", "backspace"),
			key.WithHelp("esc", "back"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Create: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "create"),
		),
		Archive: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "archive"),
		),
		Open: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open"),
		),
		OpenCursor: key.NewBinding(
			key.WithKeys("C"),
			key.WithHelp("C", "cursor"),
		),
		OpenVSCode: key.NewBinding(
			key.WithKeys("V"),
			key.WithHelp("V", "vscode"),
		),
		OpenTerminal: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "terminal"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r", "ctrl+r"),
			key.WithHelp("r", "refresh"),
		),
		Add: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d", "x"),
			key.WithHelp("d", "delete"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next"),
		),
		Ports: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "ports"),
		),
		MergeReqs: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "merge requests"),
		),
		AllPRs: key.NewBinding(
			key.WithKeys("M"),
			key.WithHelp("M", "all PRs"),
		),
		AutoSetupClaude: key.NewBinding(
			key.WithKeys("A"),
			key.WithHelp("A", "auto-setup claude PRs"),
		),
		Retry: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "retry failed setup"),
		),
		CreateWorktreeFromPR: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "create worktree"),
		),
	}
}

// ShortHelp returns keybindings to show in the help view
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

// FullHelp returns keybindings for the full help view
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Create, k.Archive, k.Delete, k.Retry},
		{k.Open, k.OpenCursor, k.OpenVSCode, k.OpenTerminal},
		{k.Filter, k.Refresh, k.Ports, k.MergeReqs, k.AllPRs, k.AutoSetupClaude},
		{k.Help, k.Quit},
	}
}
