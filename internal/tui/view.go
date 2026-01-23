package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tui/styles"
)

// View renders the current state
func (m *Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var sections []string

	// Header
	sections = append(sections, m.renderHeader())

	// Tabs
	sections = append(sections, m.renderTabs())

	// Title bar
	sections = append(sections, m.renderTitleBar())

	// Main content - for modal views, render the background content first
	switch m.currentView {
	case ViewProjects:
		sections = append(sections, m.renderProjectsTable())
	case ViewWorktrees:
		sections = append(sections, m.renderWorktreesTable())
	case ViewPorts:
		sections = append(sections, m.renderPortsTable())
	case ViewCreateWorktree:
		// Render worktree table as background
		sections = append(sections, m.renderWorktreesTable())
	case ViewConfirmDelete:
		// Render previous view as background
		if m.prevView == ViewWorktrees {
			sections = append(sections, m.renderWorktreesTable())
		} else {
			sections = append(sections, m.renderProjectsTable())
		}
	case ViewConfirmDbReinit:
		// Render worktrees table as background (reinit is always from worktrees view)
		sections = append(sections, m.renderWorktreesTable())
	case ViewHelp:
		// Render previous view as background
		switch m.prevView {
		case ViewWorktrees:
			sections = append(sections, m.renderWorktreesTable())
		case ViewPorts:
			sections = append(sections, m.renderPortsTable())
		default:
			sections = append(sections, m.renderProjectsTable())
		}
	case ViewLogs:
		sections = append(sections, m.renderLogsView())
	case ViewPRs:
		sections = append(sections, m.renderPRsPage())
	case ViewAllPRs:
		sections = append(sections, m.renderAllPRsPage())
	case ViewTunnelModal:
		sections = append(sections, m.renderWorktreesTable())
	case ViewArchivedList:
		sections = append(sections, m.renderArchivedListPage())
	case ViewStatusHistory:
		sections = append(sections, m.renderStatusHistoryPage())
	case ViewDatabases:
		sections = append(sections, m.renderDatabasesTable())
	case ViewDatabaseLogs:
		sections = append(sections, m.renderDatabaseLogsView())
	}

	// Status bar (with separator above)
	sections = append(sections, m.renderFooter())

	// Command bar (at the very bottom)
	sections = append(sections, m.renderKeyHints())

	// Join all sections into base view
	baseView := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Overlay modal if in a modal view
	switch m.currentView {
	case ViewCreateWorktree:
		return m.overlayModal(baseView, m.renderCreateWorktreeModal())
	case ViewConfirmDelete:
		return m.overlayModal(baseView, m.renderConfirmDeleteModal())
	case ViewConfirmDbReinit:
		return m.overlayModal(baseView, m.renderConfirmDbReinitModal())
	case ViewHelp:
		return m.overlayModal(baseView, m.renderHelpModal())
	case ViewQuit:
		return m.overlayModal(baseView, m.renderQuitModal())
	case ViewTunnelModal:
		return m.overlayModal(baseView, m.renderTunnelModal())
	case ViewBranchRename:
		return m.overlayModal(baseView, m.renderBranchRenameModal())
	}

	return baseView
}

func (m *Model) renderHeader() string {
	// Logo
	logo := m.styles.Logo.Render("⚡ CONDUCTOR")

	// Info
	info := ""
	totalPorts := m.config.TotalUsedPorts()
	projectCount := len(m.config.Projects)
	info = m.styles.HeaderInfo.Render(fmt.Sprintf("  %d projects • %d ports", projectCount, totalPorts))

	// Version info (right side)
	versionStr := fmt.Sprintf("v%s", m.version)
	if m.updateAvailable {
		versionStr = fmt.Sprintf("v%s → v%s ✨", m.version, m.latestVersion)
	} else if m.updateDownloaded {
		versionStr = fmt.Sprintf("v%s (updated, restart) ✓", m.version)
	}
	versionInfo := m.styles.HeaderInfo.Render(versionStr)

	// Build header line
	left := logo + info
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(versionInfo)

	// Middle spacing
	spacing := m.width - leftWidth - rightWidth - 2
	if spacing < 0 {
		spacing = 0
	}

	header := left + strings.Repeat(" ", spacing) + versionInfo
	return m.styles.Header.Width(m.width).Render(header)
}

func (m *Model) renderTabs() string {
	var tabs []string

	switch m.currentView {
	case ViewProjects, ViewWorktrees, ViewPorts, ViewDatabases:
		tabs = append(tabs, m.styles.RenderTab("1", "projects", m.currentView == ViewProjects))
		if m.selectedProject != "" {
			tabs = append(tabs, m.styles.RenderTab("2", "worktrees", m.currentView == ViewWorktrees))
		}
		tabs = append(tabs, m.styles.RenderTab("p", "ports", m.currentView == ViewPorts))
		tabs = append(tabs, m.styles.RenderTab("3", "databases", m.currentView == ViewDatabases))
	}

	return "  " + strings.Join(tabs, "  ")
}

func (m *Model) renderTitleBar() string {
	var title string
	var count int

	switch m.currentView {
	case ViewProjects:
		title = "PROJECTS"
		count = len(m.projectNames)
	case ViewWorktrees:
		title = "WORKTREES"
		count = len(m.worktreeNames)
	case ViewPorts:
		title = "PORTS"
		count = len(m.config.GetAllPortInfo())
	case ViewCreateWorktree:
		title = "CREATE WORKTREE"
		count = 0
	case ViewConfirmDelete:
		title = "CONFIRM"
		count = 0
	case ViewConfirmDbReinit:
		title = "CONFIRM REINIT"
		count = 0
	case ViewHelp:
		title = "HELP"
		count = 0
	case ViewLogs:
		if m.logsType == "archive" {
			title = "ARCHIVE LOGS: " + m.logsWorktree
		} else {
			title = "SETUP LOGS: " + m.logsWorktree
		}
		count = 0
	case ViewPRs:
		title = "MERGE REQUESTS: " + m.prWorktree
		count = len(m.prList)
	case ViewAllPRs:
		title = "ALL PULL REQUESTS"
		count = len(m.allPRList)
	case ViewArchivedList:
		if m.archivedListMode == 0 {
			title = "ARCHIVED WORKTREES"
			count = len(m.archivedWorktrees)
		} else {
			title = "ORPHANED BRANCHES"
			count = len(m.orphanedBranches)
		}
	case ViewStatusHistory:
		title = "MESSAGE HISTORY"
		count = len(m.statusHistory)
	case ViewDatabases:
		title = "DATABASES"
		count = len(m.databaseProjects)
	}

	// Build title: ─────── TITLE(count) ───────
	titleText := m.styles.TitleText.Render(title)
	if count > 0 {
		titleText += m.styles.TitleCount.Render(fmt.Sprintf("(%d)", count))
	}

	// Add filter indicator
	if m.filterMode || m.filter != "" {
		filterText := m.styles.TitleFilter.Render(" /" + m.filter)
		if m.filterMode {
			filterText += m.styles.Cursor.Render("█")
		}
		titleText += filterText
	}

	titleWidth := lipgloss.Width(titleText)
	dashWidth := (m.width - titleWidth - 4) / 2
	if dashWidth < 0 {
		dashWidth = 0
	}

	dashes := m.styles.TitleDash.Render(strings.Repeat("─", dashWidth))
	return dashes + " " + titleText + " " + dashes
}

func (m *Model) renderProjectsTable() string {
	if len(m.projectNames) == 0 {
		empty := m.styles.Muted.Render("No projects registered. Use 'conductor project add' to add a project.")
		return m.padContent(empty)
	}

	// Column widths
	nameW := 20
	pathW := m.width - nameW - 15 - 12 - 6 // Remaining space for path
	if pathW < 20 {
		pathW = 20
	}
	wtW := 12
	portW := 15

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s",
		nameW, "NAME",
		pathW, "PATH",
		wtW, "WORKTREES",
		portW, "PORTS")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.offset
	end := start + tableHeight
	if end > len(m.projectNames) {
		end = len(m.projectNames)
	}

	// Rows
	for i := start; i < end; i++ {
		name := m.projectNames[i]
		project := m.config.Projects[name]
		ports := m.config.GetProjectPorts(name)

		// Format port range
		portRange := "-"
		if len(ports) > 0 {
			if len(ports) == 1 {
				portRange = fmt.Sprintf("%d", ports[0])
			} else {
				portRange = fmt.Sprintf("%d-%d", ports[0], ports[len(ports)-1])
			}
		}

		// Truncate path
		path := project.Path
		if len(path) > pathW {
			path = "..." + path[len(path)-pathW+3:]
		}

		// Count worktrees (excluding root)
		wtCount := 0
		for _, wt := range project.Worktrees {
			if !wt.IsRoot {
				wtCount++
			}
		}

		// Build row content (without cursor)
		rowContent := fmt.Sprintf("%-*s  %-*s  %-*d  %-*s",
			nameW, truncate(name, nameW),
			pathW, path,
			wtW, wtCount,
			portW, portRange)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2) // -2 for cursor space

		if i == m.cursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderWorktreesTable() string {
	project := m.config.Projects[m.selectedProject]
	if project == nil || len(m.worktreeNames) == 0 {
		empty := m.styles.Muted.Render("No worktrees found. Press 'c' to create one.")
		return m.padContent(empty)
	}

	// Column widths
	nameW := 15
	portW := 12
	statusW := 28 // Widened to accommodate git status tags
	createdW := 14
	prW := 12
	branchW := m.width - nameW - portW - statusW - createdW - prW - 14 // Remaining space for branch
	if branchW < 15 {
		branchW = 15
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
		nameW, "NAME",
		branchW, "BRANCH",
		portW, "PORTS",
		statusW, "STATUS",
		createdW, "CREATED",
		prW, "PR")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.offset
	end := start + tableHeight
	if end > len(m.worktreeNames) {
		end = len(m.worktreeNames)
	}

	// Rows
	for i := start; i < end; i++ {
		name := m.worktreeNames[i]
		wt := project.Worktrees[name]
		if wt == nil {
			// Worktree was deleted but list not yet refreshed
			continue
		}

		// Format port range
		portRange := "-"
		if len(wt.Ports) > 0 {
			if len(wt.Ports) == 1 {
				portRange = fmt.Sprintf("%d", wt.Ports[0])
			} else {
				portRange = fmt.Sprintf("%d-%d", wt.Ports[0], wt.Ports[len(wt.Ports)-1])
			}
		}

		// Status based on setup state, archive state, or archived state
		status := "ready"
		statusTags := ""
		if wt.ArchiveStatus == config.ArchiveStatusRunning {
			status = m.spinner.View() + " archiving"
		} else if wt.Archived {
			status = "archived"
		} else {
			switch wt.SetupStatus {
			case config.SetupStatusCreating:
				status = m.spinner.View() + " creating"
			case config.SetupStatusRunning:
				status = m.spinner.View() + " setting up"
			case config.SetupStatusFailed:
				status = "✗ failed"
			case config.SetupStatusDone:
				status = "ready"
				// Add tunnel indicator for ready worktrees
				if wt.Tunnel != nil && wt.Tunnel.Active {
					// Truncate URL for display
					tunnelURL := wt.Tunnel.URL
					if len(tunnelURL) > 25 {
						tunnelURL = tunnelURL[len(tunnelURL)-22:]
						tunnelURL = "..." + tunnelURL
					}
					statusTags += " " + m.styles.StatusRunning.Render("["+tunnelURL+"]")
				}
				// Add git status tags for ready worktrees
				if gitStatus, ok := m.gitStatusCache[name]; ok {
					if gitStatus.IsDirty {
						statusTags += " " + m.styles.TagDirty.Render("[dirty]")
					}
					if gitStatus.CommitsBehind > 0 {
						statusTags += " " + m.styles.TagBehind.Render(fmt.Sprintf("[behind %d]", gitStatus.CommitsBehind))
					}
				}
			}
		}

		// Show archived date instead of created date for archived worktrees
		dateStr := wt.CreatedAt.Format("Jan 2, 15:04")
		if wt.Archived {
			dateStr = wt.ArchivedAt.Format("Jan 2, 15:04")
		}

		// PR column - show most recent PR
		prStr := "-"
		if len(wt.PRs) > 0 {
			pr := wt.PRs[0] // Most recent
			prStr = fmt.Sprintf("#%d %s", pr.Number, pr.State)
		}

		// Build row content (without cursor)
		displayName := name
		if wt.Archived {
			displayName = "◦" + name // Add ◦ prefix for archived
		}

		// Build status column with tags
		// The tags have ANSI colors, so we need to handle padding carefully
		statusWithTags := status + statusTags
		// Calculate the visible length (without ANSI codes) for padding
		statusVisibleLen := len(status)
		if statusTags != "" {
			// Count visible characters in tags: "[dirty]" = 7, "[behind N]" = 9+ chars
			if gitStatus, ok := m.gitStatusCache[name]; ok {
				if gitStatus.IsDirty {
					statusVisibleLen += 8 // " [dirty]"
				}
				if gitStatus.CommitsBehind > 0 {
					statusVisibleLen += 10 + len(fmt.Sprintf("%d", gitStatus.CommitsBehind)) // " [behind N]"
				}
			}
		}
		// Pad the status to fill the column width
		statusPadding := statusW - statusVisibleLen
		if statusPadding > 0 {
			statusWithTags += strings.Repeat(" ", statusPadding)
		}

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %s  %-*s  %-*s",
			nameW, truncate(displayName, nameW),
			branchW, truncate(wt.Branch, branchW),
			portW, portRange,
			statusWithTags,
			createdW, dateStr,
			prW, truncate(prStr, prW))

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.cursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else if wt.Archived {
			// Dim archived worktrees
			rows = append(rows, m.styles.Muted.Render("  "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderPortsTable() string {
	portInfo := m.config.GetAllPortInfo()

	if len(portInfo) == 0 {
		empty := m.styles.Muted.Render("No ports allocated.")
		return m.padContent(empty)
	}

	// Column widths
	portW := 8
	projectW := 20
	wtW := 15
	indexW := 8
	labelW := 15

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s",
		portW, "PORT",
		projectW, "PROJECT",
		wtW, "WORKTREE",
		indexW, "INDEX",
		labelW, "LABEL")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.offset
	end := start + tableHeight
	if end > len(portInfo) {
		end = len(portInfo)
	}

	// Rows
	for i := start; i < end; i++ {
		p := portInfo[i]

		label := p.Label
		if label == "" {
			label = "-"
		}

		// Build row content (without cursor)
		rowContent := fmt.Sprintf("%-*d  %-*s  %-*s  %-*d  %-*s",
			portW, p.Port,
			projectW, truncate(p.Project, projectW),
			wtW, truncate(p.Worktree, wtW),
			indexW, p.Index,
			labelW, label)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.cursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

// CommandKey represents a key-action pair for the command bar
type CommandKey struct {
	Key    string
	Action string
}

// getContextKeys returns the relevant keybindings for the current view
func (m *Model) getContextKeys() []CommandKey {
	switch m.currentView {
	case ViewProjects:
		return []CommandKey{{"enter", "select"}, {"d", "delete"}, {"p", "ports"}, {"3", "databases"}, {"?", "help"}, {"q", "quit"}}
	case ViewWorktrees:
		return []CommandKey{{"c", "create"}, {"a", "archive"}, {"o", "open"}, {"C", "cursor"}, {"T", "tunnel"}, {"m", "PRs"}, {"?", "help"}}
	case ViewPorts:
		return []CommandKey{{"1", "projects"}, {"3", "databases"}, {"?", "help"}, {"esc", "back"}}
	case ViewDatabases:
		return []CommandKey{{"S", "sync"}, {"F", "force sync"}, {"l", "logs"}, {"1", "projects"}, {"p", "ports"}, {"?", "help"}, {"esc", "back"}}
	case ViewDatabaseLogs:
		return []CommandKey{{"j/k", "scroll"}, {"a", "auto-scroll"}, {"g/G", "top/bottom"}, {"esc", "back"}}
	case ViewPRs:
		return []CommandKey{{"o", "open"}, {"w", "worktree"}, {"r", "refresh"}, {"?", "help"}, {"esc", "back"}}
	case ViewAllPRs:
		return []CommandKey{{"enter", "worktree"}, {"o", "open"}, {"r", "refresh"}, {"?", "help"}, {"esc", "back"}}
	case ViewArchivedList:
		if m.archivedListMode == 0 {
			return []CommandKey{{"tab", "orphaned"}, {"l", "logs"}, {"d", "delete"}, {"r", "refresh"}, {"esc", "back"}}
		}
		return []CommandKey{{"tab", "archived"}, {"d", "delete"}, {"r", "refresh"}, {"esc", "back"}}
	case ViewStatusHistory:
		return []CommandKey{{"c", "clear"}, {"?", "help"}, {"esc", "back"}}
	case ViewCreateWorktree:
		return []CommandKey{{"enter", "create"}, {"tab", "next"}, {"esc", "cancel"}}
	case ViewConfirmDelete:
		return []CommandKey{{"enter", "confirm"}, {"esc", "cancel"}}
	case ViewConfirmDbReinit:
		return []CommandKey{{"y", "yes"}, {"n", "no"}, {"esc", "cancel"}}
	case ViewTunnelModal:
		return []CommandKey{{"enter", "start"}, {"tab", "switch"}, {"esc", "cancel"}}
	default:
		return []CommandKey{{"?", "help"}, {"q", "quit"}}
	}
}

// renderKeyHints renders the command bar at the bottom
func (m *Model) renderKeyHints() string {
	commands := m.getContextKeys()

	var parts []string
	for _, cmd := range commands {
		parts = append(parts, m.styles.RenderCommand(cmd.Key, cmd.Action))
	}
	line := strings.Join(parts, "   ")

	// Truncate if too long (calculate visible width without ANSI codes)
	// For now, just return the line - lipgloss handles truncation
	return "  " + line
}

func (m *Model) renderFooter() string {
	var left, right string

	// Left: Breadcrumb navigation
	var breadcrumbs []string
	switch m.currentView {
	case ViewProjects:
		breadcrumbs = append(breadcrumbs, "projects")
	case ViewWorktrees:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.selectedProject)
	case ViewPorts:
		breadcrumbs = append(breadcrumbs, "ports")
	case ViewCreateWorktree:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.selectedProject)
		breadcrumbs = append(breadcrumbs, "create")
	case ViewConfirmDelete:
		breadcrumbs = append(breadcrumbs, "confirm")
	case ViewConfirmDbReinit:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.dbReinitProject)
		breadcrumbs = append(breadcrumbs, "reinit-db")
	case ViewPRs:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.selectedProject)
		breadcrumbs = append(breadcrumbs, m.prWorktree)
		breadcrumbs = append(breadcrumbs, "prs")
	case ViewAllPRs:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.selectedProject)
		breadcrumbs = append(breadcrumbs, "all-prs")
	case ViewArchivedList:
		breadcrumbs = append(breadcrumbs, "projects")
		breadcrumbs = append(breadcrumbs, m.selectedProject)
		if m.archivedListMode == 0 {
			breadcrumbs = append(breadcrumbs, "archived")
		} else {
			breadcrumbs = append(breadcrumbs, "orphaned-branches")
		}
	case ViewStatusHistory:
		breadcrumbs = append(breadcrumbs, "history")
	case ViewDatabases:
		breadcrumbs = append(breadcrumbs, "databases")
	case ViewDatabaseLogs:
		breadcrumbs = append(breadcrumbs, "databases")
		breadcrumbs = append(breadcrumbs, m.databaseLogsProject)
		breadcrumbs = append(breadcrumbs, "logs")
	}

	for i, bc := range breadcrumbs {
		if i == len(breadcrumbs)-1 {
			left += m.styles.Breadcrumb.Render(bc)
		} else {
			left += m.styles.Breadcrumb.Render(bc) + " "
		}
	}

	// Right: Status message with icon
	if m.statusMessage != "" {
		if m.statusIsError {
			right = lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("✗ " + m.statusMessage)
		} else {
			right = m.styles.Muted.Render("✓ " + m.statusMessage)
		}
	}

	// Calculate spacing
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	spacing := m.width - leftWidth - rightWidth - 2
	if spacing < 0 {
		spacing = 0
	}

	// Build separator line
	separator := m.styles.StatusSeparator.Render(strings.Repeat("─", m.width))

	// Build status bar content
	statusBar := " " + left + strings.Repeat(" ", spacing) + right

	return separator + "\n" + statusBar
}

func (m *Model) renderCreateWorktreeModal() string {
	width := 60
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Create Worktree"))
	content.WriteString("\n\n")

	// Show error if present
	if m.createError != "" {
		errorStyle := lipgloss.NewStyle().Foreground(styles.ErrorColor)
		content.WriteString(errorStyle.Render("  Error: " + m.createError))
		content.WriteString("\n\n")
	}

	// Branch input
	branchLabel := "Branch name:"
	if m.createFocused == 0 {
		branchLabel = m.styles.Cursor.Render("► ") + branchLabel
	} else {
		branchLabel = "  " + branchLabel
	}
	content.WriteString(branchLabel + "\n")
	content.WriteString("  " + m.createInput.View())
	content.WriteString("\n\n")

	// Port count input
	portLabel := "Ports to allocate:"
	if m.createFocused == 1 {
		portLabel = m.styles.Cursor.Render("► ") + portLabel
	} else {
		portLabel = "  " + portLabel
	}
	content.WriteString(portLabel + "\n")
	content.WriteString("  " + m.createPortInput.View())
	content.WriteString("\n\n")

	// Hints
	content.WriteString(m.styles.Muted.Render("  Leave empty for defaults"))
	content.WriteString("\n\n")

	// Actions
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("enter", "create"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("tab", "next"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "cancel"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return modal
}

func (m *Model) renderConfirmDeleteModal() string {
	width := 50
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	switch m.deleteTargetType {
	case "worktree":
		content.WriteString(m.styles.ModalTitle.Render("Confirm Archive"))
		content.WriteString("\n\n")
		content.WriteString(fmt.Sprintf("  Archive worktree '%s'?\n\n", m.deleteTarget))
		content.WriteString(m.styles.Muted.Render("  This will remove the git worktree and free its ports.\n"))
		content.WriteString(m.styles.Muted.Render("  The entry will remain for viewing logs."))
	case "worktree-delete":
		content.WriteString(m.styles.ModalTitle.Render("Confirm Delete"))
		content.WriteString("\n\n")
		content.WriteString(fmt.Sprintf("  Permanently delete '%s'?\n\n", m.deleteTarget))
		content.WriteString(m.styles.Muted.Render("  This will remove the worktree entry and its logs."))
	default:
		content.WriteString(m.styles.ModalTitle.Render("Confirm Delete"))
		content.WriteString("\n\n")
		content.WriteString(fmt.Sprintf("  Remove project '%s'?\n\n", m.deleteTarget))
		content.WriteString(m.styles.Muted.Render("  This will free all ports. Files will NOT be deleted."))
	}

	content.WriteString("\n\n  ")
	content.WriteString(m.styles.RenderKeyHelp("y", "yes"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("n", "no"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return modal
}

func (m *Model) renderConfirmDbReinitModal() string {
	width := 55
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Confirm Database Reinit"))
	content.WriteString("\n\n")
	content.WriteString(fmt.Sprintf("  Reinitialize database '%s'?\n\n", m.dbReinitDBName))
	content.WriteString(m.styles.Muted.Render("  This will DROP the existing database and clone\n"))
	content.WriteString(m.styles.Muted.Render("  fresh data from the golden database.\n\n"))
	content.WriteString(m.styles.StatusError.Render("  ⚠ All local changes will be lost!"))

	content.WriteString("\n\n  ")
	content.WriteString(m.styles.RenderKeyHelp("y", "yes"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("n", "no"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return modal
}

func (m *Model) renderHelpModal() string {
	width := 70
	if width > m.width-4 {
		width = m.width - 4
	}

	// Build all help lines first
	var allLines []string

	// Use KeyGroups() as single source of truth
	for _, group := range m.keyMap.KeyGroups() {
		allLines = append(allLines, m.styles.StatusRunning.Render("  "+group.Name))
		for _, k := range group.Keys {
			help := k.Help()
			allLines = append(allLines, fmt.Sprintf("    %s %s",
				m.styles.HelpKey.Render(fmt.Sprintf("%-10s", help.Key)),
				m.styles.Muted.Render(help.Desc)))
		}
		allLines = append(allLines, "") // Empty line between groups
	}

	// Calculate available height for content (modal height - title - footer - padding)
	modalHeight := m.height - 8 // Leave room for modal chrome
	if modalHeight < 10 {
		modalHeight = 10
	}
	contentHeight := modalHeight - 6 // Title (2 lines) + footer (2 lines) + margins

	// Clamp scroll
	maxScroll := len(allLines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}

	// Get visible lines
	start := m.helpScroll
	end := start + contentHeight
	if end > len(allLines) {
		end = len(allLines)
	}
	visibleLines := allLines[start:end]

	// Build content
	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Keyboard Shortcuts"))
	content.WriteString("\n\n")

	for _, line := range visibleLines {
		content.WriteString(line)
		content.WriteString("\n")
	}

	// Scroll indicator
	if len(allLines) > contentHeight {
		scrollInfo := fmt.Sprintf("  %d-%d of %d", start+1, end, len(allLines))
		content.WriteString("\n")
		content.WriteString(m.styles.Muted.Render(scrollInfo + "  j/k scroll  g/G top/bottom"))
		content.WriteString("\n")
	}

	content.WriteString("\n  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "close"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return modal
}

func (m *Model) renderQuitModal() string {
	width := 40
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Quit Conductor"))
	content.WriteString("\n\n")
	content.WriteString(m.styles.Muted.Render("  Choose an action:"))
	content.WriteString("\n\n")

	// Kill all option
	killLabel := "  Kill All"
	killDesc := "Stop all tmux windows and exit"
	if m.quitFocused == 0 {
		content.WriteString(m.styles.Cursor.Render("► "))
		content.WriteString(m.styles.TableRowSelected.Render(killLabel))
		content.WriteString("\n")
		content.WriteString("    " + m.styles.Muted.Render(killDesc))
	} else {
		content.WriteString("  " + killLabel)
		content.WriteString("\n")
		content.WriteString("    " + m.styles.Muted.Render(killDesc))
	}
	content.WriteString("\n\n")

	// Detach option
	detachLabel := "  Detach"
	detachDesc := "Exit TUI, keep windows running"
	if m.quitFocused == 1 {
		content.WriteString(m.styles.Cursor.Render("► "))
		content.WriteString(m.styles.TableRowSelected.Render(detachLabel))
		content.WriteString("\n")
		content.WriteString("    " + m.styles.Muted.Render(detachDesc))
	} else {
		content.WriteString("  " + detachLabel)
		content.WriteString("\n")
		content.WriteString("    " + m.styles.Muted.Render(detachDesc))
	}
	content.WriteString("\n\n")

	// Actions
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("enter", "confirm"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "cancel"))

	modal := m.styles.Modal.Width(width).Render(content.String())
	return modal
}

func (m *Model) renderPRsPage() string {
	if m.prLoading {
		loading := m.spinner.View() + " Fetching PRs from GitHub..."
		return m.padContent(loading)
	}

	if len(m.prList) == 0 {
		empty := m.styles.Muted.Render("No pull requests found for this branch.")
		return m.padContent(empty)
	}

	// Build a map of branch -> worktree name for quick lookup
	branchToWorktree := make(map[string]string)
	if project, ok := m.config.GetProject(m.selectedProject); ok {
		for wtName, wt := range project.Worktrees {
			if !wt.Archived {
				branchToWorktree[wt.Branch] = wtName
			}
		}
	}

	// Column widths for PR table
	numW := 8
	stateW := 12
	authorW := 15
	updatedW := 14
	worktreeW := 12
	titleW := m.width - numW - stateW - authorW - updatedW - worktreeW - 14 // Remaining for title
	if titleW < 20 {
		titleW = 20
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
		numW, "NUMBER",
		titleW, "TITLE",
		stateW, "STATE",
		authorW, "AUTHOR",
		updatedW, "UPDATED",
		worktreeW, "WORKTREE")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.offset
	end := start + tableHeight
	if end > len(m.prList) {
		end = len(m.prList)
	}

	// PR rows
	for i := start; i < end; i++ {
		pr := m.prList[i]
		numStr := fmt.Sprintf("#%d", pr.Number)

		// State with indicator
		stateStr := pr.State
		switch pr.State {
		case "merged":
			stateStr = "✓ merged"
		case "closed":
			stateStr = "✗ closed"
		case "draft":
			stateStr = "◦ draft"
		case "open":
			stateStr = "● open"
		}

		// Format updated date
		updatedStr := pr.UpdatedAt.Format("Jan 2, 15:04")

		// Check if worktree exists for this PR's branch
		worktreeStr := "-"
		if wtName, exists := branchToWorktree[pr.HeadBranch]; exists {
			worktreeStr = wtName
		}

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
			numW, numStr,
			titleW, truncate(pr.Title, titleW),
			stateW, stateStr,
			authorW, truncate(pr.Author, authorW),
			updatedW, updatedStr,
			worktreeW, truncate(worktreeStr, worktreeW))

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.prCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderAllPRsPage() string {
	if m.allPRLoading {
		loading := m.spinner.View() + " Fetching all PRs from GitHub..."
		return m.padContent(loading)
	}

	if len(m.allPRList) == 0 {
		empty := m.styles.Muted.Render("No pull requests found for this repository.")
		return m.padContent(empty)
	}

	// Column widths for PR table
	numW := 8
	stateW := 12
	authorW := 15
	branchW := 25
	updatedW := 14
	titleW := m.width - numW - stateW - authorW - branchW - updatedW - 14 // Remaining for title
	if titleW < 15 {
		titleW = 15
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
		numW, "NUMBER",
		titleW, "TITLE",
		branchW, "BRANCH",
		stateW, "STATE",
		authorW, "AUTHOR",
		updatedW, "UPDATED")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.offset
	end := start + tableHeight
	if end > len(m.allPRList) {
		end = len(m.allPRList)
	}

	// Check which branches already have worktrees
	project := m.config.Projects[m.selectedProject]
	existingBranches := make(map[string]bool)
	if project != nil {
		for _, wt := range project.Worktrees {
			if !wt.Archived {
				existingBranches[wt.Branch] = true
			}
		}
	}

	// PR rows
	for i := start; i < end; i++ {
		pr := m.allPRList[i]
		numStr := fmt.Sprintf("#%d", pr.Number)

		// State with indicator
		stateStr := pr.State
		switch pr.State {
		case "merged":
			stateStr = "✓ merged"
		case "closed":
			stateStr = "✗ closed"
		case "draft":
			stateStr = "◦ draft"
		case "open":
			stateStr = "● open"
		}

		// Format updated date
		updatedStr := pr.UpdatedAt.Format("Jan 2, 15:04")

		// Check if worktree already exists for this branch
		branchDisplay := truncate(pr.HeadBranch, branchW)
		if existingBranches[pr.HeadBranch] {
			branchDisplay = truncate(pr.HeadBranch, branchW-4) + " [✓]"
		}

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
			numW, numStr,
			titleW, truncate(pr.Title, titleW),
			branchW, branchDisplay,
			stateW, stateStr,
			authorW, truncate(pr.Author, authorW),
			updatedW, updatedStr)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.allPRCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

// overlayModal overlays a modal on top of the base view
// The modal is centered both horizontally and vertically within the full view
func (m *Model) overlayModal(baseView string, modal string) string {
	baseLines := strings.Split(baseView, "\n")
	modalLines := strings.Split(modal, "\n")

	// Ensure we have enough base lines
	for len(baseLines) < m.height {
		baseLines = append(baseLines, "")
	}

	// Calculate center position for modal
	modalHeight := len(modalLines)
	modalWidth := lipgloss.Width(modal)

	topPad := (m.height - modalHeight) / 2
	if topPad < 0 {
		topPad = 0
	}

	leftPad := (m.width - modalWidth) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	// Overlay modal lines onto base
	for i, modalLine := range modalLines {
		baseIdx := topPad + i
		if baseIdx < 0 || baseIdx >= len(baseLines) {
			continue
		}

		// Build new line: padding + modal line
		// The lines where the modal appears will replace the base content
		// Lines above/below the modal will show the original base content
		padding := strings.Repeat(" ", leftPad)
		baseLines[baseIdx] = padding + modalLine
	}

	return strings.Join(baseLines, "\n")
}

func (m *Model) padContent(content string) string {
	lines := strings.Split(content, "\n")
	tableHeight := m.tableHeight()

	// Pad to fill available height
	for len(lines) < tableHeight {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func (m *Model) renderLogsView() string {
	logs := m.getCurrentLogs()

	if logs == "" {
		var emptyMsg string
		if m.logsType == "archive" {
			emptyMsg = "No archive logs available for this worktree."
		} else {
			emptyMsg = "No setup logs available for this worktree."
		}
		empty := m.styles.Muted.Render(emptyMsg)
		return m.padContent(empty)
	}

	// Split logs into lines
	lines := strings.Split(logs, "\n")

	// Calculate visible area
	viewHeight := m.tableHeight()

	// Calculate max scroll
	maxScroll := len(lines) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Apply auto-scroll if enabled
	if m.logsAutoScroll {
		m.logsScroll = maxScroll
	}

	// Apply scroll offset
	start := m.logsScroll
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		start = len(lines) - 1
		if start < 0 {
			start = 0
		}
	}

	end := start + viewHeight
	if end > len(lines) {
		end = len(lines)
	}

	visibleLines := lines[start:end]

	// Add line numbers and format
	var formatted []string
	for i, line := range visibleLines {
		lineNum := start + i + 1
		// Truncate long lines
		maxLineWidth := m.width - 8 // space for line number
		if len(line) > maxLineWidth {
			line = line[:maxLineWidth-3] + "..."
		}
		formatted = append(formatted, fmt.Sprintf("%4d  %s", lineNum, line))
	}

	// Pad to fill height
	for len(formatted) < viewHeight {
		formatted = append(formatted, "")
	}

	// Add scroll indicator with auto-scroll status
	autoScrollStatus := ""
	if m.logsAutoScroll {
		autoScrollStatus = " [AUTO-SCROLL ON]"
	}
	// Check if this is an archived worktree to show toggle hint
	toggleHint := ""
	project := m.config.Projects[m.selectedProject]
	if wt, ok := project.Worktrees[m.logsWorktree]; ok && wt.Archived {
		toggleHint = ", t: toggle setup/archive"
	}
	scrollInfo := fmt.Sprintf("Lines %d-%d of %d%s (j/k/mouse: scroll, a: toggle auto-scroll%s, esc: close)", start+1, end, len(lines), autoScrollStatus, toggleHint)
	formatted = append(formatted, "")
	formatted = append(formatted, m.styles.Muted.Render(scrollInfo))

	return strings.Join(formatted, "\n")
}

func (m *Model) renderTunnelModal() string {
	width := 55
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Start Tunnel"))
	content.WriteString("\n\n")

	// Show selected worktree and port
	if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
		wtName := m.worktreeNames[m.cursor]
		content.WriteString(fmt.Sprintf("  Worktree: %s\n", wtName))
		content.WriteString(fmt.Sprintf("  Port: %d\n\n", m.tunnelModalPort))
	}

	// Check cloudflared auth status for named tunnel
	namedAvailable := m.tunnelManager.IsCloudflaredAuthenticated()
	domainConfigured := m.config.Defaults.Tunnel.Domain != ""

	// Mode selection
	quickLabel := "Quick Tunnel (random URL)"
	namedLabel := "Named Tunnel (custom domain)"

	if m.tunnelModalMode == 0 {
		content.WriteString(m.styles.Cursor.Render("► "))
		content.WriteString(m.styles.TableRowSelected.Render(quickLabel))
	} else {
		content.WriteString("  ")
		content.WriteString(quickLabel)
	}
	content.WriteString("\n")

	if m.tunnelModalMode == 1 {
		content.WriteString(m.styles.Cursor.Render("► "))
		content.WriteString(m.styles.TableRowSelected.Render(namedLabel))
	} else {
		content.WriteString("  ")
		content.WriteString(namedLabel)
	}

	// Show named tunnel status
	if !namedAvailable {
		content.WriteString(" ")
		content.WriteString(lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("(not logged in)"))
	} else if !domainConfigured {
		content.WriteString(" ")
		content.WriteString(lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("(no domain)"))
	}
	content.WriteString("\n\n")

	// Descriptions
	content.WriteString(m.styles.Muted.Render("  Quick: No setup, random trycloudflare.com URL"))
	content.WriteString("\n")
	if namedAvailable && domainConfigured {
		content.WriteString(m.styles.Muted.Render(fmt.Sprintf("  Named: Uses %s domain", m.config.Defaults.Tunnel.Domain)))
	} else if !namedAvailable {
		content.WriteString(m.styles.Muted.Render("  Named: Run 'cloudflared tunnel login' first"))
	} else {
		content.WriteString(m.styles.Muted.Render("  Named: Set tunnel.domain in config"))
	}
	content.WriteString("\n\n")

	// Actions
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("enter", "start"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "cancel"))

	return m.styles.Modal.Width(width).Render(content.String())
}

func (m *Model) renderBranchRenameModal() string {
	width := 70
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Branch Already Checked Out"))
	content.WriteString("\n\n")

	// Show conflict info
	content.WriteString(m.styles.Muted.Render("  The branch '"))
	content.WriteString(m.styles.TableRowSelected.Render(m.branchRenameOriginal))
	content.WriteString(m.styles.Muted.Render("' is already checked out at:\n"))
	content.WriteString("  ")
	content.WriteString(m.styles.Muted.Render(m.branchRenameConflict))
	content.WriteString("\n\n")

	content.WriteString(m.styles.Muted.Render("  Enter a new branch name to create a worktree based on the original branch:\n\n"))

	// Input field
	content.WriteString("  ")
	content.WriteString(m.branchRenameInput.View())
	content.WriteString("\n\n")

	// Actions
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("enter", "create"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "cancel"))

	return m.styles.Modal.Width(width).Render(content.String())
}

func (m *Model) renderArchivedListPage() string {
	if m.archivedListMode == 0 {
		return m.renderArchivedWorktreesTable()
	}
	return m.renderOrphanedBranchesTable()
}

func (m *Model) renderArchivedWorktreesTable() string {
	if len(m.archivedWorktrees) == 0 {
		empty := m.styles.Muted.Render("No archived worktrees found.")
		return m.padContent(empty)
	}

	// Column widths
	nameW := 15
	branchW := 30
	archivedW := 16
	logsW := 12
	errorW := m.width - nameW - branchW - archivedW - logsW - 12

	if errorW < 20 {
		errorW = 20
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s",
		nameW, "NAME",
		branchW, "BRANCH",
		archivedW, "ARCHIVED AT",
		logsW, "LOGS",
		errorW, "ERROR")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.archivedListOffset
	end := start + tableHeight
	if end > len(m.archivedWorktrees) {
		end = len(m.archivedWorktrees)
	}

	// Rows
	for i := start; i < end; i++ {
		wt := m.archivedWorktrees[i]

		// Format archived date
		archivedStr := wt.ArchivedAt.Format("Jan 2, 15:04")

		// Format logs column
		logsStr := ""
		if wt.HasSetupLogs {
			logsStr += "S"
		}
		if wt.HasArchLogs {
			if logsStr != "" {
				logsStr += "/"
			}
			logsStr += "A"
		}
		if logsStr == "" {
			logsStr = "-"
		}

		// Error column
		errorStr := "-"
		if wt.ArchiveError != "" {
			errorStr = truncate(wt.ArchiveError, errorW)
		}

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s",
			nameW, truncate(wt.Name, nameW),
			branchW, truncate(wt.Branch, branchW),
			archivedW, archivedStr,
			logsW, logsStr,
			errorW, errorStr)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.archivedListCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else if wt.ArchiveError != "" {
			// Highlight error rows
			rows = append(rows, lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("  "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderOrphanedBranchesTable() string {
	if m.orphanedLoading {
		loading := m.spinner.View() + " Scanning for orphaned branches..."
		return m.padContent(loading)
	}

	if len(m.orphanedBranches) == 0 {
		empty := m.styles.Muted.Render("No orphaned branches found. All local branches have associated worktrees.")
		return m.padContent(empty)
	}

	// Column widths
	branchW := 40
	commitW := 10
	dateW := 20
	statusW := m.width - branchW - commitW - dateW - 10

	if statusW < 20 {
		statusW = 20
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s",
		branchW, "BRANCH",
		commitW, "COMMIT",
		dateW, "DATE",
		statusW, "STATUS")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.archivedListOffset
	end := start + tableHeight
	if end > len(m.orphanedBranches) {
		end = len(m.orphanedBranches)
	}

	// Rows
	for i := start; i < end; i++ {
		branch := m.orphanedBranches[i]

		// Status column
		statusStr := "orphaned"
		if branch.CheckedOutAt != "" {
			statusStr = "checked out at " + truncate(branch.CheckedOutAt, statusW-15)
		}

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s",
			branchW, truncate(branch.Branch, branchW),
			commitW, branch.LastCommit,
			dateW, truncate(branch.CommitDate, dateW),
			statusW, truncate(statusStr, statusW))

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.archivedListCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else if branch.CheckedOutAt != "" {
			// Highlight checked out branches
			rows = append(rows, lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("  "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderStatusHistoryPage() string {
	if len(m.statusHistory) == 0 {
		empty := m.styles.Muted.Render("No message history.")
		return m.padContent(empty)
	}

	// Column widths
	timeW := 14
	typeW := 8
	msgW := m.width - timeW - typeW - 8

	if msgW < 30 {
		msgW = 30
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s",
		timeW, "TIME",
		typeW, "TYPE",
		msgW, "MESSAGE")
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.statusHistoryOffset
	end := start + tableHeight
	if end > len(m.statusHistory) {
		end = len(m.statusHistory)
	}

	// Rows
	for i := start; i < end; i++ {
		item := m.statusHistory[i]

		// Format time (show relative time)
		timeStr := item.Timestamp.Format("15:04:05")

		// Type column
		typeStr := "info"
		if item.IsError {
			typeStr = "error"
		}

		// Message (truncate if needed)
		msgStr := truncate(item.Message, msgW)

		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s",
			timeW, timeStr,
			typeW, typeStr,
			msgW, msgStr)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.statusHistoryCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else if item.IsError {
			// Highlight error messages
			rows = append(rows, lipgloss.NewStyle().Foreground(styles.ErrorColor).Render("  "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

func (m *Model) renderDatabasesTable() string {
	// Build list of projects with database config
	m.databaseProjects = m.databaseProjects[:0]
	for name, project := range m.config.Projects {
		if project.Database != nil && project.Database.Source != "" {
			m.databaseProjects = append(m.databaseProjects, name)
		}
	}

	// Sort for consistent ordering
	sort.Strings(m.databaseProjects)

	if len(m.databaseProjects) == 0 {
		empty := m.styles.Muted.Render("No projects with database config. Use 'conductor db set-source <project> <url>' to configure.")
		return m.padContent(empty)
	}

	// Column widths (dynamic based on terminal width)
	nameW := 18
	statusW := 12
	lastSyncW := 18
	sizeW := 10
	tablesW := 10
	// Source gets remaining space
	sourceW := m.width - nameW - statusW - lastSyncW - sizeW - tablesW - 14 // 14 for spacing and margins
	if sourceW < 20 {
		sourceW = 20
	}

	var rows []string

	// Header - full width
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
		nameW, "PROJECT",
		sourceW, "SOURCE",
		statusW, "STATUS",
		lastSyncW, "LAST SYNC",
		sizeW, "SIZE",
		tablesW, "TABLES")
	header = padRight(header, m.width-2)
	rows = append(rows, m.styles.TableHeader.Render(header))

	// Calculate visible rows
	tableHeight := m.tableHeight()
	start := m.databaseOffset
	end := start + tableHeight
	if end > len(m.databaseProjects) {
		end = len(m.databaseProjects)
	}

	// Rows
	for i := start; i < end; i++ {
		name := m.databaseProjects[i]
		project := m.config.Projects[name]
		dbConfig := project.Database

		// Mask source URL for display
		sourceDisplay := "-"
		if dbConfig.Source != "" {
			// Just show host/db, mask everything else
			if info, err := parseDBSource(dbConfig.Source); err == nil {
				sourceDisplay = truncate(fmt.Sprintf("%s/%s", info.host, info.db), sourceW)
			} else {
				sourceDisplay = truncate("configured", sourceW)
			}
		}

		// Status - show progress if syncing
		status := "never"
		if m.databaseSyncing[name] {
			if progress, ok := m.databaseProgress[name]; ok && progress != "" {
				// Truncate progress for display in status column
				status = truncate(progress, statusW)
			} else {
				status = "syncing..."
			}
		} else if dbConfig.SyncStatus != nil {
			if dbConfig.SyncStatus.Status != "" {
				status = dbConfig.SyncStatus.Status
			}
		}

		// Last sync time
		lastSync := "-"
		if dbConfig.SyncStatus != nil && dbConfig.SyncStatus.LastSyncAt != "" {
			lastSync = dbConfig.SyncStatus.LastSyncAt
		}

		// Size
		sizeStr := "-"
		if dbConfig.SyncStatus != nil && dbConfig.SyncStatus.GoldenCopySize > 0 {
			sizeStr = formatSize(dbConfig.SyncStatus.GoldenCopySize)
		}

		// Tables
		tablesStr := "-"
		if dbConfig.SyncStatus != nil && dbConfig.SyncStatus.TableCount > 0 {
			if dbConfig.SyncStatus.ExcludedCount > 0 {
				tablesStr = fmt.Sprintf("%d (-%d)", dbConfig.SyncStatus.TableCount, dbConfig.SyncStatus.ExcludedCount)
			} else {
				tablesStr = fmt.Sprintf("%d", dbConfig.SyncStatus.TableCount)
			}
		}

		// Build row content
		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s",
			nameW, truncate(name, nameW),
			sourceW, sourceDisplay,
			statusW, status,
			lastSyncW, lastSync,
			sizeW, sizeStr,
			tablesW, tablesStr)

		// Pad to full width
		rowContent = padRight(rowContent, m.width-2)

		if i == m.databaseCursor {
			rows = append(rows, m.styles.TableRowSelected.Width(m.width).Render("> "+rowContent))
		} else {
			rows = append(rows, "  "+rowContent)
		}
	}

	return m.padContent(strings.Join(rows, "\n"))
}

// parseDBSource extracts host and database from a connection string
type dbSourceInfo struct {
	host string
	db   string
}

func parseDBSource(source string) (*dbSourceInfo, error) {
	// Simple parsing: postgresql://user:pass@host:port/dbname
	// Just extract host and dbname
	parts := strings.Split(source, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid source format")
	}
	hostPart := parts[1]
	// Remove query params
	if idx := strings.Index(hostPart, "?"); idx != -1 {
		hostPart = hostPart[:idx]
	}
	// Split host:port/db
	slashIdx := strings.Index(hostPart, "/")
	if slashIdx == -1 {
		return nil, fmt.Errorf("no database in source")
	}
	hostPort := hostPart[:slashIdx]
	db := hostPart[slashIdx+1:]

	// Extract just host (remove port)
	host := hostPort
	if colonIdx := strings.Index(hostPort, ":"); colonIdx != -1 {
		host = hostPort[:colonIdx]
	}

	return &dbSourceInfo{host: host, db: db}, nil
}

// formatSize formats bytes to human readable format
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// renderDatabaseLogsView renders the database sync logs view
func (m *Model) renderDatabaseLogsView() string {
	logs := m.databaseLogs[m.databaseLogsProject]

	if len(logs) == 0 {
		empty := m.styles.Muted.Render("No sync logs available for " + m.databaseLogsProject + ".")
		return m.padContent(empty)
	}

	// Calculate visible area
	viewHeight := m.tableHeight()

	// Calculate max scroll
	maxScroll := len(logs) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Apply auto-scroll if enabled
	if m.databaseLogsAuto {
		m.databaseLogsScroll = maxScroll
	}

	// Apply scroll offset
	start := m.databaseLogsScroll
	if start < 0 {
		start = 0
	}
	if start >= len(logs) {
		start = len(logs) - 1
		if start < 0 {
			start = 0
		}
	}

	end := start + viewHeight
	if end > len(logs) {
		end = len(logs)
	}

	visibleLines := logs[start:end]

	// Format lines with line numbers
	var formatted []string
	for i, line := range visibleLines {
		lineNum := start + i + 1
		// Truncate long lines
		maxLineWidth := m.width - 8 // space for line number
		if len(line) > maxLineWidth {
			line = line[:maxLineWidth-3] + "..."
		}
		formatted = append(formatted, fmt.Sprintf("%4d  %s", lineNum, line))
	}

	// Pad to fill height
	for len(formatted) < viewHeight {
		formatted = append(formatted, "")
	}

	// Add scroll indicator with auto-scroll status
	autoScrollStatus := ""
	if m.databaseLogsAuto {
		autoScrollStatus = " [AUTO-SCROLL ON]"
	}
	scrollInfo := fmt.Sprintf("Lines %d-%d of %d%s (j/k: scroll, a: toggle auto-scroll, g/G: top/bottom, esc: close)", start+1, end, len(logs), autoScrollStatus)
	formatted = append(formatted, "")
	formatted = append(formatted, m.styles.Muted.Render(scrollInfo))

	return m.padContent(strings.Join(formatted, "\n"))
}
