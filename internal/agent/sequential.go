package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/clickup"
	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
)

// activeTask tracks a currently running sequential task
type activeTask struct {
	ProjectName string    `json:"projectName"`
	TaskID      string    `json:"taskId"`
	TaskName    string    `json:"taskName"`
	PaneID      string    `json:"paneId"`
	WindowName  string    `json:"windowName"`
	StartedAt   time.Time `json:"startedAt"`
}

// SequentialHandler manages one-at-a-time task execution per project
type SequentialHandler struct {
	mu          sync.Mutex
	activeTasks map[string]*activeTask // projectName → active task
	client      *clickup.Client
	picker      *TaskPicker
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewSequentialHandler creates a new sequential handler
func NewSequentialHandler(client *clickup.Client) *SequentialHandler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &SequentialHandler{
		activeTasks: make(map[string]*activeTask),
		client:      client,
		picker:      NewTaskPicker(client),
		ctx:         ctx,
		cancel:      cancel,
	}
	h.loadState()
	h.recoverState()
	return h
}

// HasActiveTask returns true if the project has a task currently running
func (h *SequentialHandler) HasActiveTask(projectName string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activeTasks[projectName] != nil
}

// GetActiveTask returns the active task for a project, if any
func (h *SequentialHandler) GetActiveTask(projectName string) *activeTask {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activeTasks[projectName]
}

// StartTask starts a sequential task for a project
func (h *SequentialHandler) StartTask(projectName string, task *clickup.Task, projectConfig *config.ProjectClickUpConfig, projectPath string) error {
	h.mu.Lock()
	if h.activeTasks[projectName] != nil {
		h.mu.Unlock()
		return fmt.Errorf("project %s already has an active task", projectName)
	}
	h.mu.Unlock()

	taskURL := task.URL
	if taskURL == "" {
		taskURL = fmt.Sprintf("https://app.clickup.com/t/%s", task.ID)
	}

	// Create tmux window in the project's root worktree path
	paneID, windowName, err := h.createSequentialWindow(projectName, task, projectPath, taskURL)
	if err != nil {
		return fmt.Errorf("failed to create sequential window: %w", err)
	}

	at := &activeTask{
		ProjectName: projectName,
		TaskID:      task.ID,
		TaskName:    task.Name,
		PaneID:      paneID,
		WindowName:  windowName,
		StartedAt:   time.Now(),
	}

	h.mu.Lock()
	h.activeTasks[projectName] = at
	h.mu.Unlock()

	h.saveState()

	log.Printf("sequential: started task %q (ID: %s) for project %s [pane: %s]", task.Name, task.ID, projectName, paneID)

	// Start monitoring for completion
	go h.monitorCompletion(projectName, at, projectConfig, projectPath)

	return nil
}

// Stop shuts down the sequential handler
func (h *SequentialHandler) Stop() {
	h.cancel()
}

// createSequentialWindow creates a multiplexer window for sequential mode.
// Returns the agent pane ID and window name.
func (h *SequentialHandler) createSequentialWindow(projectName string, task *clickup.Task, projectPath, taskURL string) (string, string, error) {
	windowName := fmt.Sprintf("%s/seq-%s", projectName, task.ID)
	taskPrompt := BuildSequentialTaskPrompt(task.Name, task.Description, taskURL)

	agent := codingagent.ClaudeCode // Default to Claude Code for agent daemon
	paneTitle := fmt.Sprintf("%s - %s (sequential)", task.Name, agent.PaneLabel())

	paneID, err := mux.Current().StartAgentPane(windowName, projectPath, agent.TaskArgs("", taskPrompt), paneTitle)
	if err != nil {
		return "", "", err
	}
	return paneID, windowName, nil
}

// isShellCommand reports whether a pane command means "no agent is running".
func isShellCommand(cmd string) bool {
	switch cmd {
	case "", "bash", "zsh", "fish", "sh":
		return true
	}
	return false
}

// monitorCompletion polls the agent pane to detect when the agent exits.
//
// The pane command only becomes non-shell once the agent process is actually
// running, and under herdr that means once herdr has *detected* the agent —
// which lags pane creation. So completion is only inferred after the agent has
// been seen at least once; before that, an empty reading means "still starting".
func (h *SequentialHandler) monitorCompletion(projectName string, at *activeTask, projectConfig *config.ProjectClickUpConfig, projectPath string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	agentSeen := false

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			m := mux.Current()
			if !m.PaneExists(at.PaneID) {
				// Pane was killed — treat as completion
				log.Printf("sequential: pane %s gone for task %s, treating as completed", at.PaneID, at.TaskID)
				h.handleTaskCompletion(projectName, at, projectConfig, projectPath)
				return
			}

			cmd := m.GetPaneCommand(at.PaneID)
			if !isShellCommand(cmd) {
				agentSeen = true
				continue
			}
			if !agentSeen {
				// Agent has not started (or been detected) yet.
				continue
			}
			log.Printf("sequential: agent finished for task %s (pane command: %q)", at.TaskID, cmd)
			h.handleTaskCompletion(projectName, at, projectConfig, projectPath)
			return
		}
	}
}

// handleTaskCompletion processes a completed sequential task
func (h *SequentialHandler) handleTaskCompletion(projectName string, at *activeTask, projectConfig *config.ProjectClickUpConfig, projectPath string) {
	doneStatus := projectConfig.GetDoneStatus()

	// Move task to done status in ClickUp
	if err := h.client.UpdateTaskStatus(at.TaskID, doneStatus); err != nil {
		log.Printf("sequential: failed to update task %s to %q: %v", at.TaskID, doneStatus, err)
	} else {
		log.Printf("sequential: moved task %s to %q", at.TaskID, doneStatus)
	}

	// Add completion comment
	comment := "Task completed by conductor agent (sequential mode). Changes committed directly to main branch."
	if err := h.client.AddTaskComment(at.TaskID, comment); err != nil {
		log.Printf("sequential: failed to add comment to task %s: %v", at.TaskID, err)
	}

	// NOTE: tmux window is NOT closed — user reviews manually

	// Clear active task
	h.mu.Lock()
	delete(h.activeTasks, projectName)
	h.mu.Unlock()
	h.saveState()

	// Auto-pick next task if enabled
	if projectConfig.AutoPick {
		h.autoPickNext(projectName, projectConfig, projectPath)
	}
}

// autoPickNext uses the AI picker to select and start the next task
func (h *SequentialHandler) autoPickNext(projectName string, projectConfig *config.ProjectClickUpConfig, projectPath string) {
	log.Printf("sequential: auto-picking next task for project %s", projectName)

	task, err := h.picker.PickNextTask(projectConfig.ListID, projectConfig.GetReadyStatus())
	if err != nil {
		log.Printf("sequential: no next task available for %s: %v", projectName, err)
		return
	}

	log.Printf("sequential: auto-picked task %q (ID: %s) for project %s", task.Name, task.ID, projectName)

	// Move picked task to trigger status so the flow is consistent
	triggerStatus := projectConfig.TriggerStatus
	if triggerStatus == "" {
		triggerStatus = "in progress"
	}
	if err := h.client.UpdateTaskStatus(task.ID, triggerStatus); err != nil {
		log.Printf("sequential: failed to move picked task %s to %q: %v", task.ID, triggerStatus, err)
		return
	}

	// Start the task directly
	if err := h.StartTask(projectName, task, projectConfig, projectPath); err != nil {
		log.Printf("sequential: failed to start auto-picked task %s: %v", task.ID, err)
	}
}

// InitialAutoPick triggers auto-pick for idle sequential+autoPick projects
func (h *SequentialHandler) InitialAutoPick(projectName string, projectConfig *config.ProjectClickUpConfig, projectPath string) {
	if !projectConfig.AutoPick || projectConfig.GetMode() != config.AgentModeSequential {
		return
	}
	if h.HasActiveTask(projectName) {
		return
	}
	h.autoPickNext(projectName, projectConfig, projectPath)
}

// State persistence

func (h *SequentialHandler) stateFilePath() string {
	dir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent-sequential-state.json")
}

func (h *SequentialHandler) saveState() {
	path := h.stateFilePath()
	if path == "" {
		return
	}

	h.mu.Lock()
	data, err := json.MarshalIndent(h.activeTasks, "", "  ")
	h.mu.Unlock()

	if err != nil {
		log.Printf("sequential: failed to marshal state: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("sequential: failed to save state: %v", err)
	}
}

func (h *SequentialHandler) loadState() {
	path := h.stateFilePath()
	if path == "" {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return // No state file is fine
	}

	var tasks map[string]*activeTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		log.Printf("sequential: failed to parse state file: %v", err)
		return
	}

	h.mu.Lock()
	h.activeTasks = tasks
	h.mu.Unlock()
}

// recoverState checks persisted active tasks and cleans up stale ones
func (h *SequentialHandler) recoverState() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for projectName, at := range h.activeTasks {
		if !mux.Current().PaneExists(at.PaneID) {
			log.Printf("sequential: recovering stale task %s for project %s (pane gone)", at.TaskID, projectName)
			// Mark as done since pane is gone (Claude likely finished)
			doneStatus := "done" // Use default since we don't have project config here
			if err := h.client.UpdateTaskStatus(at.TaskID, doneStatus); err != nil {
				log.Printf("sequential: failed to update recovered task %s: %v", at.TaskID, err)
			}
			delete(h.activeTasks, projectName)
		}
	}
}
