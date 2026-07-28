package clickup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.clickup.com/api/v2"

// Client handles ClickUp API communication
type Client struct {
	apiToken   string
	httpClient *http.Client
}

// NewClient creates a new ClickUp API client
func NewClient(apiToken string) *Client {
	return &Client{
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetTask fetches a task by ID
func (c *Client) GetTask(taskID string) (*Task, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/task/%s", taskID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var task Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode task: %w", err)
	}
	return &task, nil
}

// GetFilteredTasks fetches tasks from a list filtered by status
func (c *Client) GetFilteredTasks(listID string, statuses []string) ([]Task, error) {
	url := fmt.Sprintf("/list/%s/task?", listID)
	for i, status := range statuses {
		if i > 0 {
			url += "&"
		}
		url += fmt.Sprintf("statuses[]=%s", status)
	}

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result TasksResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode tasks: %w", err)
	}
	return result.Tasks, nil
}

// UpdateTaskStatus updates a task's status
func (c *Client) UpdateTaskStatus(taskID, status string) error {
	body := map[string]interface{}{
		"status": status,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal body: %w", err)
	}

	resp, err := c.doRequest("PUT", fmt.Sprintf("/task/%s", taskID), bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// AddTaskComment adds a comment to a task
func (c *Client) AddTaskComment(taskID, commentText string) error {
	body := Comment{CommentText: commentText}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal comment: %w", err)
	}

	resp, err := c.doRequest("POST", fmt.Sprintf("/task/%s/comment", taskID), bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to add comment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// RegisterWebhook registers a webhook for task status updates
func (c *Client) RegisterWebhook(teamID, endpoint string) (*WebhookRegistration, error) {
	body := WebhookRequest{
		Endpoint: endpoint,
		Events:   []string{"taskStatusUpdated"},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook request: %w", err)
	}

	resp, err := c.doRequest("POST", fmt.Sprintf("/team/%s/webhook", teamID), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to register webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var reg WebhookRegistration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("failed to decode webhook response: %w", err)
	}
	return &reg, nil
}

// DeleteWebhook removes a registered webhook
func (c *Client) DeleteWebhook(webhookID string) error {
	resp, err := c.doRequest("DELETE", fmt.Sprintf("/webhook/%s", webhookID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// doRequest makes an authenticated HTTP request to the ClickUp API
func (c *Client) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	url := baseURL + path

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ClickUp API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}
