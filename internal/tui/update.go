package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/database"
	"github.com/hammashamzah/conductor/internal/github"
	"github.com/hammashamzah/conductor/internal/opener"
	"github.com/hammashamzah/conductor/internal/tmux"
	"github.com/hammashamzah/conductor/internal/tui/ipc"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Update handles messages and updates state
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.MouseMsg:
		return m.handleMouseMsg(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case WorktreeCreatedMsg:
		m.refreshWorktreeList()
		if !msg.Success {
			m.setStatus("Failed to create worktree: "+msg.Err.Error(), true)
			// Mark as failed
			_ = m.store.SetWorktreeStatus(msg.ProjectName, msg.WorktreeName, config.SetupStatusFailed)
			return m, nil
		}
		// Git worktree created, now run setup
		m.setStatus("Created "+msg.WorktreeName+", running setup...", false)
		_ = m.store.SetWorktreeStatus(msg.ProjectName, msg.WorktreeName, config.SetupStatusRunning)
		projectName := msg.ProjectName
		worktreeName := msg.WorktreeName
		return m, func() tea.Msg {
			done := make(chan SetupCompleteMsg)
			_ = m.wsManager.RunSetupAsync(projectName, worktreeName, func(success bool, setupErr error) {
				done <- SetupCompleteMsg{
					ProjectName:  projectName,
					WorktreeName: worktreeName,
					Success:      success,
					Err:          setupErr,
				}
			})
			return <-done
		}

	case SetupCompleteMsg:
		m.refreshWorktreeList()
		if msg.Success {
			m.setStatus("Setup complete: "+msg.WorktreeName, false)
		} else {
			m.setStatus("Setup failed: "+msg.WorktreeName+" (press 'l' to view logs, 'R' to retry)", true)
		}
		return m, nil

	case RetrySetupMsg:
		// Update worktree status
		if msg.Success {
			_ = m.store.SetWorktreeStatus(msg.ProjectName, msg.WorktreeName, config.SetupStatusDone)
		} else {
			_ = m.store.SetWorktreeStatus(msg.ProjectName, msg.WorktreeName, config.SetupStatusFailed)
		}
		m.refreshWorktreeList()
		if msg.Success {
			m.setStatus("Retry successful: "+msg.WorktreeName, false)
		} else {
			errMsg := "unknown error"
			if msg.Err != nil {
				errMsg = msg.Err.Error()
			}
			m.setStatus("Retry failed: "+msg.WorktreeName+" - "+errMsg+" (press 'l' to view logs, 'R' to retry)", true)
		}
		return m, nil

	case WorktreeArchivedMsg:
		// Clear archive status
		if msg.ProjectName != "" && msg.WorktreeName != "" {
			_ = m.store.SetWorktreeArchiveStatus(msg.ProjectName, msg.WorktreeName, config.ArchiveStatusNone)
		}

		// Always refresh to show updated status
		m.refreshWorktreeList()

		if msg.Err != nil {
			m.setStatus("Error: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Archived worktree: "+msg.WorktreeName, false)
			if m.cursor >= len(m.worktreeNames) {
				m.cursor = len(m.worktreeNames) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		return m, nil

	case WorktreeDeletedMsg:
		// Always refresh to show updated status
		m.refreshWorktreeList()

		if msg.Err != nil {
			m.setStatus("Error: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Deleted worktree: "+msg.WorktreeName, false)
			if m.cursor >= len(m.worktreeNames) {
				m.cursor = len(m.worktreeNames) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		return m, nil

	case ConfigSavedMsg:
		if msg.Err != nil {
			m.setStatus("Error saving config: "+msg.Err.Error(), true)
		}
		return m, nil

	case OpenedMsg:
		if msg.Err != nil {
			m.setStatus("Error opening: "+msg.Err.Error(), true)
		} else {
			m.setStatus("", false) // Clear status on success
		}
		return m, nil

	case RefreshMsg:
		cfg, err := config.Load()
		if err != nil {
			m.setStatus("Error reloading config: "+err.Error(), true)
			return m, nil
		}
		m.config = cfg
		m.wsManager = workspace.NewManagerWithStore(cfg, m.store)
		m.refreshProjectList()
		if m.selectedProject != "" {
			m.refreshWorktreeList()
			// Also sync PRs and git status in background when refreshing in worktrees view
			if m.currentView == ViewWorktrees {
				projectName := m.selectedProject
				m.gitStatusLoading = true
				m.setStatus("Refreshing...", false)
				return m, tea.Batch(
					func() tea.Msg {
						err := m.wsManager.SyncAllPRs(projectName)
						return AllPRsSyncedMsg{ProjectName: projectName, Err: err}
					},
					func() tea.Msg {
						statuses, err := m.wsManager.FetchGitStatusForProject(projectName)
						return GitStatusFetchedMsg{ProjectName: projectName, Statuses: statuses, Err: err}
					},
				)
			}
		}
		m.setStatus("Refreshed", false)
		return m, nil

	case PRsFetchedMsg:
		m.prLoading = false
		if msg.Err != nil {
			m.setStatus("Error fetching PRs: "+msg.Err.Error(), true)
			m.prList = nil
		} else {
			m.prList = msg.PRs
			m.prCursor = 0
		}
		return m, nil

	case PROpenedMsg:
		if msg.Err != nil {
			m.setStatus("Error opening PR: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Opened PR in browser", false)
		}
		return m, nil

	case WorktreeFromPRCreatedMsg:
		m.allPRCreating = false
		if msg.Err != nil {
			if msg.PRNumber > 0 {
				m.setStatus(fmt.Sprintf("Failed to create worktree for PR #%d: %s", msg.PRNumber, msg.Err.Error()), true)
			} else {
				m.setStatus("Error creating worktree: "+msg.Err.Error(), true)
			}
		} else {
			if msg.PRNumber > 0 {
				m.setStatus(fmt.Sprintf("Creating worktree '%s' for PR #%d (%s)...", msg.WorktreeName, msg.PRNumber, msg.Branch), false)
			} else {
				m.setStatus("Created worktree "+msg.WorktreeName+" for "+msg.Branch, false)
			}
			// Navigate back to worktrees view to see the new worktree
			m.currentView = ViewWorktrees
			m.prList = nil
			m.prWorktree = ""
			m.allPRList = nil
			m.cursor = 0
			m.offset = 0
			m.refreshWorktreeList()
		}
		return m, nil

	case AllPRsSyncedMsg:
		if msg.Err != nil {
			m.setStatus("Error syncing PRs: "+msg.Err.Error(), true)
		} else {
			m.setStatus("PRs refreshed", false)
		}
		return m, nil

	case GitStatusFetchedMsg:
		m.gitStatusLoading = false
		if msg.Err != nil {
			// Silently ignore git status fetch errors
			return m, nil
		}
		// Update the cache with fetched statuses
		for name, status := range msg.Statuses {
			m.gitStatusCache[name] = status
		}
		return m, nil

	case DatabaseSyncProgressMsg:
		// Update progress message for this project
		m.databaseProgress[msg.ProjectName] = msg.Message
		// Append to logs
		m.databaseLogs[msg.ProjectName] = append(m.databaseLogs[msg.ProjectName],
			fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg.Message))
		return m, nil

	case progressMsgWithContinue:
		// Handle progress and continue listening
		m.databaseProgress[msg.progress.ProjectName] = msg.progress.Message
		// Append to logs
		m.databaseLogs[msg.progress.ProjectName] = append(m.databaseLogs[msg.progress.ProjectName],
			fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg.progress.Message))
		return m, msg.next

	case DatabaseSyncCompletedMsg:
		// Clear syncing flag, progress, and cancel function
		delete(m.databaseSyncing, msg.ProjectName)
		delete(m.databaseProgress, msg.ProjectName)
		delete(m.databaseSyncCancel, msg.ProjectName)

		// Handle skipped sync
		if msg.Skipped {
			m.databaseLogs[msg.ProjectName] = append(m.databaseLogs[msg.ProjectName],
				fmt.Sprintf("[%s] Skipped: %s", time.Now().Format("15:04:05"), msg.SkipReason))
			m.setStatus(fmt.Sprintf("Sync skipped: %s", msg.SkipReason), false)
			return m, nil
		}

		// Handle cancelled sync
		if msg.Cancelled {
			m.databaseLogs[msg.ProjectName] = append(m.databaseLogs[msg.ProjectName],
				fmt.Sprintf("[%s] Cancelled by user", time.Now().Format("15:04:05")))
			m.setStatus("Database sync cancelled", false)
			return m, nil
		}

		// Update sync status in config
		project := m.config.Projects[msg.ProjectName]
		if project != nil && project.Database != nil {
			if project.Database.SyncStatus == nil {
				project.Database.SyncStatus = &config.DatabaseSyncStatus{}
			}
			if msg.Err != nil {
				project.Database.SyncStatus.Status = "failed"
				project.Database.SyncStatus.LastError = msg.Err.Error()
				m.databaseLogs[msg.ProjectName] = append(m.databaseLogs[msg.ProjectName],
					fmt.Sprintf("[%s] ERROR: %s", time.Now().Format("15:04:05"), msg.Err.Error()))
				m.setStatus("Database sync failed: "+msg.Err.Error(), true)
			} else {
				project.Database.SyncStatus.Status = "synced"
				project.Database.SyncStatus.LastSyncAt = time.Now().Format("2006-01-02 15:04")
				project.Database.SyncStatus.GoldenCopySize = msg.GoldenFileSize
				project.Database.SyncStatus.TableCount = msg.TableCount
				project.Database.SyncStatus.ExcludedCount = msg.ExcludedCount
				project.Database.SyncStatus.LastError = ""
				completionMsg := fmt.Sprintf("Completed: %d tables, %s in %dms",
					msg.TableCount, database.FormatSize(msg.GoldenFileSize), msg.DurationMs)
				m.databaseLogs[msg.ProjectName] = append(m.databaseLogs[msg.ProjectName],
					fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), completionMsg))
				m.setStatus(fmt.Sprintf("Database synced: %d tables, %s in %dms",
					msg.TableCount, database.FormatSize(msg.GoldenFileSize), msg.DurationMs), false)
			}
			// Persist to config file
			_ = m.store.SetDatabaseConfig(msg.ProjectName, project.Database)
		}
		return m, nil

	case DatabaseReinitCompletedMsg:
		if msg.Err != nil {
			m.setStatus("Database reinit failed: "+msg.Err.Error(), true)
		} else {
			statusMsg := fmt.Sprintf("Database %s re-initialized", msg.DatabaseName)
			if msg.PendingMigrations > 0 {
				statusMsg += fmt.Sprintf(" (%d pending migrations - run prisma migrate deploy)", msg.PendingMigrations)
			} else if msg.MigrationStatus == "synced" {
				statusMsg += " (migrations up to date)"
			}
			m.setStatus(statusMsg, false)
		}
		return m, nil

	case DatabaseMigrationStatusMsg:
		if msg.Err != nil {
			m.setStatus("Migration check failed: "+msg.Err.Error(), true)
		} else {
			statusMsg := fmt.Sprintf("Migration status [%s]: %d applied, %d in worktree",
				msg.MigrationStatus, msg.AppliedCount, msg.WorktreeCount)
			if len(msg.PendingMigrations) > 0 {
				statusMsg += fmt.Sprintf(" | %d pending", len(msg.PendingMigrations))
			}
			if len(msg.ExtraMigrations) > 0 {
				statusMsg += fmt.Sprintf(" | %d extra in DB", len(msg.ExtraMigrations))
			}
			m.setStatus(statusMsg, msg.MigrationStatus == "diverged" || msg.MigrationStatus == "behind")
		}
		return m, nil

	case BackgroundSyncNeededMsg:
		// Trigger background sync for the project
		project := m.config.Projects[msg.ProjectName]
		if project != nil && project.Database != nil {
			m.setStatus(fmt.Sprintf("Auto-syncing %s (%s)...", msg.ProjectName, msg.Reason), false)
			return m, m.triggerDatabaseSync(msg.ProjectName, false)
		}
		return m, nil

	case DatabaseMetadataLoadedMsg:
		// Update SyncStatus from loaded metadata for projects that don't have it yet
		for projectName, syncStatus := range msg.Metadata {
			project := m.config.Projects[projectName]
			if project != nil && project.Database != nil {
				// Only update if SyncStatus is empty or has no last sync time
				if project.Database.SyncStatus == nil || project.Database.SyncStatus.LastSyncAt == "" {
					project.Database.SyncStatus = syncStatus
					// Persist to config file
					_ = m.store.SetDatabaseConfig(projectName, project.Database)
				}
			}
		}
		return m, nil

	case UpdateCheckMsg:
		if msg.Err != nil {
			// Silently ignore update check errors
			return m, nil
		}
		if msg.UpdateAvailable {
			m.updateAvailable = true
			m.latestVersion = msg.LatestVersion
			if m.config.Updates.NotifyInTUI {
				m.setStatus("Update available: "+msg.LatestVersion+" (current: "+m.version+")", false)
			}
		}
		return m, nil

	case UpdateInstalledMsg:
		if msg.Err != nil {
			m.setStatus("Failed to install update: "+msg.Err.Error(), true)
		} else {
			m.updateDownloaded = true
			m.setStatus("Updated to "+msg.Version+"! Restart to use new version.", false)
			// Update config
			m.store.SetLastVersion(msg.Version)
		}
		return m, nil

	case UpdateCheckTickMsg:
		// Schedule next check regardless of current state
		nextCheck := m.scheduleUpdateCheck()

		// Skip if auto-check is disabled or update already available
		if !m.config.Updates.AutoCheck || m.updateAvailable {
			return m, nextCheck
		}

		// Perform update check in background
		return m, tea.Batch(nextCheck, func() tea.Msg {
			return m.performUpdateCheck()
		})

	case ClaudePRScanTickMsg:
		// Schedule next scan regardless of current state
		nextScan := m.scheduleClaudePRScan()

		// Skip if already scanning or no projects
		if m.claudePRScanning || len(m.config.Projects) == 0 {
			return m, nextScan
		}

		// Scan all projects for Claude PRs
		m.claudePRScanning = true
		var scanCmds []tea.Cmd
		scanCmds = append(scanCmds, nextScan)

		for projectName := range m.config.Projects {
			pName := projectName // capture for closure
			scanCmds = append(scanCmds, func() tea.Msg {
				result, err := m.wsManager.AutoSetupClaudePRs(pName)
				if err != nil {
					return AutoSetupClaudePRsMsg{
						ProjectName: pName,
						Err:         err,
					}
				}
				return AutoSetupClaudePRsMsg{
					ProjectName:    pName,
					NewWorktrees:   result.NewWorktrees,
					ExistingBranch: result.ExistingBranch,
					Errors:         result.Errors,
				}
			})
		}
		return m, tea.Batch(scanCmds...)

	case AllProjectPRsFetchedMsg:
		m.allPRLoading = false
		if msg.Err != nil {
			m.setStatus("Error fetching PRs: "+msg.Err.Error(), true)
			m.allPRList = nil
		} else {
			m.allPRList = msg.PRs
			m.allPRCursor = 0
			m.offset = 0
		}
		return m, nil

	case AutoSetupClaudePRsMsg:
		m.claudePRScanning = false
		if msg.Err != nil {
			// Only show error for manual triggers
			if msg.IsManual {
				m.setStatus("Error auto-setting up Claude PRs: "+msg.Err.Error(), true)
			}
		} else {
			if msg.IsManual {
				// Manual trigger: always show results
				statusMsg := fmt.Sprintf("Created %d worktree(s)", len(msg.NewWorktrees))
				if len(msg.ExistingBranch) > 0 {
					statusMsg += fmt.Sprintf(", skipped %d existing", len(msg.ExistingBranch))
				}
				if len(msg.Errors) > 0 {
					statusMsg += fmt.Sprintf(", %d error(s)", len(msg.Errors))
					m.setStatus(statusMsg, true)
				} else {
					m.setStatus(statusMsg, false)
				}
			} else if len(msg.NewWorktrees) > 0 {
				// Periodic scan: only show if new worktrees were created
				statusMsg := fmt.Sprintf("Auto-created %d worktree(s) for Claude PRs", len(msg.NewWorktrees))
				if len(msg.Errors) > 0 {
					statusMsg += fmt.Sprintf(", %d error(s)", len(msg.Errors))
					m.setStatus(statusMsg, true)
				} else {
					m.setStatus(statusMsg, false)
				}
			}

			// Reload config to show new worktrees
			if len(msg.NewWorktrees) > 0 {
				m.refreshWorktreeList()
			}
		}
		return m, nil

	case TunnelStartedMsg:
		m.tunnelStarting = false
		if msg.Err != nil {
			m.setStatus("Tunnel failed: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Tunnel active: "+msg.URL, false)
			// Update worktree state
			_ = m.store.SetTunnelState(msg.ProjectName, msg.WorktreeName, &config.TunnelState{
				Active: true,
				Mode:   config.TunnelMode(msg.Mode),
				URL:    msg.URL,
				Port:   msg.Port,
			})
			// Refresh to show tunnel status
			m.refreshWorktreeList()
		}
		return m, nil

	case TunnelStoppedMsg:
		if msg.Err != nil {
			m.setStatus("Failed to stop tunnel: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Tunnel stopped", false)
			// Clear tunnel state
			_ = m.store.ClearTunnelState(msg.ProjectName, msg.WorktreeName)
			// Refresh to clear tunnel status
			m.refreshWorktreeList()
		}
		return m, nil

	case TunnelRestoredMsg:
		if msg.Err != nil {
			// Silent error - don't disturb user on startup
		} else if msg.RestoredCount > 0 {
			m.setStatus(fmt.Sprintf("Restored %d tunnel(s)", msg.RestoredCount), false)
			// Refresh to show restored tunnel states
			m.refreshWorktreeList()
		}
		return m, nil

	case StatesRecoveredMsg:
		if msg.RecoveredCount > 0 {
			// Refresh the worktree list to show updated states
			m.refreshWorktreeList()
			m.setStatus(fmt.Sprintf("Recovered %d interrupted worktree(s) - use 'R' to retry", msg.RecoveredCount), false)
		}
		return m, nil

	case OrphanedBranchesScannedMsg:
		m.orphanedLoading = false
		if msg.Err != nil {
			m.setStatus("Error scanning branches: "+msg.Err.Error(), true)
			m.orphanedBranches = nil
		} else {
			m.orphanedBranches = msg.Branches
			if len(msg.Branches) == 0 {
				m.setStatus("No orphaned branches found", false)
			} else {
				m.setStatus(fmt.Sprintf("Found %d orphaned branch(es)", len(msg.Branches)), false)
			}
		}
		return m, nil

	case BranchDeletedMsg:
		if msg.Err != nil {
			m.setStatus("Error deleting branch: "+msg.Err.Error(), true)
		} else {
			m.setStatus("Deleted branch: "+msg.Branch, false)
			// Remove from list
			for i, b := range m.orphanedBranches {
				if b.Branch == msg.Branch {
					m.orphanedBranches = append(m.orphanedBranches[:i], m.orphanedBranches[i+1:]...)
					break
				}
			}
			// Adjust cursor if needed
			if m.archivedListCursor >= len(m.orphanedBranches) && m.archivedListCursor > 0 {
				m.archivedListCursor--
			}
		}
		return m, nil

	case StatusTimeoutMsg:
		// Only clear if the message hasn't been replaced
		if msg.SetAt.Equal(m.statusSetAt) {
			m.statusMessage = ""
			m.statusIsError = false
		}
		return m, nil

	case ipc.RefreshRequestMsg:
		// Immediate refresh from CLI via IPC
		return m, func() tea.Msg { return ConfigFileChangedMsg{} }

	case ConfigWatchTickMsg:
		// Polling fallback - schedule next check and verify file
		nextCheck := m.scheduleConfigWatch()
		return m, tea.Batch(nextCheck, func() tea.Msg {
			return m.checkConfigFile()
		})

	case ConfigFileChangedMsg:
		// Debounce: skip if we reloaded very recently
		if time.Since(m.lastConfigReload) < 500*time.Millisecond {
			return m, nil
		}
		m.lastConfigReload = time.Now()

		// Update stored mod time
		if path, err := config.ConfigPath(); err == nil {
			if info, err := os.Stat(path); err == nil {
				m.configModTime = info.ModTime()
			}
		}

		// Reload config
		cfg, err := config.Load()
		if err != nil {
			return m, nil
		}

		m.config = cfg
		m.wsManager = workspace.NewManagerWithStore(cfg, m.store)
		m.refreshProjectList()
		if m.selectedProject != "" {
			m.refreshWorktreeList()
		}

		return m, m.setStatusWithTimeout("Config reloaded", false)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter mode
	if m.filterMode {
		return m.handleFilterInput(msg)
	}

	// Handle create worktree modal
	if m.currentView == ViewCreateWorktree {
		return m.handleCreateWorktreeInput(msg)
	}

	// Handle confirm delete
	if m.currentView == ViewConfirmDelete {
		return m.handleConfirmDelete(msg)
	}

	// Handle help modal
	if m.currentView == ViewHelp {
		switch {
		case msg.Type == tea.KeyEsc || msg.String() == "?" || msg.String() == "q":
			m.currentView = m.prevView
			return m, nil
		case msg.String() == "j" || msg.Type == tea.KeyDown:
			m.helpScroll++
			return m, nil
		case msg.String() == "k" || msg.Type == tea.KeyUp:
			if m.helpScroll > 0 {
				m.helpScroll--
			}
			return m, nil
		case msg.String() == "g":
			m.helpScroll = 0
			return m, nil
		case msg.String() == "G":
			m.helpScroll = 999 // Will be clamped in render
			return m, nil
		}
		return m, nil
	}

	// Handle logs view
	if m.currentView == ViewLogs {
		return m.handleLogsView(msg)
	}

	// Handle quit dialog
	if m.currentView == ViewQuit {
		return m.handleQuitDialog(msg)
	}

	// Handle PRs modal
	if m.currentView == ViewPRs {
		return m.handlePRsView(msg)
	}

// Handle All PRs view
	if m.currentView == ViewAllPRs {
		return m.handleAllPRsView(msg)
	}

	// Handle tunnel modal
	if m.currentView == ViewTunnelModal {
		return m.handleTunnelModal(msg)
	}

	// Handle branch rename dialog
	if m.currentView == ViewBranchRename {
		return m.handleBranchRenameDialog(msg)
	}

	// Handle archived list view
	if m.currentView == ViewArchivedList {
		return m.handleArchivedListView(msg)
	}

	// Handle status history view
	if m.currentView == ViewStatusHistory {
		return m.handleStatusHistoryView(msg)
	}

	// Handle databases view
	if m.currentView == ViewDatabases {
		return m.handleDatabasesView(msg)
	}

	// Handle database logs view
	if m.currentView == ViewDatabaseLogs {
		return m.handleDatabaseLogsView(msg)
	}

	// Global keys
	switch {
	case key.Matches(msg, m.keyMap.Quit):
		m.quitFocused = 0
		m.prevView = m.currentView
		m.currentView = ViewQuit
		return m, nil

	case key.Matches(msg, m.keyMap.Help):
		m.prevView = m.currentView
		m.currentView = ViewHelp
		m.helpScroll = 0
		return m, nil

	case key.Matches(msg, m.keyMap.StatusHistory):
		if len(m.statusHistory) > 0 {
			m.statusHistoryCursor = 0
			m.statusHistoryOffset = 0
			m.prevView = m.currentView
			m.currentView = ViewStatusHistory
		} else {
			m.setStatus("No message history", false)
		}
		return m, nil

	case key.Matches(msg, m.keyMap.Filter):
		m.filterMode = true
		m.filter = ""
		return m, nil

	case key.Matches(msg, m.keyMap.Refresh):
		return m, func() tea.Msg { return RefreshMsg{} }

	// Tab navigation with number keys
	case msg.String() == "1":
		if m.currentView != ViewProjects {
			m.currentView = ViewProjects
			m.cursor = 0
			m.offset = 0
		}
		return m, nil

	case msg.String() == "2":
		if m.selectedProject != "" && m.currentView != ViewWorktrees {
			m.currentView = ViewWorktrees
			m.cursor = 0
			m.offset = 0
		}
		return m, nil
	}

	// View-specific keys
	switch m.currentView {
	case ViewProjects:
		return m.handleProjectsView(msg)
	case ViewWorktrees:
		return m.handleWorktreesView(msg)
	case ViewPorts:
		return m.handlePortsView(msg)
	}

	return m, nil
}

func (m *Model) handleProjectsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(m.projectNames)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Enter):
		if m.cursor >= 0 && m.cursor < len(m.projectNames) {
			m.selectedProject = m.projectNames[m.cursor]
			m.refreshWorktreeList()
			m.cursor = 0
			m.offset = 0
			m.prevView = ViewProjects
			m.currentView = ViewWorktrees

			// Clear git status cache for new project
			m.gitStatusCache = make(map[string]*workspace.GitStatusInfo)
			m.gitStatusLoading = true

			// Sync PRs and git status for all worktrees in background
			projectName := m.selectedProject
			return m, tea.Batch(
				func() tea.Msg {
					err := m.wsManager.SyncAllPRs(projectName)
					return AllPRsSyncedMsg{ProjectName: projectName, Err: err}
				},
				func() tea.Msg {
					statuses, err := m.wsManager.FetchGitStatusForProject(projectName)
					return GitStatusFetchedMsg{ProjectName: projectName, Statuses: statuses, Err: err}
				},
			)
		}

	case key.Matches(msg, m.keyMap.Ports):
		m.prevView = ViewProjects
		m.currentView = ViewPorts
		m.cursor = 0
		m.offset = 0

	case key.Matches(msg, m.keyMap.DatabaseList):
		m.prevView = ViewProjects
		m.currentView = ViewDatabases
		m.databaseCursor = 0
		m.databaseOffset = 0
		// Load sync metadata from disk for projects that may have been synced via CLI
		return m, m.loadDatabaseMetadata()

	case key.Matches(msg, m.keyMap.Delete):
		if m.cursor >= 0 && m.cursor < len(m.projectNames) {
			m.deleteTarget = m.projectNames[m.cursor]
			m.deleteTargetType = "project"
			m.prevView = ViewProjects
			m.currentView = ViewConfirmDelete
		}
	}

	return m, nil
}

func (m *Model) handleWorktreesView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(m.worktreeNames)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Back):
		// Find the index of the current project before switching back
		projectIndex := 0
		for i, name := range m.projectNames {
			if name == m.selectedProject {
				projectIndex = i
				break
			}
		}
		m.currentView = ViewProjects
		m.selectedProject = ""
		m.cursor = projectIndex
		m.offset = 0
		m.refreshProjectList()
		m.ensureCursorVisible()

	case key.Matches(msg, m.keyMap.Create):
		m.createInput.Reset()
		m.createPortInput.Reset()
		m.createFocused = 0
		m.createError = "" // Clear any previous error
		m.createInput.Focus()
		m.prevView = ViewWorktrees
		m.currentView = ViewCreateWorktree

	case key.Matches(msg, m.keyMap.Archive):
		// 'a' key - archive active worktrees
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			if wt := project.Worktrees[wtName]; wt != nil {
				if wt.IsRoot {
					m.setStatus("Cannot archive root worktree", true)
				} else if wt.Archived {
					m.setStatus("Worktree is already archived (use 'd' to delete)", true)
				} else {
					m.deleteTarget = wtName
					m.deleteTargetType = "worktree"
					m.prevView = ViewWorktrees
					m.currentView = ViewConfirmDelete
				}
			}
		}

	case key.Matches(msg, m.keyMap.Delete):
		// 'd' key - delete archived worktrees permanently
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			if wt := project.Worktrees[wtName]; wt != nil {
				if wt.IsRoot {
					m.setStatus("Cannot delete root worktree", true)
				} else if !wt.Archived {
					m.setStatus("Worktree must be archived first (use 'a' to archive)", true)
				} else {
					m.deleteTarget = wtName
					m.deleteTargetType = "worktree-delete"
					m.prevView = ViewWorktrees
					m.currentView = ViewConfirmDelete
				}
			}
		}

	case key.Matches(msg, m.keyMap.Open), key.Matches(msg, m.keyMap.OpenTerminal):
		return m.openWorktree(opener.TerminalITerm)

	case key.Matches(msg, m.keyMap.OpenCursor):
		return m.openWorktreeIDE(opener.IDECursor)

	case key.Matches(msg, m.keyMap.OpenVSCode):
		return m.openWorktreeIDE(opener.IDEVSCode)

	case key.Matches(msg, m.keyMap.Ports):
		m.prevView = ViewWorktrees
		m.currentView = ViewPorts

	case key.Matches(msg, m.keyMap.DatabaseList):
		m.prevView = ViewWorktrees
		m.currentView = ViewDatabases
		m.databaseCursor = 0
		m.databaseOffset = 0
		// Load sync metadata from disk for projects that may have been synced via CLI
		return m, m.loadDatabaseMetadata()

	case key.Matches(msg, m.keyMap.MergeReqs):
		// Open PR modal for selected worktree
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			m.prWorktree = wtName
			m.prList = nil
			m.prCursor = 0
			m.prLoading = true
			m.prevView = ViewWorktrees
			m.currentView = ViewPRs

			// Fetch PRs asynchronously
			projectName := m.selectedProject
			return m, func() tea.Msg {
				prs, err := m.wsManager.SyncPRsForWorktree(projectName, wtName)
				return PRsFetchedMsg{
					ProjectName:  projectName,
					WorktreeName: wtName,
					PRs:          prs,
					Err:          err,
				}
			}
		}

	case key.Matches(msg, m.keyMap.AllPRs):
		// Open all PRs view for the project
		if m.selectedProject != "" {
			m.allPRList = nil
			m.allPRCursor = 0
			m.allPRLoading = true
			m.offset = 0
			m.prevView = ViewWorktrees
			m.currentView = ViewAllPRs

			// Fetch all PRs asynchronously
			projectName := m.selectedProject
			return m, func() tea.Msg {
				prs, err := m.wsManager.FetchAllProjectPRs(projectName)
				return AllProjectPRsFetchedMsg{
					ProjectName: projectName,
					PRs:         prs,
					Err:         err,
				}
			}
		}

	case key.Matches(msg, m.keyMap.AutoSetupClaude):
		// Auto-setup worktrees for all Claude PRs (manual trigger)
		if m.selectedProject != "" {
			m.statusMessage = "🔍 Scanning for Claude PRs..."
			m.statusIsError = false
			projectName := m.selectedProject
			return m, func() tea.Msg {
				result, err := m.wsManager.AutoSetupClaudePRs(projectName)
				if err != nil {
					return AutoSetupClaudePRsMsg{
						ProjectName: projectName,
						Err:         err,
						IsManual:    true,
					}
				}
				return AutoSetupClaudePRsMsg{
					ProjectName:    projectName,
					NewWorktrees:   result.NewWorktrees,
					ExistingBranch: result.ExistingBranch,
					Errors:         result.Errors,
					Err:            nil,
					IsManual:       true,
				}
			}
		}

	case key.Matches(msg, m.keyMap.Retry):
		// Retry failed setup for selected worktree
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			if wt := project.Worktrees[wtName]; wt != nil {
				if wt.SetupStatus != config.SetupStatusFailed {
					m.setStatus("Can only retry failed setups", true)
					return m, nil
				}

				projectName := m.selectedProject
				worktreeName := wtName

				// Check if worktree directory exists - if not, need to create it first
				if !workspace.WorktreeExists(wt.Path) {
					// Worktree creation failed, queue it for creation
					_ = m.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusCreating)
					m.setStatus("Retrying worktree creation: "+wtName+"...", false)

					workspace.GetWorktreeQueue().Enqueue(&workspace.WorktreeJob{
						ProjectName:  projectName,
						WorktreeName: worktreeName,
						Worktree:     wt,
						Store:        m.store,
						Manager:      m.wsManager,
						OnComplete: func(success bool, err error) {
							// This callback runs in background, TUI will update via refresh
						},
					})
					return m, nil
				}

				// Worktree exists, just retry setup
				_ = m.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusRunning)
				m.setStatus("Retrying setup: "+wtName+"...", false)

				return m, func() tea.Msg {
					done := make(chan RetrySetupMsg)
					_ = m.wsManager.RunSetupAsync(projectName, worktreeName, func(success bool, setupErr error) {
						done <- RetrySetupMsg{
							ProjectName:  projectName,
							WorktreeName: worktreeName,
							Success:      success,
							Err:          setupErr,
						}
					})
					return <-done
				}
			}
		}

	case msg.String() == "l":
		// View logs for selected worktree
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			wt := project.Worktrees[wtName]
			if wt == nil {
				return m, nil
			}

			m.logsWorktree = wtName
			m.logsScroll = 0
			m.logsAutoScroll = true // Enable auto-scroll by default
			m.prevView = ViewWorktrees
			m.currentView = ViewLogs

			// Always show setup logs by default (users can toggle with 't' for archived worktrees)
			m.logsType = "setup"
		}
		// Note: Don't reset cursor here - preserve selection for when we return

	case key.Matches(msg, m.keyMap.Enter):
		// Open in terminal on enter
		return m.openWorktree(opener.TerminalITerm)

	case key.Matches(msg, m.keyMap.Tunnel):
		// Toggle tunnel for selected worktree
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			project := m.config.Projects[m.selectedProject]
			wt := project.Worktrees[wtName]
			if wt == nil {
				return m, nil
			}

			// Check if worktree is ready
			if wt.SetupStatus != config.SetupStatusDone && wt.SetupStatus != "" {
				m.setStatus("Worktree setup not complete", true)
				return m, nil
			}

			// If tunnel is active, stop it
			if wt.Tunnel != nil && wt.Tunnel.Active {
				projectName := m.selectedProject
				worktreeName := wtName
				m.setStatus("Stopping tunnel...", false)
				return m, func() tea.Msg {
					err := m.tunnelManager.StopTunnel(projectName, worktreeName)
					return TunnelStoppedMsg{
						ProjectName:  projectName,
						WorktreeName: worktreeName,
						Err:          err,
					}
				}
			}

			// Open tunnel modal to choose mode
			if len(wt.Ports) == 0 {
				m.setStatus("No ports allocated for this worktree", true)
				return m, nil
			}

			m.tunnelModalOpen = true
			m.tunnelModalPort = wt.Ports[0] // Default to first port
			m.tunnelModalMode = 0           // Default to quick tunnel
			m.prevView = ViewWorktrees
			m.currentView = ViewTunnelModal
		}

	case key.Matches(msg, m.keyMap.CopyURL):
		// Copy tunnel URL to clipboard
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			wt := m.config.Projects[m.selectedProject].Worktrees[wtName]
			if wt != nil && wt.Tunnel != nil && wt.Tunnel.URL != "" {
				return m, m.copyToClipboard(wt.Tunnel.URL)
			} else {
				m.setStatus("No active tunnel to copy URL from", true)
			}
		}

	case key.Matches(msg, m.keyMap.ArchivedList):
		// Show archived worktrees and orphaned branches dialog
		if m.selectedProject != "" {
			m.archivedListCursor = 0
			m.archivedListOffset = 0
			m.archivedListMode = 0 // Start with archived worktrees
			m.orphanedBranches = nil
			m.orphanedLoading = false

			// Build list of archived worktrees
			m.buildArchivedWorktreesList()

			m.prevView = ViewWorktrees
			m.currentView = ViewArchivedList
		}

	case key.Matches(msg, m.keyMap.DatabaseSync):
		// Sync database from source for this project
		project := m.config.Projects[m.selectedProject]
		if project == nil || project.Database == nil {
			m.setStatus("Database not configured for this project. Use CLI: conductor database set-source <url>", true)
			return m, nil
		}

		defaults := m.store.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			m.setStatus("Local PostgreSQL not configured. Use CLI: conductor database set-local <url>", true)
			return m, nil
		}

		projectName := m.selectedProject
		m.setStatus("Syncing database for "+projectName+"...", false)

		return m, func() tea.Msg {
			conductorDir, err := config.ConductorDir()
			if err != nil {
				return DatabaseSyncCompletedMsg{ProjectName: projectName, Err: err}
			}

			mgr := database.NewManager(defaults.LocalPostgresURL, conductorDir)
			metadata, err := mgr.SyncProject(projectName, project.Database)
			if err != nil {
				return DatabaseSyncCompletedMsg{ProjectName: projectName, Err: err}
			}

			return DatabaseSyncCompletedMsg{
				ProjectName:    projectName,
				GoldenFileSize: metadata.GoldenFileSize,
				TableCount:     len(metadata.TableSizes),
				DurationMs:     metadata.SyncDurationMs,
				Err:            nil,
			}
		}

	case key.Matches(msg, m.keyMap.DatabaseReinit):
		// Reinitialize database for selected worktree
		worktrees := m.worktreeNames
		if len(worktrees) == 0 || m.cursor >= len(worktrees) {
			return m, nil
		}

		worktreeName := worktrees[m.cursor]
		project := m.config.Projects[m.selectedProject]
		worktree := project.Worktrees[worktreeName]

		if worktree == nil || worktree.DatabaseName == "" {
			m.setStatus("No database configured for this worktree", true)
			return m, nil
		}

		defaults := m.store.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			m.setStatus("Local PostgreSQL not configured", true)
			return m, nil
		}

		projectName := m.selectedProject
		dbName := worktree.DatabaseName
		worktreePath := worktree.Path
		localURL := defaults.LocalPostgresURL
		m.setStatus("Re-initializing database "+dbName+"...", false)

		return m, func() tea.Msg {
			ctx := context.Background()

			// First try V3 (golden database) - preferred method
			goldenExists, err := database.GoldenDBExists(localURL, projectName)
			if err == nil && goldenExists {
				// Use V3: Clone from golden database
				result, err := database.ReinitializeDatabaseV3(ctx, localURL, projectName, dbName, worktreePath, nil)
				if err != nil {
					return DatabaseReinitCompletedMsg{ProjectName: projectName, WorktreeName: worktreeName, Err: err}
				}

				migStatus := "unknown"
				pendingCount := 0
				if result.MigrationState != nil {
					migStatus = string(result.MigrationState.Compatibility)
					pendingCount = len(result.MigrationState.PendingMigrations)
				}

				return DatabaseReinitCompletedMsg{
					ProjectName:       projectName,
					WorktreeName:      worktreeName,
					DatabaseName:      result.DatabaseName,
					MigrationStatus:   migStatus,
					PendingMigrations: pendingCount,
					Err:               nil,
				}
			}

			// Fall back to V1/V2 (file-based golden copy)
			conductorDir, err := config.ConductorDir()
			if err != nil {
				return DatabaseReinitCompletedMsg{ProjectName: projectName, WorktreeName: worktreeName, Err: err}
			}

			goldenPath := database.GetGoldenCopyPath(projectName, conductorDir)
			schemaPath := database.GetSchemaOnlyPath(projectName, conductorDir)

			if !database.GoldenCopyExists(projectName, conductorDir) {
				return DatabaseReinitCompletedMsg{
					ProjectName:  projectName,
					WorktreeName: worktreeName,
					Err:          fmt.Errorf("golden copy not found - run 'S' to sync first"),
				}
			}

			result, err := database.ReinitializeDatabase(localURL, dbName, goldenPath, schemaPath, worktreePath)
			if err != nil {
				return DatabaseReinitCompletedMsg{ProjectName: projectName, WorktreeName: worktreeName, Err: err}
			}

			migStatus := "unknown"
			pendingCount := 0
			if result.MigrationState != nil {
				migStatus = string(result.MigrationState.Compatibility)
				pendingCount = len(result.MigrationState.PendingMigrations)
			}

			return DatabaseReinitCompletedMsg{
				ProjectName:       projectName,
				WorktreeName:      worktreeName,
				DatabaseName:      result.DatabaseName,
				MigrationStatus:   migStatus,
				PendingMigrations: pendingCount,
				Err:               nil,
			}
		}

	case key.Matches(msg, m.keyMap.DatabaseMigrationStatus):
		// Check migration status for selected worktree
		worktrees := m.worktreeNames
		if len(worktrees) == 0 || m.cursor >= len(worktrees) {
			return m, nil
		}

		worktreeName := worktrees[m.cursor]
		project := m.config.Projects[m.selectedProject]
		worktree := project.Worktrees[worktreeName]

		if worktree == nil || worktree.DatabaseName == "" {
			m.setStatus("No database configured for this worktree", true)
			return m, nil
		}

		defaults := m.store.GetDefaults()
		if defaults.LocalPostgresURL == "" {
			m.setStatus("Local PostgreSQL not configured", true)
			return m, nil
		}

		projectName := m.selectedProject
		dbName := worktree.DatabaseName
		worktreePath := worktree.Path
		m.setStatus("Checking migration status for "+worktreeName+"...", false)

		return m, func() tea.Msg {
			state, err := database.GetWorktreeMigrationStatus(defaults.LocalPostgresURL, dbName, worktreePath)
			if err != nil {
				return DatabaseMigrationStatusMsg{ProjectName: projectName, WorktreeName: worktreeName, Err: err}
			}

			return DatabaseMigrationStatusMsg{
				ProjectName:       projectName,
				WorktreeName:      worktreeName,
				MigrationStatus:   string(state.Compatibility),
				AppliedCount:      len(state.AppliedMigrations),
				WorktreeCount:     len(state.WorktreeMigrations),
				PendingMigrations: state.PendingMigrations,
				ExtraMigrations:   state.ExtraMigrations,
				Recommendation:    state.RecommendedAction,
				Err:               nil,
			}
		}
	}

	return m, nil
}

func (m *Model) handlePortsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	portInfo := m.config.GetAllPortInfo()

	switch {
	case key.Matches(msg, m.keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.cursor < len(portInfo)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		m.cursor = 0
		m.offset = 0
	}

	return m, nil
}

func (m *Model) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filterMode = false
		m.filter = ""
	case tea.KeyEnter:
		m.filterMode = false
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.filter += string(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) handleCreateWorktreeInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.currentView = ViewWorktrees
		return m, nil

	case tea.KeyTab:
		m.createFocused = (m.createFocused + 1) % 2
		if m.createFocused == 0 {
			m.createInput.Focus()
			m.createPortInput.Blur()
		} else {
			m.createInput.Blur()
			m.createPortInput.Focus()
		}
		return m, nil

	case tea.KeyEnter:
		return m.createWorktree()

	default:
		var cmd tea.Cmd
		if m.createFocused == 0 {
			m.createInput, cmd = m.createInput.Update(msg)
		} else {
			m.createPortInput, cmd = m.createPortInput.Update(msg)
		}
		return m, cmd
	}
}

func (m *Model) handleConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.executeDelete()
	case "n", "N", "esc":
		m.currentView = m.prevView
		m.deleteTarget = ""
		m.deleteTargetType = ""
	}
	return m, nil
}

func (m *Model) createWorktree() (tea.Model, tea.Cmd) {
	branch := m.createInput.Value()

	portCount := 0
	if m.createPortInput.Value() != "" {
		var err error
		portCount, err = strconv.Atoi(m.createPortInput.Value())
		if err != nil {
			m.createError = "Invalid port count"
			return m, nil
		}
	}

	// Prepare worktree optimistically (allocates ports, creates entry)
	name, worktree, err := m.wsManager.PrepareWorktree(m.selectedProject, branch, portCount)
	if err != nil {
		m.createError = err.Error()
		return m, nil
	}

	// Success - close dialog and show worktree list immediately
	m.createError = ""
	m.currentView = ViewWorktrees
	m.refreshWorktreeList()
	m.setStatus("Creating worktree: "+name+"...", false)

	// Start git worktree creation in background
	projectName := m.selectedProject
	worktreeName := name
	return m, func() tea.Msg {
		done := make(chan WorktreeCreatedMsg)
		err := m.wsManager.CreateWorktreeAsync(projectName, worktreeName, func(success bool, createErr error) {
			done <- WorktreeCreatedMsg{
				ProjectName:  projectName,
				WorktreeName: worktreeName,
				Worktree:     worktree,
				Success:      success,
				Err:          createErr,
			}
		})
		// If CreateWorktreeAsync failed to start (e.g., worktree not found in config due to race),
		// return an error message immediately instead of waiting forever
		if err != nil {
			return WorktreeCreatedMsg{
				ProjectName:  projectName,
				WorktreeName: worktreeName,
				Worktree:     worktree,
				Success:      false,
				Err:          err,
			}
		}
		return <-done
	}
}

func (m *Model) executeDelete() (tea.Model, tea.Cmd) {
	switch m.deleteTargetType {
	case "worktree":
		projectName := m.selectedProject
		wtName := m.deleteTarget

		// Mark worktree as archiving
		_ = m.store.SetWorktreeArchiveStatus(projectName, wtName, config.ArchiveStatusRunning)
		// Refresh to show archiving status immediately
		m.refreshWorktreeList()

		m.currentView = ViewWorktrees
		m.deleteTarget = ""
		m.deleteTargetType = ""
		m.setStatus("Archiving "+wtName+"...", false)

		return m, func() tea.Msg {
			err := m.wsManager.ArchiveWorktree(projectName, wtName)
			if err != nil {
				return WorktreeArchivedMsg{Err: err}
			}

			return WorktreeArchivedMsg{
				ProjectName:  projectName,
				WorktreeName: wtName,
			}
		}
	case "worktree-delete":
		projectName := m.selectedProject
		wtName := m.deleteTarget

		m.currentView = ViewWorktrees
		m.deleteTarget = ""
		m.deleteTargetType = ""

		return m, func() tea.Msg {
			err := m.wsManager.DeleteWorktree(projectName, wtName)
			if err != nil {
				return WorktreeDeletedMsg{Err: err}
			}

			return WorktreeDeletedMsg{
				ProjectName:  projectName,
				WorktreeName: wtName,
			}
		}
	case "project":
		projectName := m.deleteTarget
		m.currentView = ViewProjects
		m.deleteTarget = ""
		m.deleteTargetType = ""

		return m, func() tea.Msg {
			err := m.store.RemoveProject(projectName)
			if err != nil {
				return ErrorMsg{Err: err}
			}

			return RefreshMsg{}
		}
	default:
		m.currentView = m.prevView
		return m, nil
	}
}

func (m *Model) openWorktree(termType opener.TerminalType) (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.worktreeNames) {
		return m, nil
	}

	wtName := m.worktreeNames[m.cursor]
	project := m.config.Projects[m.selectedProject]
	wt := project.Worktrees[wtName]
	if wt == nil {
		return m, nil
	}

	m.setStatus("Opening "+wtName+"...", false)

	return m, func() tea.Msg {
		// Check if tmux window already exists
		if tmux.WindowExists(m.selectedProject, wt.Branch) {
			// Focus existing window
			err := tmux.FocusWindow(m.selectedProject, wt.Branch)
			return OpenedMsg{Path: wt.Path, Err: err}
		}

		// Create new coding window with claude on left, dev server on right
		err := tmux.CreateCodingWindow(m.selectedProject, wt.Branch, wt.Path)
		return OpenedMsg{Path: wt.Path, Err: err}
	}
}

func (m *Model) openWorktreeIDE(ideType opener.IDEType) (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.worktreeNames) {
		return m, nil
	}

	wtName := m.worktreeNames[m.cursor]
	project := m.config.Projects[m.selectedProject]
	wt := project.Worktrees[wtName]
	if wt == nil {
		return m, nil
	}

	m.setStatus("Opening "+wtName+" in "+string(ideType)+"...", false)

	return m, func() tea.Msg {
		err := opener.OpenInIDE(wt.Path, ideType)
		return OpenedMsg{Path: wt.Path, Err: err}
	}
}

func (m *Model) handleLogsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	logs := m.getCurrentLogs()
	lines := strings.Split(logs, "\n")
	maxScroll := len(lines) - m.tableHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch {
	case msg.Type == tea.KeyEsc || msg.String() == "q":
		// Restore cursor to the worktree we were viewing logs for
		if m.prevView == ViewWorktrees && m.logsWorktree != "" {
			for i, name := range m.worktreeNames {
				if name == m.logsWorktree {
					m.cursor = i
					m.ensureCursorVisible()
					break
				}
			}
		}
		m.currentView = m.prevView
		m.logsWorktree = ""
		m.logsScroll = 0

	case key.Matches(msg, m.keyMap.Up) || msg.String() == "k":
		if m.logsScroll > 0 {
			m.logsScroll--
			m.logsAutoScroll = false // Disable auto-scroll when user scrolls up
		}

	case key.Matches(msg, m.keyMap.Down) || msg.String() == "j":
		if m.logsScroll < maxScroll {
			m.logsScroll++
		}
		// Re-enable auto-scroll if we reach the bottom
		if m.logsScroll >= maxScroll {
			m.logsAutoScroll = true
		}

	case msg.String() == "g":
		m.logsScroll = 0
		m.logsAutoScroll = false

	case msg.String() == "G":
		m.logsScroll = maxScroll
		m.logsAutoScroll = true

	case msg.String() == "a":
		// Toggle auto-scroll
		m.logsAutoScroll = !m.logsAutoScroll
		if m.logsAutoScroll {
			m.logsScroll = maxScroll
		}

	case msg.String() == "t":
		// Toggle between setup and archive logs (only for archived worktrees)
		project := m.config.Projects[m.selectedProject]
		if wt, ok := project.Worktrees[m.logsWorktree]; ok && wt.Archived {
			if m.logsType == "setup" {
				m.logsType = "archive"
			} else {
				m.logsType = "setup"
			}
			m.logsScroll = 0
		}
	}

	return m, nil
}

func (m *Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Only handle mouse events in logs view
	if m.currentView != ViewLogs {
		return m, nil
	}

	logs := m.getCurrentLogs()
	lines := strings.Split(logs, "\n")
	maxScroll := len(lines) - m.tableHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.logsScroll > 0 {
			m.logsScroll -= 3
			if m.logsScroll < 0 {
				m.logsScroll = 0
			}
			m.logsAutoScroll = false // Disable auto-scroll when user scrolls up
		}

	case tea.MouseButtonWheelDown:
		if m.logsScroll < maxScroll {
			m.logsScroll += 3
			if m.logsScroll > maxScroll {
				m.logsScroll = maxScroll
			}
		}
		// Re-enable auto-scroll if we reach the bottom
		if m.logsScroll >= maxScroll {
			m.logsAutoScroll = true
		}
	}

	return m, nil
}

// getCurrentLogs returns the appropriate logs based on logsType
func (m *Model) getCurrentLogs() string {
	if m.logsType == "archive" {
		return workspace.GetSetupManager().GetArchiveLogs(m.selectedProject, m.logsWorktree)
	}
	return workspace.GetSetupManager().GetLogs(m.selectedProject, m.logsWorktree)
}

func (m *Model) handleQuitDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc || msg.String() == "q":
		m.currentView = m.prevView
		return m, nil

	case key.Matches(msg, m.keyMap.Up), msg.String() == "k", msg.String() == "h":
		if m.quitFocused > 0 {
			m.quitFocused--
		}

	case key.Matches(msg, m.keyMap.Down), msg.String() == "j", msg.String() == "l":
		if m.quitFocused < 1 {
			m.quitFocused++
		}

	case msg.Type == tea.KeyEnter:
		if m.quitFocused == 0 {
			// Kill all - kill the entire tmux session
			return m, func() tea.Msg {
				_ = tmux.KillSession()
				return tea.Quit()
			}
		}
		// Detach - detach from tmux session, TUI keeps running
		_ = tmux.DetachSession()
		m.currentView = m.prevView
		return m, nil
	}

	return m, nil
}

func (m *Model) handlePRsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		// Find the index of the worktree we came from
		worktreeIndex := 0
		for i, name := range m.worktreeNames {
			if name == m.prWorktree {
				worktreeIndex = i
				break
			}
		}
		m.currentView = ViewWorktrees
		m.prList = nil
		m.prWorktree = ""
		m.cursor = worktreeIndex
		m.offset = 0
		m.ensureCursorVisible()
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.prCursor > 0 {
			m.prCursor--
			m.ensurePRCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.prCursor < len(m.prList)-1 {
			m.prCursor++
			m.ensurePRCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Open) || msg.Type == tea.KeyEnter:
		// Open selected PR in browser
		if len(m.prList) > 0 && m.prCursor >= 0 && m.prCursor < len(m.prList) {
			pr := m.prList[m.prCursor]
			return m, func() tea.Msg {
				err := github.OpenInBrowser(pr.URL)
				return PROpenedMsg{URL: pr.URL, Err: err}
			}
		}

	case key.Matches(msg, m.keyMap.Refresh):
		// Refresh PRs
		if m.prWorktree != "" {
			m.prLoading = true
			projectName := m.selectedProject
			wtName := m.prWorktree
			return m, func() tea.Msg {
				prs, err := m.wsManager.SyncPRsForWorktree(projectName, wtName)
				return PRsFetchedMsg{
					ProjectName:  projectName,
					WorktreeName: wtName,
					PRs:          prs,
					Err:          err,
				}
			}
		}

	case key.Matches(msg, m.keyMap.CreateWorktreeFromPR):
		// Create worktree from selected PR
		if len(m.prList) > 0 && m.prCursor >= 0 && m.prCursor < len(m.prList) {
			pr := m.prList[m.prCursor]
			projectName := m.selectedProject

			// Check if worktree already exists for this branch
			project, ok := m.config.GetProject(projectName)
			if !ok {
				m.setStatus("Project not found", true)
				return m, nil
			}

			// Look for existing worktree with this branch
			for wtName, wt := range project.Worktrees {
				if wt.Branch == pr.HeadBranch && !wt.Archived {
					m.setStatus(fmt.Sprintf("Worktree '%s' already exists for branch %s", wtName, pr.HeadBranch), false)
					return m, nil
				}
			}

			// Create the worktree
			return m, func() tea.Msg {
				name, worktree, err := m.wsManager.PrepareWorktree(projectName, pr.HeadBranch, project.DefaultPortsPerWorktree)
				if err != nil {
					return WorktreeFromPRCreatedMsg{
						ProjectName: projectName,
						PRNumber:    pr.Number,
						Branch:      pr.HeadBranch,
						Err:         err,
					}
				}

				// Queue worktree creation
				workspace.GetWorktreeQueue().Enqueue(&workspace.WorktreeJob{
					ProjectName:  projectName,
					WorktreeName: name,
					Worktree:     worktree,
					Store:        m.store,
					Manager:      m.wsManager,
					OnComplete:   nil,
				})

				return WorktreeFromPRCreatedMsg{
					ProjectName:  projectName,
					WorktreeName: name,
					PRNumber:     pr.Number,
					Branch:       pr.HeadBranch,
					Err:          nil,
				}
			}
		}
	}

	return m, nil
}

func (m *Model) handleAllPRsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.currentView = ViewWorktrees
		m.allPRList = nil
		m.offset = 0
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.allPRCursor > 0 {
			m.allPRCursor--
			m.ensureAllPRCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.allPRCursor < len(m.allPRList)-1 {
			m.allPRCursor++
			m.ensureAllPRCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Open):
		// Open selected PR in browser
		if len(m.allPRList) > 0 && m.allPRCursor >= 0 && m.allPRCursor < len(m.allPRList) {
			pr := m.allPRList[m.allPRCursor]
			return m, func() tea.Msg {
				err := github.OpenInBrowser(pr.URL)
				return PROpenedMsg{URL: pr.URL, Err: err}
			}
		}

	case msg.Type == tea.KeyEnter, key.Matches(msg, m.keyMap.Create):
		// Create worktree from selected PR
		if len(m.allPRList) > 0 && m.allPRCursor >= 0 && m.allPRCursor < len(m.allPRList) && !m.allPRCreating {
			pr := m.allPRList[m.allPRCursor]
			projectName := m.selectedProject

			// Check if branch is already checked out in another worktree
			project, ok := m.config.Projects[projectName]
			if ok {
				if existingPath := workspace.GitBranchCheckedOutAt(project.Path, pr.HeadBranch); existingPath != "" {
					// Branch is already checked out - show rename dialog
					suggestedName := workspace.SuggestNewBranchName(project.Path, pr.HeadBranch)
					m.branchRenameOriginal = pr.HeadBranch
					m.branchRenamePR = pr
					m.branchRenameConflict = existingPath
					m.branchRenameInput.SetValue(suggestedName)
					m.branchRenameInput.Focus()
					m.prevView = m.currentView
					m.currentView = ViewBranchRename
					return m, textinput.Blink
				}
			}

			m.allPRCreating = true
			m.setStatus("Creating worktree for "+pr.HeadBranch+"...", false)
			return m, func() tea.Msg {
				name, _, err := m.wsManager.CreateWorktreeFromPR(projectName, pr)
				return WorktreeFromPRCreatedMsg{
					ProjectName:  projectName,
					WorktreeName: name,
					PRNumber:     pr.Number,
					Branch:       pr.HeadBranch,
					Err:          err,
				}
			}
		}

	case key.Matches(msg, m.keyMap.Refresh):
		// Refresh all PRs
		m.allPRLoading = true
		projectName := m.selectedProject
		return m, func() tea.Msg {
			prs, err := m.wsManager.FetchAllProjectPRs(projectName)
			return AllProjectPRsFetchedMsg{
				ProjectName: projectName,
				PRs:         prs,
				Err:         err,
			}
		}
	}

	return m, nil
}

func (m *Model) handleTunnelModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.tunnelModalOpen = false
		m.currentView = m.prevView
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.tunnelModalMode > 0 {
			m.tunnelModalMode--
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.tunnelModalMode < 1 {
			m.tunnelModalMode++
		}

	case msg.Type == tea.KeyEnter:
		// Start the tunnel
		if m.cursor >= 0 && m.cursor < len(m.worktreeNames) {
			wtName := m.worktreeNames[m.cursor]
			projectName := m.selectedProject
			port := m.tunnelModalPort

			m.tunnelModalOpen = false
			m.currentView = m.prevView
			m.tunnelStarting = true

			if m.tunnelModalMode == 0 {
				// Quick tunnel
				m.setStatus("Starting quick tunnel...", false)
				return m, func() tea.Msg {
					state, err := m.tunnelManager.StartQuickTunnel(projectName, wtName, port)
					if err != nil {
						return TunnelStartedMsg{
							ProjectName:  projectName,
							WorktreeName: wtName,
							Err:          err,
						}
					}
					return TunnelStartedMsg{
						ProjectName:  projectName,
						WorktreeName: wtName,
						URL:          state.URL,
						Port:         port,
						Mode:         "quick",
					}
				}
			} else {
				// Named tunnel
				m.setStatus("Starting named tunnel...", false)
				project := m.config.Projects[projectName]
				projectPath := project.Path
				return m, func() tea.Msg {
					// Load project config to get tunnel settings
					projectConfig, _ := config.LoadProjectConfig(projectPath)
					state, err := m.tunnelManager.StartNamedTunnel(projectName, wtName, port, projectConfig)
					if err != nil {
						return TunnelStartedMsg{
							ProjectName:  projectName,
							WorktreeName: wtName,
							Err:          err,
						}
					}
					return TunnelStartedMsg{
						ProjectName:  projectName,
						WorktreeName: wtName,
						URL:          state.URL,
						Port:         port,
						Mode:         "named",
					}
				}
			}
		}
	}

	return m, nil
}

func (m *Model) copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		// Use pbcopy on macOS
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to copy to clipboard: %w", err)}
		}
		return OpenedMsg{Path: text} // Reuse OpenedMsg for success notification
	}
}

func (m *Model) handleBranchRenameDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Cancel
		m.currentView = m.prevView
		m.branchRenameInput.Blur()
		return m, nil

	case tea.KeyEnter:
		// Create worktree with new branch name
		newBranch := m.branchRenameInput.Value()
		if newBranch == "" {
			m.setStatus("Branch name cannot be empty", true)
			return m, nil
		}

		// Check if the new branch is also in use
		projectName := m.selectedProject
		project, ok := m.config.Projects[projectName]
		if ok {
			if existingPath := workspace.GitBranchCheckedOutAt(project.Path, newBranch); existingPath != "" {
				m.setStatus(fmt.Sprintf("Branch '%s' is also already checked out", newBranch), true)
				return m, nil
			}
		}

		// Create worktree with the new branch name (based on original branch)
		m.currentView = m.prevView
		m.branchRenameInput.Blur()
		m.allPRCreating = true
		m.setStatus("Creating worktree with new branch "+newBranch+"...", false)

		pr := m.branchRenamePR
		originalBranch := m.branchRenameOriginal
		return m, func() tea.Msg {
			name, _, err := m.wsManager.CreateWorktreeWithNewBranch(projectName, pr, originalBranch, newBranch)
			return WorktreeFromPRCreatedMsg{
				ProjectName:  projectName,
				WorktreeName: name,
				PRNumber:     pr.Number,
				Branch:       newBranch,
				Err:          err,
			}
		}
	}

	// Handle text input
	var cmd tea.Cmd
	m.branchRenameInput, cmd = m.branchRenameInput.Update(msg)
	return m, cmd
}

// buildArchivedWorktreesList populates the archived worktrees list for the current project
func (m *Model) buildArchivedWorktreesList() {
	m.archivedWorktrees = nil

	project, ok := m.config.Projects[m.selectedProject]
	if !ok {
		return
	}

	for name, wt := range project.Worktrees {
		if !wt.Archived {
			continue
		}

		info := archivedWorktreeInfo{
			Name:       name,
			Branch:     wt.Branch,
			ArchivedAt: wt.ArchivedAt,
		}

		// Check if logs exist
		setupLogs := workspace.GetSetupManager().GetLogs(m.selectedProject, name)
		archLogs := workspace.GetSetupManager().GetArchiveLogs(m.selectedProject, name)
		info.HasSetupLogs = setupLogs != ""
		info.HasArchLogs = archLogs != ""

		// Check for errors in archive logs
		if archLogs != "" && strings.Contains(archLogs, "FAILED") {
			// Extract error from logs
			lines := strings.Split(archLogs, "\n")
			for _, line := range lines {
				if strings.Contains(line, "error") || strings.Contains(line, "Error") || strings.Contains(line, "FAILED") {
					info.ArchiveError = strings.TrimSpace(line)
					break
				}
			}
		}

		m.archivedWorktrees = append(m.archivedWorktrees, info)
	}

	// Sort by archived date (most recent first)
	sort.Slice(m.archivedWorktrees, func(i, j int) bool {
		return m.archivedWorktrees[i].ArchivedAt.After(m.archivedWorktrees[j].ArchivedAt)
	})
}

// handleArchivedListView handles key events in the archived list view
func (m *Model) handleArchivedListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		return m, nil

	case msg.String() == "tab":
		// Toggle between archived worktrees and orphaned branches
		if m.archivedListMode == 0 {
			m.archivedListMode = 1
			m.archivedListCursor = 0
			m.archivedListOffset = 0
			// Load orphaned branches if not already loaded
			if m.orphanedBranches == nil && !m.orphanedLoading {
				m.orphanedLoading = true
				projectName := m.selectedProject
				return m, func() tea.Msg {
					project, ok := m.config.Projects[projectName]
					if !ok {
						return OrphanedBranchesScannedMsg{ProjectName: projectName, Err: fmt.Errorf("project not found")}
					}

					// Build map of known branches from worktrees
					knownBranches := make(map[string]bool)
					for _, wt := range project.Worktrees {
						knownBranches[wt.Branch] = true
					}

					branches, err := workspace.GitListOrphanedBranches(project.Path, knownBranches)
					return OrphanedBranchesScannedMsg{
						ProjectName: projectName,
						Branches:    branches,
						Err:         err,
					}
				}
			}
		} else {
			m.archivedListMode = 0
			m.archivedListCursor = 0
			m.archivedListOffset = 0
		}
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.archivedListCursor > 0 {
			m.archivedListCursor--
			m.ensureArchivedListCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		maxItems := m.getArchivedListItemCount()
		if m.archivedListCursor < maxItems-1 {
			m.archivedListCursor++
			m.ensureArchivedListCursorVisible()
		}

	case msg.String() == "l":
		// View logs for selected archived worktree
		if m.archivedListMode == 0 && m.archivedListCursor < len(m.archivedWorktrees) {
			wt := m.archivedWorktrees[m.archivedListCursor]
			m.logsWorktree = wt.Name
			m.logsScroll = 0
			m.logsAutoScroll = true
			m.logsType = "archive" // Default to archive logs for archived worktrees
			m.prevView = ViewArchivedList
			m.currentView = ViewLogs
		}

	case key.Matches(msg, m.keyMap.Delete):
		// Delete selected item
		if m.archivedListMode == 0 {
			// Delete archived worktree
			if m.archivedListCursor < len(m.archivedWorktrees) {
				wt := m.archivedWorktrees[m.archivedListCursor]
				m.deleteTarget = wt.Name
				m.deleteTargetType = "worktree-delete"
				m.prevView = ViewArchivedList
				m.currentView = ViewConfirmDelete
			}
		} else {
			// Delete orphaned branch
			if m.archivedListCursor < len(m.orphanedBranches) {
				branch := m.orphanedBranches[m.archivedListCursor]
				if branch.CheckedOutAt != "" {
					m.setStatus("Cannot delete: branch is checked out at "+branch.CheckedOutAt, true)
					return m, nil
				}
				projectName := m.selectedProject
				branchName := branch.Branch
				m.setStatus("Deleting branch "+branchName+"...", false)
				return m, func() tea.Msg {
					project, ok := m.config.Projects[projectName]
					if !ok {
						return BranchDeletedMsg{ProjectName: projectName, Branch: branchName, Err: fmt.Errorf("project not found")}
					}
					err := workspace.GitDeleteBranchForce(project.Path, branchName)
					return BranchDeletedMsg{ProjectName: projectName, Branch: branchName, Err: err}
				}
			}
		}

	case key.Matches(msg, m.keyMap.Refresh):
		// Refresh the list
		if m.archivedListMode == 0 {
			m.buildArchivedWorktreesList()
		} else {
			m.orphanedBranches = nil
			m.orphanedLoading = true
			projectName := m.selectedProject
			return m, func() tea.Msg {
				project, ok := m.config.Projects[projectName]
				if !ok {
					return OrphanedBranchesScannedMsg{ProjectName: projectName, Err: fmt.Errorf("project not found")}
				}
				knownBranches := make(map[string]bool)
				for _, wt := range project.Worktrees {
					knownBranches[wt.Branch] = true
				}
				branches, err := workspace.GitListOrphanedBranches(project.Path, knownBranches)
				return OrphanedBranchesScannedMsg{ProjectName: projectName, Branches: branches, Err: err}
			}
		}
	}

	return m, nil
}

// getArchivedListItemCount returns the number of items in the current archived list mode
func (m *Model) getArchivedListItemCount() int {
	if m.archivedListMode == 0 {
		return len(m.archivedWorktrees)
	}
	return len(m.orphanedBranches)
}

// ensureArchivedListCursorVisible adjusts offset to keep cursor visible
func (m *Model) ensureArchivedListCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.archivedListCursor < m.archivedListOffset {
		m.archivedListOffset = m.archivedListCursor
	} else if m.archivedListCursor >= m.archivedListOffset+tableHeight {
		m.archivedListOffset = m.archivedListCursor - tableHeight + 1
	}
}

// handleStatusHistoryView handles key events in the status history view
func (m *Model) handleStatusHistoryView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.statusHistoryCursor > 0 {
			m.statusHistoryCursor--
			m.ensureStatusHistoryCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.statusHistoryCursor < len(m.statusHistory)-1 {
			m.statusHistoryCursor++
			m.ensureStatusHistoryCursorVisible()
		}

	case msg.String() == "c":
		// Clear history
		m.statusHistory = nil
		m.currentView = m.prevView
		m.setStatus("History cleared", false)
		return m, nil
	}

	return m, nil
}

// ensureStatusHistoryCursorVisible adjusts offset to keep cursor visible
func (m *Model) ensureStatusHistoryCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.statusHistoryCursor < m.statusHistoryOffset {
		m.statusHistoryOffset = m.statusHistoryCursor
	} else if m.statusHistoryCursor >= m.statusHistoryOffset+tableHeight {
		m.statusHistoryOffset = m.statusHistoryCursor - tableHeight + 1
	}
}

// handleDatabasesView handles key events in the databases view
func (m *Model) handleDatabasesView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		return m, nil

	case msg.String() == "esc":
		// Cancel sync for the selected project if syncing
		if m.databaseCursor >= 0 && m.databaseCursor < len(m.databaseProjects) {
			projectName := m.databaseProjects[m.databaseCursor]
			if cancel, ok := m.databaseSyncCancel[projectName]; ok && m.databaseSyncing[projectName] {
				cancel()
				m.setStatus("Cancelling sync for "+projectName+"...", false)
			}
		}
		return m, nil

	case key.Matches(msg, m.keyMap.Up):
		if m.databaseCursor > 0 {
			m.databaseCursor--
			m.ensureDatabaseCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Down):
		if m.databaseCursor < len(m.databaseProjects)-1 {
			m.databaseCursor++
			m.ensureDatabaseCursorVisible()
		}

	case key.Matches(msg, m.keyMap.Enter), key.Matches(msg, m.keyMap.DatabaseSync):
		// Sync the selected project's database (incremental)
		if m.databaseCursor >= 0 && m.databaseCursor < len(m.databaseProjects) {
			projectName := m.databaseProjects[m.databaseCursor]
			return m, m.triggerDatabaseSync(projectName, false)
		}

	case key.Matches(msg, m.keyMap.DatabaseSyncForce):
		// Force sync the selected project's database
		if m.databaseCursor >= 0 && m.databaseCursor < len(m.databaseProjects) {
			projectName := m.databaseProjects[m.databaseCursor]
			return m, m.triggerDatabaseSync(projectName, true)
		}

	case key.Matches(msg, m.keyMap.DatabaseLogs):
		// Show logs for the selected project
		if m.databaseCursor >= 0 && m.databaseCursor < len(m.databaseProjects) {
			projectName := m.databaseProjects[m.databaseCursor]
			if logs, ok := m.databaseLogs[projectName]; ok && len(logs) > 0 {
				m.databaseLogsProject = projectName
				m.databaseLogsScroll = 0
				m.databaseLogsAuto = true
				m.prevView = m.currentView
				m.currentView = ViewDatabaseLogs
			} else {
				m.setStatus("No sync logs for "+projectName, false)
			}
		}

	case msg.String() == "1":
		m.currentView = ViewProjects
		m.cursor = 0
		m.offset = 0

	case msg.String() == "p":
		m.currentView = ViewPorts
		m.cursor = 0
		m.offset = 0
	}

	return m, nil
}

// handleDatabaseLogsView handles key events in the database logs view
func (m *Model) handleDatabaseLogsView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	logs := m.databaseLogs[m.databaseLogsProject]
	viewHeight := m.tableHeight()
	maxScroll := len(logs) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.currentView = m.prevView
		return m, nil

	case key.Matches(msg, m.keyMap.Up), msg.String() == "k":
		if m.databaseLogsScroll > 0 {
			m.databaseLogsScroll--
			m.databaseLogsAuto = false
		}

	case key.Matches(msg, m.keyMap.Down), msg.String() == "j":
		if m.databaseLogsScroll < maxScroll {
			m.databaseLogsScroll++
		}
		// Enable auto-scroll if at bottom
		if m.databaseLogsScroll >= maxScroll {
			m.databaseLogsAuto = true
		}

	case msg.String() == "a":
		// Toggle auto-scroll
		m.databaseLogsAuto = !m.databaseLogsAuto
		if m.databaseLogsAuto {
			m.databaseLogsScroll = maxScroll
		}

	case msg.String() == "G":
		// Jump to bottom
		m.databaseLogsScroll = maxScroll
		m.databaseLogsAuto = true

	case msg.String() == "g":
		// Jump to top
		m.databaseLogsScroll = 0
		m.databaseLogsAuto = false
	}

	return m, nil
}

// ensureDatabaseCursorVisible adjusts offset to keep cursor visible
func (m *Model) ensureDatabaseCursorVisible() {
	tableHeight := m.tableHeight()
	if tableHeight <= 0 {
		return
	}

	if m.databaseCursor < m.databaseOffset {
		m.databaseOffset = m.databaseCursor
	} else if m.databaseCursor >= m.databaseOffset+tableHeight {
		m.databaseOffset = m.databaseCursor - tableHeight + 1
	}
}

// triggerDatabaseSync validates and starts a database sync
func (m *Model) triggerDatabaseSync(projectName string, force bool) tea.Cmd {
	if m.databaseSyncing[projectName] {
		m.setStatus("Sync already in progress for "+projectName, false)
		return nil
	}

	project := m.config.Projects[projectName]
	if project == nil || project.Database == nil {
		m.setStatus("No database config for "+projectName, true)
		return nil
	}

	// Mark as syncing and clear old logs
	m.databaseSyncing[projectName] = true
	m.databaseProgress[projectName] = "Starting..."
	m.databaseLogs[projectName] = []string{fmt.Sprintf("[%s] Starting sync...", time.Now().Format("15:04:05"))}

	if force {
		m.setStatus("Starting force sync for "+projectName+"...", false)
	} else {
		m.setStatus("Checking for changes in "+projectName+"...", false)
	}

	// Start async sync with progress channel
	return m.startDatabaseSyncWithProgress(projectName, project.Database, force)
}

// startDatabaseSyncWithProgress starts an async database sync with real-time progress
// Uses V3 (golden database) approach for faster sync and time-based cooldown
func (m *Model) startDatabaseSyncWithProgress(projectName string, dbConfig *config.DatabaseConfig, force bool) tea.Cmd {
	// Create a channel for progress updates
	progressChan := make(chan string, 100)

	// Create context with cancel for interruption
	ctx, cancel := context.WithCancel(context.Background())

	// Store the cancel function so it can be called on Esc
	m.databaseSyncCancel[projectName] = cancel

	// Get localPostgresURL and conductorDir before starting goroutine
	defaults := m.store.GetDefaults()
	localPostgresURL := defaults.LocalPostgresURL

	// Start the sync in a goroutine
	go func() {
		defer close(progressChan)
		defer func() {
			// Clean up cancel function when done
			delete(m.databaseSyncCancel, projectName)
		}()

		startTime := time.Now()

		// Get the conductor directory
		conductorDir, err := config.ConductorDir()
		if err != nil {
			progressChan <- "ERROR:" + err.Error()
			return
		}

		// Create manager for V3 sync
		mgr := database.NewManager(localPostgresURL, conductorDir)

		// Check if sync is needed (unless force) - uses V3 time-based check
		if !force {
			progressChan <- "Checking for changes..."
			checkResult, err := mgr.CheckSyncNeeded(projectName, dbConfig)
			if err == nil && !checkResult.NeedsSync {
				progressChan <- "SKIP:" + checkResult.Reason
				return
			}
			if checkResult != nil && checkResult.NeedsSync {
				progressChan <- "Changes detected: " + checkResult.Reason
			}
		}

		// Check for cancellation before starting sync
		if ctx.Err() != nil {
			progressChan <- "CANCELLED:"
			return
		}

		// Progress callback
		progress := func(msg string) {
			select {
			case progressChan <- msg:
			default:
				// Channel full, skip
			}
		}

		progressChan <- "Starting sync..."

		// Run the V3 sync with progress and context for cancellation
		metadata, err := mgr.SyncProjectWithProgressCtx(ctx, projectName, dbConfig, progress)
		if err != nil {
			if ctx.Err() != nil {
				progressChan <- "CANCELLED:"
			} else {
				progressChan <- "ERROR:" + err.Error()
			}
			return
		}

		// Send completion info
		progressChan <- fmt.Sprintf("DONE:%d:%d:%d", len(metadata.TableSizes), len(metadata.ExcludedTables), time.Since(startTime).Milliseconds())
	}()

	// Return a command that reads from the channel
	return m.listenForSyncProgress(projectName, progressChan)
}

// listenForSyncProgress returns a command that listens for sync progress
func (m *Model) listenForSyncProgress(projectName string, progressChan <-chan string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-progressChan
		if !ok {
			// Channel closed without completion message - shouldn't happen normally
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Err:         fmt.Errorf("sync terminated unexpectedly"),
			}
		}

		// Check for special messages
		if strings.HasPrefix(msg, "SKIP:") {
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Skipped:     true,
				SkipReason:  strings.TrimPrefix(msg, "SKIP:"),
			}
		}

		if strings.HasPrefix(msg, "ERROR:") {
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Err:         errors.New(strings.TrimPrefix(msg, "ERROR:")),
			}
		}

		if strings.HasPrefix(msg, "CANCELLED:") {
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Cancelled:   true,
			}
		}

		if strings.HasPrefix(msg, "DONE:") {
			// Parse completion: DONE:tableCount:excludedCount:durationMs
			parts := strings.Split(strings.TrimPrefix(msg, "DONE:"), ":")
			tableCount := 0
			excludedCount := 0
			durationMs := int64(0)
			if len(parts) >= 3 {
				_, _ = fmt.Sscanf(parts[0], "%d", &tableCount)
				_, _ = fmt.Sscanf(parts[1], "%d", &excludedCount)
				_, _ = fmt.Sscanf(parts[2], "%d", &durationMs)
			}

			// Get golden file size
			homeDir, _ := os.UserHomeDir()
			dbsyncDir := filepath.Join(homeDir, ".conductor", "dbsync")
			goldenPath := filepath.Join(dbsyncDir, projectName, "golden.sql")
			var goldenSize int64
			if info, err := os.Stat(goldenPath); err == nil {
				goldenSize = info.Size()
			}

			return DatabaseSyncCompletedMsg{
				ProjectName:    projectName,
				GoldenFileSize: goldenSize,
				TableCount:     tableCount,
				ExcludedCount:  excludedCount,
				DurationMs:     durationMs,
			}
		}

		// Regular progress message - return it and continue listening
		return progressMsgWithContinue{
			progress: DatabaseSyncProgressMsg{
				ProjectName: projectName,
				Message:     msg,
			},
			next: func() tea.Msg {
				// Continue listening
				return listenForMoreProgress(projectName, progressChan)()
			},
		}
	}
}

// progressMsgWithContinue wraps a progress message with a continuation
type progressMsgWithContinue struct {
	progress DatabaseSyncProgressMsg
	next     func() tea.Msg
}

// listenForMoreProgress continues listening for progress
func listenForMoreProgress(projectName string, progressChan <-chan string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-progressChan
		if !ok {
			// Channel closed - sync complete
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
			}
		}

		// Check for special messages
		if strings.HasPrefix(msg, "SKIP:") {
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Skipped:     true,
				SkipReason:  strings.TrimPrefix(msg, "SKIP:"),
			}
		}

		if strings.HasPrefix(msg, "ERROR:") {
			return DatabaseSyncCompletedMsg{
				ProjectName: projectName,
				Err:         errors.New(strings.TrimPrefix(msg, "ERROR:")),
			}
		}

		if strings.HasPrefix(msg, "DONE:") {
			parts := strings.Split(strings.TrimPrefix(msg, "DONE:"), ":")
			tableCount := 0
			excludedCount := 0
			durationMs := int64(0)
			if len(parts) >= 3 {
				_, _ = fmt.Sscanf(parts[0], "%d", &tableCount)
				_, _ = fmt.Sscanf(parts[1], "%d", &excludedCount)
				_, _ = fmt.Sscanf(parts[2], "%d", &durationMs)
			}

			homeDir, _ := os.UserHomeDir()
			dbsyncDir := filepath.Join(homeDir, ".conductor", "dbsync")
			goldenPath := filepath.Join(dbsyncDir, projectName, "golden.sql")
			var goldenSize int64
			if info, err := os.Stat(goldenPath); err == nil {
				goldenSize = info.Size()
			}

			return DatabaseSyncCompletedMsg{
				ProjectName:    projectName,
				GoldenFileSize: goldenSize,
				TableCount:     tableCount,
				ExcludedCount:  excludedCount,
				DurationMs:     durationMs,
			}
		}

		// Regular progress
		return progressMsgWithContinue{
			progress: DatabaseSyncProgressMsg{
				ProjectName: projectName,
				Message:     msg,
			},
			next: func() tea.Msg {
				return listenForMoreProgress(projectName, progressChan)()
			},
		}
	}
}

// loadDatabaseMetadata creates a command that loads sync metadata from disk for all projects
// This is used to populate SyncStatus from metadata.json files created by CLI sync commands
func (m *Model) loadDatabaseMetadata() tea.Cmd {
	return func() tea.Msg {
		result := make(map[string]*config.DatabaseSyncStatus)

		// Get dbsync directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return DatabaseMetadataLoadedMsg{Metadata: result}
		}
		dbsyncDir := filepath.Join(homeDir, ".conductor", "dbsync")

		// Load metadata for each project with database config
		for projectName, project := range m.config.Projects {
			if project.Database == nil || project.Database.Source == "" {
				continue
			}

			// Skip if already has valid SyncStatus
			if project.Database.SyncStatus != nil && project.Database.SyncStatus.LastSyncAt != "" {
				continue
			}

			// Try to load metadata from disk
			metadata, err := database.LoadSyncMetadata(projectName, dbsyncDir)
			if err != nil || metadata == nil {
				continue
			}

			// Convert SyncMetadata to DatabaseSyncStatus
			syncStatus := &config.DatabaseSyncStatus{
				LastSyncAt:     metadata.LastSyncAt.Format("2006-01-02 15:04"),
				GoldenCopySize: metadata.GoldenFileSize,
				TableCount:     len(metadata.TableSizes),
				ExcludedCount:  len(metadata.ExcludedTables),
				Status:         "synced",
			}

			// Check if there was an error
			if metadata.Error != "" {
				syncStatus.Status = "failed"
				syncStatus.LastError = metadata.Error
			}

			result[projectName] = syncStatus
		}

		return DatabaseMetadataLoadedMsg{Metadata: result}
	}
}
