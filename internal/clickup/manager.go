package clickup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/tunnel"
)

// Manager orchestrates webhook vs polling and event fan-out
type Manager struct {
	client        *Client
	cfg           *config.ClickUpConfig
	eventCh       chan TaskEvent
	webhookServer *WebhookServer
	poller        *Poller
	tunnelMgr     *tunnel.Manager
	tunnelState   *config.TunnelState

	mu        sync.Mutex
	processed map[string]string // taskID -> last processed status (deduplication)

	// onEvent is called when a deduplicated task event is received
	onEvent func(TaskEvent)

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager creates a new ClickUp event manager
func NewManager(clickupCfg *config.ClickUpConfig, tunnelMgr *tunnel.Manager) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		client:    NewClient(clickupCfg.APIToken),
		cfg:       clickupCfg,
		eventCh:   make(chan TaskEvent, 100),
		tunnelMgr: tunnelMgr,
		processed: make(map[string]string),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// SetEventHandler sets the callback for deduplicated task events
func (m *Manager) SetEventHandler(handler func(TaskEvent)) {
	m.onEvent = handler
}

// Start starts the ClickUp event listener (webhook + optional polling)
func (m *Manager) Start(listIDs []string) error {
	// Load persisted dedup state
	m.loadState()

	triggerStatus := m.cfg.TriggerStatus
	if triggerStatus == "" {
		triggerStatus = "in progress"
	}

	webhookPort := m.cfg.WebhookPort
	if webhookPort == 0 {
		webhookPort = 9876
	}

	// Try to start webhook mode with tunnel
	webhookStarted := false
	if m.tunnelMgr != nil && m.tunnelMgr.IsCloudflaredInstalled() {
		if err := m.startWebhookMode(webhookPort); err != nil {
			log.Printf("webhook mode failed, falling back to polling: %v", err)
		} else {
			webhookStarted = true
			log.Printf("webhook mode active on port %d", webhookPort)
		}
	}

	// Start polling (always runs as heartbeat, or primary if webhook failed)
	pollInterval := time.Duration(m.cfg.PollInterval) * time.Second
	if pollInterval == 0 {
		pollInterval = 30 * time.Second
	}

	// If webhook is active, use polling as heartbeat (5 min interval)
	if webhookStarted {
		pollInterval = 5 * time.Minute
	}

	m.poller = NewPoller(m.client, listIDs, triggerStatus, pollInterval, m.eventCh)

	// Restore poller state from persisted dedup map
	m.mu.Lock()
	m.poller.SetLastSeen(m.processed)
	m.mu.Unlock()

	go m.poller.Start(m.ctx)

	// Start event processing loop
	go m.processEvents()

	return nil
}

// Stop gracefully shuts down the manager
func (m *Manager) Stop() error {
	m.cancel()

	// Deregister webhook if we registered one
	if m.cfg.WebhookID != "" {
		if err := m.client.DeleteWebhook(m.cfg.WebhookID); err != nil {
			log.Printf("failed to deregister webhook: %v", err)
		} else {
			m.cfg.WebhookID = ""
			m.cfg.WebhookSecret = ""
		}
	}

	// Stop webhook server
	if m.webhookServer != nil {
		m.webhookServer.Stop()
	}

	// Stop tunnel
	if m.tunnelState != nil {
		_ = m.tunnelMgr.StopTunnel("conductor-agent", "webhook")
	}

	// Save dedup state
	m.saveState()

	return nil
}

// Mode returns "webhook", "polling", or "inactive"
func (m *Manager) Mode() string {
	if m.webhookServer != nil {
		return "webhook"
	}
	if m.poller != nil {
		return "polling"
	}
	return "inactive"
}

// Client returns the ClickUp API client
func (m *Manager) Client() *Client {
	return m.client
}

// startWebhookMode sets up the webhook server with a Cloudflare tunnel
func (m *Manager) startWebhookMode(port int) error {
	// Start local HTTP server
	m.webhookServer = NewWebhookServer(port, m.cfg.WebhookSecret, m.eventCh)
	if err := m.webhookServer.Start(m.ctx); err != nil {
		return fmt.Errorf("failed to start webhook server: %w", err)
	}

	// Start tunnel to get a public URL
	tunnelState, err := m.tunnelMgr.StartQuickTunnel("conductor-agent", "webhook", port)
	if err != nil {
		m.webhookServer.Stop()
		m.webhookServer = nil
		return fmt.Errorf("failed to start tunnel: %w", err)
	}
	m.tunnelState = tunnelState

	// Register webhook with ClickUp
	webhookURL := tunnelState.URL + "/clickup-webhook"
	reg, err := m.client.RegisterWebhook(m.cfg.TeamID, webhookURL)
	if err != nil {
		m.webhookServer.Stop()
		m.webhookServer = nil
		_ = m.tunnelMgr.StopTunnel("conductor-agent", "webhook")
		return fmt.Errorf("failed to register webhook: %w", err)
	}

	m.cfg.WebhookID = reg.ID
	m.cfg.WebhookSecret = reg.Secret

	return nil
}

// processEvents reads from eventCh, deduplicates, and dispatches
func (m *Manager) processEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case event := <-m.eventCh:
			// Deduplicate
			m.mu.Lock()
			lastStatus, seen := m.processed[event.TaskID]
			if seen && lastStatus == strings.ToLower(event.NewStatus) {
				m.mu.Unlock()
				continue
			}
			m.processed[event.TaskID] = strings.ToLower(event.NewStatus)
			m.mu.Unlock()

			// Enrich event with full task data if not present
			if event.Task == nil {
				task, err := m.client.GetTask(event.TaskID)
				if err != nil {
					log.Printf("failed to fetch task %s: %v", event.TaskID, err)
					continue
				}
				event.Task = task
			}

			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now()
			}

			// Dispatch to handler
			if m.onEvent != nil {
				m.onEvent(event)
			}
		}
	}
}

// agentState represents persisted dedup state
type agentState struct {
	Processed map[string]string `json:"processed"`
	SavedAt   time.Time         `json:"savedAt"`
}

func (m *Manager) stateFilePath() string {
	dir, err := config.ConductorDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent-state.json")
}

func (m *Manager) saveState() {
	path := m.stateFilePath()
	if path == "" {
		return
	}

	m.mu.Lock()
	state := agentState{
		Processed: m.processed,
		SavedAt:   time.Now(),
	}
	m.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func (m *Manager) loadState() {
	path := m.stateFilePath()
	if path == "" {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var state agentState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}

	m.mu.Lock()
	m.processed = state.Processed
	if m.processed == nil {
		m.processed = make(map[string]string)
	}
	m.mu.Unlock()
}
