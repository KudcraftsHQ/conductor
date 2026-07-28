package agent

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/hammashamzah/conductor/internal/clickup"
	"github.com/hammashamzah/conductor/internal/codingagent"
)

// TaskPicker uses AI to select the most important task from a list
type TaskPicker struct {
	client *clickup.Client
}

// NewTaskPicker creates a new TaskPicker
func NewTaskPicker(client *clickup.Client) *TaskPicker {
	return &TaskPicker{client: client}
}

// PickNextTask fetches ready tasks and uses Claude to pick the best one
func (p *TaskPicker) PickNextTask(listID, readyStatus string) (*clickup.Task, error) {
	tasks, err := p.client.GetFilteredTasks(listID, []string{readyStatus})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ready tasks: %w", err)
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks found with status %q", readyStatus)
	}

	// If only one task, return it directly
	if len(tasks) == 1 {
		return &tasks[0], nil
	}

	// Build summaries for the picker prompt
	summaries := make([]TaskSummary, len(tasks))
	for i, t := range tasks {
		s := TaskSummary{
			ID:   t.ID,
			Name: t.Name,
		}
		if t.Priority != nil {
			s.Priority = priorityLabel(t.Priority.Priority)
		}
		if t.Description != "" {
			s.Description = t.Description
		}
		for _, dep := range t.Dependencies {
			s.Dependencies = append(s.Dependencies, dep.DependsOn)
		}
		summaries[i] = s
	}

	prompt := BuildTaskPickerPrompt(summaries)
	pickedID, err := askAgent(prompt)
	if err != nil {
		log.Printf("picker: Claude failed, falling back to first task: %v", err)
		return &tasks[0], nil
	}

	// Find the picked task
	pickedID = strings.TrimSpace(pickedID)
	for i, t := range tasks {
		if t.ID == pickedID {
			return &tasks[i], nil
		}
	}

	// Claude returned an invalid ID, fall back
	log.Printf("picker: Claude returned unknown task ID %q, falling back to first task", pickedID)
	return &tasks[0], nil
}

// askAgent runs a one-shot prompt via the coding agent and returns the response
func askAgent(prompt string) (string, error) {
	agent := codingagent.ClaudeCode // Default to Claude Code for task picking
	args := agent.OneShotArgs(prompt)
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s command failed: %w", agent.BinaryName(), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// priorityLabel converts ClickUp priority ID to human-readable label
func priorityLabel(id string) string {
	switch id {
	case "1":
		return "urgent"
	case "2":
		return "high"
	case "3":
		return "normal"
	case "4":
		return "low"
	default:
		return ""
	}
}
