package clickup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
)

// WebhookServer listens for ClickUp webhook events
type WebhookServer struct {
	port     int
	secret   string
	eventCh  chan<- TaskEvent
	server   *http.Server
	listener net.Listener
}

// NewWebhookServer creates a new webhook HTTP server
func NewWebhookServer(port int, secret string, eventCh chan<- TaskEvent) *WebhookServer {
	return &WebhookServer{
		port:    port,
		secret:  secret,
		eventCh: eventCh,
	}
}

// Start starts the webhook HTTP server
func (ws *WebhookServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/clickup-webhook", ws.handleWebhook)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	ws.server = &http.Server{
		Handler: mux,
	}

	var err error
	ws.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", ws.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", ws.port, err)
	}

	go func() {
		if err := ws.server.Serve(ws.listener); err != nil && err != http.ErrServerClosed {
			log.Printf("webhook server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		ws.Stop()
	}()

	return nil
}

// Stop shuts down the webhook server
func (ws *WebhookServer) Stop() {
	if ws.server != nil {
		_ = ws.server.Close()
	}
}

// Port returns the actual listening port
func (ws *WebhookServer) Port() int {
	if ws.listener != nil {
		return ws.listener.Addr().(*net.TCPAddr).Port
	}
	return ws.port
}

// handleWebhook processes incoming ClickUp webhook events
func (ws *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	// Process status change events
	if payload.Event == "taskStatusUpdated" {
		for _, item := range payload.HistoryItems {
			if item.Field == "status" {
				event := TaskEvent{
					TaskID:    payload.TaskID,
					NewStatus: item.After.Status,
					OldStatus: item.Before.Status,
				}

				select {
				case ws.eventCh <- event:
				default:
					log.Printf("event channel full, dropping event for task %s", payload.TaskID)
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
