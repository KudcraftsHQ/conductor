package clickup

import "time"

// TaskPriority represents a ClickUp task priority
type TaskPriority struct {
	ID       string `json:"id"`
	Priority string `json:"priority"` // "1" (urgent) to "4" (low)
	Color    string `json:"color"`
}

// Dependency represents a task dependency in ClickUp
type Dependency struct {
	TaskID       string `json:"task_id"`
	DependsOn    string `json:"depends_on"`
	Type         int    `json:"type"` // 0 = waiting on, 1 = blocking
	DependencyOf string `json:"dependency_of,omitempty"`
}

// Task represents a ClickUp task
type Task struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Status       TaskStatus    `json:"status"`
	URL          string        `json:"url"`
	List         TaskList      `json:"list"`
	DateUpdated  string        `json:"date_updated"`
	Priority     *TaskPriority `json:"priority"`
	OrderIndex   string        `json:"orderindex"`
	Dependencies []Dependency  `json:"dependencies"`
}

// TaskStatus represents a ClickUp task status
type TaskStatus struct {
	Status string `json:"status"`
	Type   string `json:"type"`
}

// TaskList represents the list a task belongs to
type TaskList struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// WebhookPayload represents an incoming ClickUp webhook event
type WebhookPayload struct {
	Event        string        `json:"event"`
	TaskID       string        `json:"task_id"`
	HistoryItems []HistoryItem `json:"history_items"`
	WebhookID    string        `json:"webhook_id"`
}

// HistoryItem represents a change in a webhook event
type HistoryItem struct {
	Field  string      `json:"field"`
	Before StatusValue `json:"before"`
	After  StatusValue `json:"after"`
}

// StatusValue represents a status value in a history item
type StatusValue struct {
	Status string `json:"status"`
	Type   string `json:"type"`
}

// WebhookRegistration is the response from registering a webhook
type WebhookRegistration struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// WebhookRequest is the request body for webhook registration
type WebhookRequest struct {
	Endpoint string   `json:"endpoint"`
	Events   []string `json:"events"`
}

// TaskEvent represents a processed task status change event
type TaskEvent struct {
	TaskID    string
	Task      *Task
	NewStatus string
	OldStatus string
	Timestamp time.Time
}

// Comment represents a ClickUp task comment
type Comment struct {
	CommentText string `json:"comment_text"`
}

// TasksResponse wraps the ClickUp API response for listing tasks
type TasksResponse struct {
	Tasks []Task `json:"tasks"`
}
