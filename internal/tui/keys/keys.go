package keys

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines all keybindings
type KeyMap struct {
	Up                   key.Binding
	Down                 key.Binding
	Enter                key.Binding
	Back                 key.Binding
	Quit                 key.Binding
	Help                 key.Binding
	Filter               key.Binding
	Create               key.Binding
	Archive              key.Binding
	Open                 key.Binding
	OpenCursor           key.Binding
	OpenVSCode           key.Binding
	OpenTerminal         key.Binding
	Refresh              key.Binding
	Add                  key.Binding
	Delete               key.Binding
	Tab                  key.Binding
	Ports                key.Binding
	MergeReqs            key.Binding
	AllPRs               key.Binding
	AutoSetupClaude      key.Binding
	Retry                key.Binding
	CreateWorktreeFromPR key.Binding
	Tunnel               key.Binding
	CopyURL              key.Binding
	ArchivedList         key.Binding
	StatusHistory        key.Binding
	DatabaseSync            key.Binding
	DatabaseSyncForce       key.Binding
	DatabaseList            key.Binding
	DatabaseReinit          key.Binding
	DatabaseMigrationStatus key.Binding
	DatabaseLogs            key.Binding
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
		Tunnel: key.NewBinding(
			key.WithKeys("T"),
			key.WithHelp("T", "tunnel"),
		),
		CopyURL: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy URL"),
		),
		ArchivedList: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "archived list"),
		),
		StatusHistory: key.NewBinding(
			key.WithKeys("H"),
			key.WithHelp("H", "message history"),
		),
		DatabaseSync: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "sync database"),
		),
		DatabaseSyncForce: key.NewBinding(
			key.WithKeys("F"),
			key.WithHelp("F", "force sync"),
		),
		DatabaseList: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "databases"),
		),
		DatabaseReinit: key.NewBinding(
			key.WithKeys("I"),
			key.WithHelp("I", "reinit DB"),
		),
		DatabaseMigrationStatus: key.NewBinding(
			key.WithKeys("B"),
			key.WithHelp("B", "migration status"),
		),
		DatabaseLogs: key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "sync logs"),
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
		{k.Tunnel, k.CopyURL, k.DatabaseList, k.DatabaseSync, k.DatabaseReinit, k.DatabaseMigrationStatus},
		{k.Help, k.Quit},
	}
}

// KeyGroup represents a logical group of keybindings
type KeyGroup struct {
	Name string
	Keys []key.Binding
}

// KeyGroups returns keybindings organized by logical groups
func (k KeyMap) KeyGroups() []KeyGroup {
	return []KeyGroup{
		{
			Name: "Navigation",
			Keys: []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Tab},
		},
		{
			Name: "Views",
			Keys: []key.Binding{k.Ports, k.DatabaseList, k.ArchivedList, k.StatusHistory},
		},
		{
			Name: "Actions",
			Keys: []key.Binding{k.Create, k.Archive, k.Delete, k.Retry, k.Refresh, k.Add},
		},
		{
			Name: "Open",
			Keys: []key.Binding{k.Open, k.OpenTerminal, k.OpenCursor, k.OpenVSCode},
		},
		{
			Name: "GitHub",
			Keys: []key.Binding{k.MergeReqs, k.AllPRs, k.AutoSetupClaude, k.CreateWorktreeFromPR},
		},
		{
			Name: "Tunnels",
			Keys: []key.Binding{k.Tunnel, k.CopyURL},
		},
		{
			Name: "Database",
			Keys: []key.Binding{k.DatabaseSync, k.DatabaseSyncForce, k.DatabaseList, k.DatabaseReinit, k.DatabaseMigrationStatus, k.DatabaseLogs},
		},
		{
			Name: "Utility",
			Keys: []key.Binding{k.Help, k.Filter, k.Quit},
		},
	}
}
