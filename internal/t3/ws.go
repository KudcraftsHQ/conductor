package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// The WebSocket carries Effect's RPC protocol with JSON serialization. A call
// is one Request frame; the reply is a stream of Chunk frames terminated by an
// Exit frame carrying a Success or Failure exit.
//
// Frames are sent as JSON arrays of messages, which is what the server's
// decoder expects even for a single message.

// WS method names. These are the subset conductor drives.
const (
	MethodTerminalOpen      = "terminal.open"
	MethodTerminalWrite     = "terminal.write"
	MethodTerminalClose     = "terminal.close"
	MethodTerminalRestart   = "terminal.restart"
	MethodVcsCreateWorktree = "vcs.createWorktree"
	MethodVcsRemoveWorktree = "vcs.removeWorktree"
)

// wsRequest is Effect's RequestEncoded envelope.
type wsRequest struct {
	Tag     string      `json:"_tag"`
	ID      int         `json:"id"`
	Method  string      `json:"tag"`
	Payload any         `json:"payload"`
	Headers [][2]string `json:"headers"`
}

// wsResponse covers the reply frames conductor cares about. Ping frames are
// answered with Pong; everything else is matched by requestId.
type wsResponse struct {
	Tag       string          `json:"_tag"`
	RequestID json.RawMessage `json:"requestId"`
	Exit      *wsExit         `json:"exit"`
	Values    json.RawMessage `json:"values"`
}

type wsExit struct {
	Tag   string          `json:"_tag"`
	Value json.RawMessage `json:"value"`
	Cause json.RawMessage `json:"cause"`
}

// Conn is an open RPC connection to the T3 server.
//
// It is deliberately single-shot and synchronous: conductor issues a handful of
// calls per worktree operation, so a connection per operation is simpler than a
// pooled multiplexed client, and it cannot leak a background reader.
type Conn struct {
	ws     *websocket.Conn
	nextID int
}

// Dial opens an authenticated RPC connection. Close it when done.
func (c *Client) Dial(ctx context.Context) (*Conn, error) {
	ticket, err := c.websocketTicket(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := strings.Replace(c.Origin, "http", "ws", 1) +
		"/ws?wsTicket=" + url.QueryEscape(ticket)

	ws, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open T3 websocket: %w", err)
	}
	// Terminal history can be large; the default 32KiB read limit truncates it.
	ws.SetReadLimit(32 << 20)

	return &Conn{ws: ws, nextID: 1}, nil
}

// Close shuts the connection down.
func (c *Conn) Close() error {
	return c.ws.Close(websocket.StatusNormalClosure, "")
}

// Call issues one RPC and waits for its Exit frame. When out is non-nil the
// success value is decoded into it.
func (c *Conn) Call(ctx context.Context, method string, payload, out any) error {
	id := c.nextID
	c.nextID++

	frame := []wsRequest{{
		Tag:     "Request",
		ID:      id,
		Method:  method,
		Payload: payload,
		Headers: [][2]string{},
	}}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("failed to encode %s request: %w", method, err)
	}
	if err := c.ws.Write(ctx, websocket.MessageText, encoded); err != nil {
		return fmt.Errorf("failed to send %s: %w", method, err)
	}

	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("T3 websocket closed while awaiting %s: %w", method, err)
		}

		messages, err := decodeFrame(data)
		if err != nil {
			return err
		}

		for _, msg := range messages {
			if msg.Tag == "Ping" {
				_ = c.writeRaw(ctx, []byte(`[{"_tag":"Pong"}]`))
				continue
			}
			if !matchesID(msg.RequestID, id) {
				// A frame for another request, or a server-initiated event.
				// Single-shot connections make this rare, but streamed
				// subscriptions on the same socket would land here.
				continue
			}
			if msg.Tag != "Exit" || msg.Exit == nil {
				continue
			}
			if msg.Exit.Tag != "Success" {
				return fmt.Errorf("T3 %s failed: %s", method, truncate(string(msg.Exit.Cause), 400))
			}
			if out == nil || len(msg.Exit.Value) == 0 {
				return nil
			}
			if err := json.Unmarshal(msg.Exit.Value, out); err != nil {
				return fmt.Errorf("failed to decode %s result: %w", method, err)
			}
			return nil
		}
	}
}

func (c *Conn) writeRaw(ctx context.Context, data []byte) error {
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// decodeFrame parses a frame that may be a single message or an array of them.
func decodeFrame(data []byte) ([]wsResponse, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var messages []wsResponse
		if err := json.Unmarshal([]byte(trimmed), &messages); err != nil {
			return nil, fmt.Errorf("failed to decode T3 frame: %w", err)
		}
		return messages, nil
	}
	var single wsResponse
	if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
		return nil, fmt.Errorf("failed to decode T3 frame: %w", err)
	}
	return []wsResponse{single}, nil
}

// matchesID compares a response's requestId against a request id. The server
// may encode it as a number or a string, so both are accepted.
func matchesID(raw json.RawMessage, id int) bool {
	if len(raw) == 0 {
		return false
	}
	var asNumber int
	if json.Unmarshal(raw, &asNumber) == nil {
		return asNumber == id
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString == fmt.Sprint(id)
	}
	return false
}

// --- terminal operations ---

// TerminalSnapshot describes a pty owned by a thread.
type TerminalSnapshot struct {
	ThreadID     string  `json:"threadId"`
	TerminalID   string  `json:"terminalId"`
	CWD          string  `json:"cwd"`
	WorktreePath *string `json:"worktreePath"`
	Status       string  `json:"status"`
	PID          *int    `json:"pid"`
	History      string  `json:"history"`
	ExitCode     *int    `json:"exitCode"`
	Label        string  `json:"label"`
}

// TerminalOpen spawns a pty owned by threadID. The terminal's lifecycle is the
// thread's: archiving the thread reaps the process, so conductor does not have
// to track it separately.
func (c *Conn) TerminalOpen(ctx context.Context, threadID, terminalID, cwd, worktreePath string) (*TerminalSnapshot, error) {
	payload := map[string]any{
		"threadId":   threadID,
		"terminalId": terminalID,
		"cwd":        cwd,
		"cols":       200,
		"rows":       50,
	}
	if worktreePath != "" {
		payload["worktreePath"] = worktreePath
	}
	var snapshot TerminalSnapshot
	if err := c.Call(ctx, MethodTerminalOpen, payload, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// TerminalWrite sends raw input to a terminal. Callers append "\n" themselves
// to submit a command; this writes bytes, it does not press enter for you.
func (c *Conn) TerminalWrite(ctx context.Context, threadID, terminalID, data string) error {
	return c.Call(ctx, MethodTerminalWrite, map[string]any{
		"threadId":   threadID,
		"terminalId": terminalID,
		"data":       data,
	}, nil)
}

// TerminalRunCommand opens a terminal and submits a command line to it.
func (c *Conn) TerminalRunCommand(ctx context.Context, threadID, terminalID, cwd, worktreePath, command string) (*TerminalSnapshot, error) {
	snapshot, err := c.TerminalOpen(ctx, threadID, terminalID, cwd, worktreePath)
	if err != nil {
		return nil, err
	}
	// The shell needs a moment to draw its prompt; writing immediately can land
	// before the pty is ready to echo it.
	time.Sleep(300 * time.Millisecond)
	if err := c.TerminalWrite(ctx, threadID, terminalID, command+"\n"); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// TerminalClose terminates a terminal.
func (c *Conn) TerminalClose(ctx context.Context, threadID, terminalID string) error {
	return c.Call(ctx, MethodTerminalClose, map[string]any{
		"threadId":   threadID,
		"terminalId": terminalID,
	}, nil)
}
