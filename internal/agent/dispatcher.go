package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/clickup"
	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/workspace"
)

// Dispatcher receives ClickUp task events and maps them to conductor worktree lifecycle
type Dispatcher struct {
	store      *store.Store
	manager    *workspace.Manager
	clickupMgr *clickup.Manager
	seqHandler *SequentialHandler
}

// NewDispatcher creates a new task dispatcher
func NewDispatcher(s *store.Store, mgr *workspace.Manager, clickupMgr *clickup.Manager, seqHandler *SequentialHandler) *Dispatcher {
	return &Dispatcher{
		store:      s,
		manager:    mgr,
		clickupMgr: clickupMgr,
		seqHandler: seqHandler,
	}
}

// HandleEvent processes a ClickUp task event
func (d *Dispatcher) HandleEvent(event clickup.TaskEvent) {
	if event.Task == nil {
		log.Printf("dispatcher: received event with nil task for %s", event.TaskID)
		return
	}

	// Find project by matching task list ID to project config
	projectName, projectConfig, project, err := d.findProjectWithConfig(event.Task.List.ID)
	if err != nil {
		log.Printf("dispatcher: %v", err)
		return
	}

	log.Printf("dispatcher: processing task '%s' (ID: %s) for project %s", event.Task.Name, event.TaskID, projectName)

	// Route by mode
	if projectConfig.ClickUp != nil && projectConfig.ClickUp.GetMode() == config.AgentModeSequential {
		d.handleSequentialEvent(projectName, event, projectConfig.ClickUp, project.Path)
	} else {
		d.handleParallelEvent(projectName, event)
	}
}

// handleSequentialEvent processes a task in sequential mode
func (d *Dispatcher) handleSequentialEvent(projectName string, event clickup.TaskEvent, clickupConfig *config.ProjectClickUpConfig, projectPath string) {
	if d.seqHandler.HasActiveTask(projectName) {
		log.Printf("dispatcher: project %s already has an active sequential task, skipping %s", projectName, event.TaskID)
		return
	}

	if err := d.seqHandler.StartTask(projectName, event.Task, clickupConfig, projectPath); err != nil {
		log.Printf("dispatcher: failed to start sequential task %s: %v", event.TaskID, err)
	}
}

// handleParallelEvent processes a task in parallel mode (existing worktree-based flow)
func (d *Dispatcher) handleParallelEvent(projectName string, event clickup.TaskEvent) {
	// Check if worktree already exists for this task
	if d.worktreeExistsForTask(projectName, event.TaskID) {
		log.Printf("dispatcher: worktree already exists for task %s", event.TaskID)
		return
	}

	// Generate branch name
	branch := GenerateBranchName(event.TaskID, event.Task.Name)

	// Get project's default port count
	portCount := d.store.GetProjectDefaultPorts(projectName)

	// Create worktree
	worktreeName, wt, err := d.manager.PrepareWorktree(projectName, branch, portCount)
	if err != nil {
		log.Printf("dispatcher: failed to prepare worktree for task %s: %v", event.TaskID, err)
		return
	}

	// Set ClickUp task linkage
	taskURL := event.Task.URL
	if taskURL == "" {
		taskURL = fmt.Sprintf("https://app.clickup.com/t/%s", event.TaskID)
	}
	_ = d.store.SetWorktreeClickUpTask(projectName, worktreeName, event.TaskID, taskURL)

	log.Printf("dispatcher: created worktree %s (branch: %s) for task %s", worktreeName, branch, event.TaskID)

	// Create git worktree async
	err = d.manager.CreateWorktreeAsync(projectName, worktreeName, func(success bool, createErr error) {
		if !success {
			log.Printf("dispatcher: failed to create git worktree for task %s: %v", event.TaskID, createErr)
			_ = d.store.SetWorktreeStatus(projectName, worktreeName, config.SetupStatusFailed)
			return
		}

		// Run setup async
		err := d.manager.RunSetupAsync(projectName, worktreeName, func(setupSuccess bool, setupErr error) {
			if !setupSuccess {
				log.Printf("dispatcher: setup failed for task %s: %v", event.TaskID, setupErr)
			}

			// Open tmux window with claude task prompt regardless of setup result
			d.openCodingWindow(projectName, worktreeName, wt, event.Task)
		})
		if err != nil {
			log.Printf("dispatcher: failed to run setup for task %s: %v", event.TaskID, err)
			// Still try to open the window
			d.openCodingWindow(projectName, worktreeName, wt, event.Task)
		}
	})
	if err != nil {
		log.Printf("dispatcher: failed to create worktree async for task %s: %v", event.TaskID, err)
	}
}

// findProjectWithConfig finds a project by ClickUp list ID and returns all relevant data
func (d *Dispatcher) findProjectWithConfig(listID string) (string, *config.ProjectConfig, *config.Project, error) {
	projects := d.store.GetAllProjects()

	for projectName, project := range projects {
		projectConfig, err := config.LoadProjectConfig(project.Path)
		if err != nil || projectConfig == nil {
			continue
		}

		if projectConfig.ClickUp != nil && projectConfig.ClickUp.ListID == listID {
			return projectName, projectConfig, project, nil
		}
	}

	return "", nil, nil, fmt.Errorf("no project found for ClickUp list ID %s", listID)
}

// worktreeExistsForTask checks if a worktree already exists for a ClickUp task
func (d *Dispatcher) worktreeExistsForTask(projectName, taskID string) bool {
	worktrees := d.store.GetAllWorktrees(projectName)
	for _, wt := range worktrees {
		if wt.ClickUpTaskID == taskID && !wt.Archived {
			return true
		}
	}
	return false
}

// openCodingWindow opens a tmux window with claude pre-loaded with the task prompt
func (d *Dispatcher) openCodingWindow(projectName, worktreeName string, wt *config.Worktree, task *clickup.Task) {
	taskURL := fmt.Sprintf("https://app.clickup.com/t/%s", task.ID)
	taskPrompt := BuildTaskPrompt(task.Name, task.Description, taskURL)

	if err := mux.Current().CreateCodingWindowWithTask(projectName, wt.Branch, wt.Path, taskPrompt, codingagent.ClaudeCode); err != nil {
		log.Printf("dispatcher: failed to create coding window for task %s: %v", task.ID, err)
	}
}

// Daemon represents the full agent daemon lifecycle
type Daemon struct {
	store      *store.Store
	dispatcher *Dispatcher
	watcher    *PRWatcher
	clickupMgr *clickup.Manager
	seqHandler *SequentialHandler

	ctx    context.Context
	cancel context.CancelFunc
}

// NewDaemon creates a new agent daemon
func NewDaemon(s *store.Store, clickupCfg *config.ClickUpConfig, tunnelMgr interface{}) (*Daemon, error) {
	if clickupCfg == nil || clickupCfg.APIToken == "" {
		return nil, fmt.Errorf("ClickUp not configured. Run 'conductor agent setup' first")
	}

	ctx, cancel := context.WithCancel(context.Background())

	// We pass nil for tunnel manager since we handle it in the clickup manager
	// The tunnel.Manager is only needed if we want webhook mode
	var tunnelMgrTyped interface{ IsCloudflaredInstalled() bool }
	if tm, ok := tunnelMgr.(interface{ IsCloudflaredInstalled() bool }); ok {
		tunnelMgrTyped = tm
		_ = tunnelMgrTyped // Used by clickup manager
	}

	cfg, err := config.Load()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	mgr := workspace.NewManagerWithStore(cfg, s)
	clickupMgr := clickup.NewManager(clickupCfg, nil) // tunnel support added later

	seqHandler := NewSequentialHandler(clickupMgr.Client())

	dispatcher := NewDispatcher(s, mgr, clickupMgr, seqHandler)
	clickupMgr.SetEventHandler(dispatcher.HandleEvent)

	watcher := NewPRWatcher(s, clickupMgr.Client(), 60*time.Second)

	return &Daemon{
		store:      s,
		dispatcher: dispatcher,
		watcher:    watcher,
		clickupMgr: clickupMgr,
		seqHandler: seqHandler,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Start starts the agent daemon
func (d *Daemon) Start() error {
	// Collect list IDs from all projects
	listIDs := d.collectListIDs()
	if len(listIDs) == 0 {
		return fmt.Errorf("no projects have ClickUp list IDs configured. Set clickup.listId in project conductor.json")
	}

	// Start ClickUp event listener
	if err := d.clickupMgr.Start(listIDs); err != nil {
		return fmt.Errorf("failed to start ClickUp manager: %w", err)
	}

	// Start PR watcher
	go d.watcher.Start(d.ctx)

	// Trigger initial auto-pick for idle sequential+autoPick projects
	d.initialAutoPick()

	log.Printf("agent daemon started (mode: %s, watching %d lists)", d.clickupMgr.Mode(), len(listIDs))
	return nil
}

// Stop gracefully shuts down the daemon
func (d *Daemon) Stop() error {
	d.cancel()
	d.seqHandler.Stop()
	return d.clickupMgr.Stop()
}

// Wait blocks until the daemon context is cancelled
func (d *Daemon) Wait() {
	<-d.ctx.Done()
}

// Mode returns the current event mode
func (d *Daemon) Mode() string {
	return d.clickupMgr.Mode()
}

// initialAutoPick triggers auto-pick for idle sequential+autoPick projects on daemon start
func (d *Daemon) initialAutoPick() {
	projects := d.store.GetAllProjects()
	for projectName, project := range projects {
		projectConfig, err := config.LoadProjectConfig(project.Path)
		if err != nil || projectConfig == nil || projectConfig.ClickUp == nil {
			continue
		}
		go d.seqHandler.InitialAutoPick(projectName, projectConfig.ClickUp, project.Path)
	}
}

// collectListIDs gathers ClickUp list IDs from all project configs
func (d *Daemon) collectListIDs() []string {
	var listIDs []string
	projects := d.store.GetAllProjects()

	for _, project := range projects {
		projectConfig, err := config.LoadProjectConfig(project.Path)
		if err != nil || projectConfig == nil {
			continue
		}

		if projectConfig.ClickUp != nil && projectConfig.ClickUp.ListID != "" {
			listIDs = append(listIDs, projectConfig.ClickUp.ListID)
		}
	}

	return listIDs
}

// collectProjectInfo gathers info about watched projects for status display
func (d *Daemon) CollectProjectInfo() []ProjectInfo {
	var infos []ProjectInfo
	projects := d.store.GetAllProjects()

	for projectName, project := range projects {
		projectConfig, err := config.LoadProjectConfig(project.Path)
		if err != nil || projectConfig == nil {
			continue
		}

		if projectConfig.ClickUp != nil && projectConfig.ClickUp.ListID != "" {
			triggerStatus := projectConfig.ClickUp.TriggerStatus
			if triggerStatus == "" {
				triggerStatus = "in progress"
			}

			mode := projectConfig.ClickUp.GetMode()

			// Count active agent worktrees
			activeCount := 0
			worktrees := d.store.GetAllWorktrees(projectName)
			for _, wt := range worktrees {
				if wt.ClickUpTaskID != "" && !wt.Archived {
					activeCount++
				}
			}

			// Get active sequential task name
			var activeTaskName string
			if mode == config.AgentModeSequential {
				if at := d.seqHandler.GetActiveTask(projectName); at != nil {
					activeTaskName = at.TaskName
				}
			}

			infos = append(infos, ProjectInfo{
				Name:            projectName,
				ListID:          projectConfig.ClickUp.ListID,
				TriggerStatus:   triggerStatus,
				ActiveWorktrees: activeCount,
				Mode:            mode,
				ActiveTask:      activeTaskName,
			})
		}
	}

	return infos
}

// ProjectInfo contains display info for a watched project
type ProjectInfo struct {
	Name            string
	ListID          string
	TriggerStatus   string
	ActiveWorktrees int
	Mode            config.AgentMode
	ActiveTask      string // sequential mode: name of current task, or ""
}

// slugify converts a string for use in branch names
//
//nolint:unused // wired up in a follow-up
func slugify(s string) string {
	s = strings.ToLower(s)
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		} else if r == ' ' {
			result.WriteRune('-')
		}
	}
	return strings.Trim(result.String(), "-")
}
