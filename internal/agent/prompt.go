package agent

import (
	"fmt"
	"strings"
)

// BuildTaskPrompt constructs the claude task prompt from ClickUp task data
func BuildTaskPrompt(title, description, taskURL string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Task: %s\n", title))
	sb.WriteString(fmt.Sprintf("URL: %s\n", taskURL))
	sb.WriteString("\n")

	if description != "" {
		sb.WriteString(description)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Please implement this task. When done, run /test-feature to automatically test the implementation against this spec. Only create a PR after tests pass.")

	return sb.String()
}

// SlugifyTitle creates a URL-safe slug from a task title
func SlugifyTitle(title string) string {
	// Lowercase
	slug := strings.ToLower(title)

	// Replace spaces and special chars with hyphens
	var result strings.Builder
	lastHyphen := false
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			result.WriteRune('-')
			lastHyphen = true
		}
	}

	s := strings.Trim(result.String(), "-")

	// Truncate to 50 chars
	if len(s) > 50 {
		s = s[:50]
		// Don't end on a hyphen
		s = strings.TrimRight(s, "-")
	}

	return s
}

// GenerateBranchName creates a branch name from a ClickUp task
func GenerateBranchName(taskID, title string) string {
	slug := SlugifyTitle(title)
	if slug == "" {
		return fmt.Sprintf("feature/%s", taskID)
	}
	return fmt.Sprintf("feature/%s-%s", taskID, slug)
}

// BuildSequentialTaskPrompt constructs a prompt for sequential mode (no PRs, commit directly)
func BuildSequentialTaskPrompt(title, description, taskURL string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Task: %s\n", title))
	sb.WriteString(fmt.Sprintf("URL: %s\n", taskURL))
	sb.WriteString("\n")

	if description != "" {
		sb.WriteString(description)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Please implement this task. When done, run /test-feature to automatically test the implementation against this spec. Only commit after tests pass. Do NOT create a PR or switch branches.")

	return sb.String()
}

// TaskSummary contains minimal task info for the AI picker prompt
type TaskSummary struct {
	ID           string
	Name         string
	Priority     string // "urgent", "high", "normal", "low", or ""
	Description  string // truncated
	Dependencies []string
}

// BuildTaskPickerPrompt builds a prompt for Claude to pick the most important task
func BuildTaskPickerPrompt(tasks []TaskSummary) string {
	var sb strings.Builder

	sb.WriteString("You are a task prioritization assistant. Given the following tasks, pick the single most important one to work on next.\n\n")
	sb.WriteString("Consider: priority level, dependencies (don't pick tasks with unresolved blockers), and task importance.\n\n")
	sb.WriteString("Tasks:\n")

	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("\n- ID: %s\n  Name: %s\n", t.ID, t.Name))
		if t.Priority != "" {
			sb.WriteString(fmt.Sprintf("  Priority: %s\n", t.Priority))
		}
		if len(t.Dependencies) > 0 {
			sb.WriteString(fmt.Sprintf("  Depends on: %s\n", strings.Join(t.Dependencies, ", ")))
		}
		if t.Description != "" {
			desc := t.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("  Description: %s\n", desc))
		}
	}

	sb.WriteString("\nRespond with ONLY the task ID of the most important task. Nothing else.")

	return sb.String()
}
