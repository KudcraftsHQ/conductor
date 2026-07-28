package clickup

import (
	"context"
	"log"
	"strings"
	"time"
)

// Poller periodically checks ClickUp for task status changes
type Poller struct {
	client        *Client
	listIDs       []string
	triggerStatus string
	interval      time.Duration
	eventCh       chan<- TaskEvent
	lastSeen      map[string]string // taskID -> last known status
}

// NewPoller creates a new ClickUp task poller
func NewPoller(client *Client, listIDs []string, triggerStatus string, interval time.Duration, eventCh chan<- TaskEvent) *Poller {
	return &Poller{
		client:        client,
		listIDs:       listIDs,
		triggerStatus: triggerStatus,
		interval:      interval,
		eventCh:       eventCh,
		lastSeen:      make(map[string]string),
	}
}

// Start begins polling for task status changes
func (p *Poller) Start(ctx context.Context) {
	// Do an initial poll to seed lastSeen state without emitting events
	p.seedState()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

// UpdateListIDs updates the list of ClickUp lists to poll
func (p *Poller) UpdateListIDs(listIDs []string) {
	p.listIDs = listIDs
}

// seedState does an initial poll to populate lastSeen without emitting events
func (p *Poller) seedState() {
	for _, listID := range p.listIDs {
		tasks, err := p.client.GetFilteredTasks(listID, []string{p.triggerStatus})
		if err != nil {
			log.Printf("poller seed error for list %s: %v", listID, err)
			continue
		}
		for _, task := range tasks {
			p.lastSeen[task.ID] = strings.ToLower(task.Status.Status)
		}
	}
}

// poll checks all configured lists for tasks matching the trigger status
func (p *Poller) poll() {
	for _, listID := range p.listIDs {
		tasks, err := p.client.GetFilteredTasks(listID, []string{p.triggerStatus})
		if err != nil {
			log.Printf("poller error for list %s: %v", listID, err)
			continue
		}

		for _, task := range tasks {
			currentStatus := strings.ToLower(task.Status.Status)
			lastStatus, seen := p.lastSeen[task.ID]

			// Emit event if status changed to trigger status (or first time seeing it in trigger status)
			if !seen || lastStatus != currentStatus {
				if currentStatus == strings.ToLower(p.triggerStatus) {
					taskCopy := task
					event := TaskEvent{
						TaskID:    task.ID,
						Task:      &taskCopy,
						NewStatus: currentStatus,
						OldStatus: lastStatus,
						Timestamp: time.Now(),
					}
					select {
					case p.eventCh <- event:
					default:
						log.Printf("event channel full, dropping polled event for task %s", task.ID)
					}
				}
			}

			p.lastSeen[task.ID] = currentStatus
		}
	}
}

// GetLastSeen returns the deduplication state for persistence
func (p *Poller) GetLastSeen() map[string]string {
	result := make(map[string]string, len(p.lastSeen))
	for k, v := range p.lastSeen {
		result[k] = v
	}
	return result
}

// SetLastSeen restores deduplication state from persistence
func (p *Poller) SetLastSeen(state map[string]string) {
	p.lastSeen = state
}
