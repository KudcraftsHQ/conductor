package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tui/styles"
	"github.com/hammashamzah/conductor/internal/workspace"
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

	// Main content
	switch m.currentView {
	case ViewProjects:
		sections = append(sections, m.renderProjectsTable())
	case ViewWorktrees:
		sections = append(sections, m.renderWorktreesTable())
	case ViewPorts:
		sections = append(sections, m.renderPortsTable())
	case ViewCreateWorktree:
		sections = append(sections, m.renderCreateWorktreeModal())
	case ViewConfirmDelete:
		sections = append(sections, m.renderConfirmDeleteModal())
	case ViewHelp:
		sections = append(sections, m.renderHelpModal())
	case ViewLogs:
		sections = append(sections, m.renderLogsView())
	}

	// Footer
	sections = append(sections, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m *Model) renderHeader() string {
	// Logo
	logo := m.styles.Logo.Render("⚡ CONDUCTOR")

	// Info
	info := ""
	totalPorts := m.config.TotalUsedPorts()
	projectCount := len(m.config.Projects)
	info = m.styles.HeaderInfo.Render(fmt.Sprintf("  %d projects • %d ports", projectCount, totalPorts))

	// Build header line
	left := logo + info
	leftWidth := lipgloss.Width(left)

	// Right side spacing
	spacing := m.width - leftWidth - 2
	if spacing < 0 {
		spacing = 0
	}

	header := left + strings.Repeat(" ", spacing)
	return m.styles.Header.Width(m.width).Render(header)
}

func (m *Model) renderTabs() string {
	var tabs []string

	switch m.currentView {
	case ViewProjects, ViewWorktrees, ViewPorts:
		tabs = append(tabs, m.styles.RenderTab("1", "projects", m.currentView == ViewProjects))
		if m.selectedProject != "" {
			tabs = append(tabs, m.styles.RenderTab("2", "worktrees", m.currentView == ViewWorktrees))
		}
		tabs = append(tabs, m.styles.RenderTab("p", "ports", m.currentView == ViewPorts))
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
	case ViewHelp:
		title = "HELP"
		count = 0
	case ViewLogs:
		title = "SETUP LOGS: " + m.logsWorktree
		count = 0
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
	portW := 15
	statusW := 12
	createdW := 15
	branchW := m.width - nameW - portW - statusW - createdW - 12 // Remaining space for branch
	if branchW < 20 {
		branchW = 20
	}

	var rows []string

	// Header
	header := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s",
		nameW, "NAME",
		branchW, "BRANCH",
		portW, "PORTS",
		statusW, "STATUS",
		createdW, "CREATED")
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

		// Format port range
		portRange := "-"
		if len(wt.Ports) > 0 {
			if len(wt.Ports) == 1 {
				portRange = fmt.Sprintf("%d", wt.Ports[0])
			} else {
				portRange = fmt.Sprintf("%d-%d", wt.Ports[0], wt.Ports[len(wt.Ports)-1])
			}
		}

		// Status based on setup state
		status := "ready"
		switch wt.SetupStatus {
		case config.SetupStatusRunning:
			status = m.spinner.View() + " setting up"
		case config.SetupStatusFailed:
			status = "✗ failed"
		case config.SetupStatusDone:
			status = "ready"
		}

		created := wt.CreatedAt.Format("Jan 2, 15:04")

		// Build row content (without cursor)
		rowContent := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s",
			nameW, truncate(name, nameW),
			branchW, truncate(wt.Branch, branchW),
			portW, portRange,
			statusW, status,
			createdW, created)

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
	}

	for i, bc := range breadcrumbs {
		if i == len(breadcrumbs)-1 {
			left += m.styles.Breadcrumb.Render(bc)
		} else {
			left += m.styles.Breadcrumb.Render(bc) + " "
		}
	}

	// Right: Status or key hints
	if m.statusMessage != "" {
		if m.statusIsError {
			right = lipgloss.NewStyle().Foreground(styles.ErrorColor).Render(m.statusMessage)
		} else {
			right = m.styles.Muted.Render(m.statusMessage)
		}
	} else {
		// Key hints
		var hints []string
		switch m.currentView {
		case ViewProjects:
			hints = []string{"enter:select", "d:delete", "?:help", "q:quit"}
		case ViewWorktrees:
			hints = []string{"c:create", "o:open", "C:cursor", "a:archive", "esc:back"}
		case ViewPorts:
			hints = []string{"esc:back"}
		}
		right = m.styles.Muted.Render(strings.Join(hints, "  "))
	}

	// Calculate spacing
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	spacing := m.width - leftWidth - rightWidth - 2
	if spacing < 0 {
		spacing = 0
	}

	footer := left + strings.Repeat(" ", spacing) + right
	return "\n" + footer
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

	// Center the modal
	return m.centerModal(modal)
}

func (m *Model) renderConfirmDeleteModal() string {
	width := 50
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Confirm Delete"))
	content.WriteString("\n\n")

	if m.deleteTargetType == "worktree" {
		content.WriteString(fmt.Sprintf("  Archive worktree '%s'?\n\n", m.deleteTarget))
		content.WriteString(m.styles.Muted.Render("  This will remove the git worktree and free its ports."))
	} else {
		content.WriteString(fmt.Sprintf("  Remove project '%s'?\n\n", m.deleteTarget))
		content.WriteString(m.styles.Muted.Render("  This will free all ports. Files will NOT be deleted."))
	}

	content.WriteString("\n\n  ")
	content.WriteString(m.styles.RenderKeyHelp("y", "yes"))
	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("n", "no"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return m.centerModal(modal)
}

func (m *Model) renderHelpModal() string {
	width := 60
	if width > m.width-4 {
		width = m.width - 4
	}

	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render("Keyboard Shortcuts"))
	content.WriteString("\n\n")

	sections := []struct {
		title string
		keys  []struct{ key, desc string }
	}{
		{
			title: "Navigation",
			keys: []struct{ key, desc string }{
				{"↑/k", "Move up"},
				{"↓/j", "Move down"},
				{"enter", "Select/Open"},
				{"esc", "Go back"},
			},
		},
		{
			title: "Actions",
			keys: []struct{ key, desc string }{
				{"c", "Create worktree"},
				{"a/d", "Archive/Delete"},
				{"o/t", "Open in terminal"},
				{"C", "Open in Cursor"},
				{"V", "Open in VSCode"},
			},
		},
		{
			title: "Views",
			keys: []struct{ key, desc string }{
				{"1", "Projects view"},
				{"2", "Worktrees view"},
				{"p", "Ports view"},
			},
		},
		{
			title: "Other",
			keys: []struct{ key, desc string }{
				{"/", "Filter"},
				{"r", "Refresh"},
				{"?", "Help"},
				{"q", "Quit"},
			},
		},
	}

	for _, section := range sections {
		content.WriteString(m.styles.StatusRunning.Render("  " + section.title))
		content.WriteString("\n")
		for _, k := range section.keys {
			content.WriteString(fmt.Sprintf("    %s %s\n",
				m.styles.HelpKey.Render(fmt.Sprintf("%-8s", k.key)),
				m.styles.Muted.Render(k.desc)))
		}
		content.WriteString("\n")
	}

	content.WriteString("  ")
	content.WriteString(m.styles.RenderKeyHelp("esc", "close"))

	modal := m.styles.Modal.Width(width).Render(content.String())

	return m.centerModal(modal)
}

func (m *Model) centerModal(modal string) string {
	modalHeight := strings.Count(modal, "\n") + 1
	modalWidth := lipgloss.Width(modal)

	// Vertical padding
	topPad := (m.height - modalHeight) / 2
	if topPad < 0 {
		topPad = 0
	}

	// Horizontal padding
	leftPad := (m.width - modalWidth) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	// Add padding
	lines := strings.Split(modal, "\n")
	var padded []string
	for i := 0; i < topPad; i++ {
		padded = append(padded, "")
	}
	for _, line := range lines {
		padded = append(padded, strings.Repeat(" ", leftPad)+line)
	}

	return strings.Join(padded, "\n")
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
	logs := workspace.GetSetupManager().GetLogs(m.selectedProject, m.logsWorktree)

	if logs == "" {
		empty := m.styles.Muted.Render("No logs available for this worktree.")
		return m.padContent(empty)
	}

	// Split logs into lines
	lines := strings.Split(logs, "\n")

	// Calculate visible area
	viewHeight := m.tableHeight()

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

	// Add scroll indicator
	scrollInfo := fmt.Sprintf("Lines %d-%d of %d (j/k to scroll, esc to close)", start+1, end, len(lines))
	formatted = append(formatted, "")
	formatted = append(formatted, m.styles.Muted.Render(scrollInfo))

	return strings.Join(formatted, "\n")
}
