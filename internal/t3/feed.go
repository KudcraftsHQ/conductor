package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/coder/websocket"
)

// ErrShellWatchUnsupported is returned by WatchShell against a V1 server,
// which has no shell subscription conductor can use.
var ErrShellWatchUnsupported = errors.New("T3 shell subscription needs orchestration protocol 2")

// ShellWatch is a live V2 shell subscription used as a change signal.
//
// It subscribes to both orchestration.subscribeShell and
// orchestration.subscribeArchivedShell, and fires Changed whenever either
// stream delivers anything. It deliberately does *not* rebuild the thread list
// from the stream: a thread moving between the active and archived streams
// arrives as a removal on one and an update on the other, and between the two
// it is in neither — which to the watcher is a deletion, and a deletion tears
// the worktree down on the spot. So a change only prompts a fresh
// Client.Snapshot, whose three-read union cannot lose a moving thread.
type ShellWatch struct {
	conn    *Conn
	cancel  context.CancelFunc
	changed chan struct{}
	done    chan struct{}

	mu  sync.Mutex
	err error
}

// WatchShell opens a shell subscription. It needs a V2 server; against V1 it
// returns ErrShellWatchUnsupported and the caller keeps polling.
func (c *Client) WatchShell(ctx context.Context) (*ShellWatch, error) {
	p, err := c.Protocol(ctx)
	if err != nil {
		return nil, err
	}
	if p < ProtocolV2 {
		return nil, ErrShellWatchUnsupported
	}
	conn, err := c.dial(ctx, p)
	if err != nil {
		return nil, err
	}

	watchCtx, cancel := context.WithCancel(context.Background())
	w := &ShellWatch{
		conn:    conn,
		cancel:  cancel,
		changed: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

	ids := map[int]bool{}
	for _, method := range []string{MethodV2SubscribeShell, MethodV2SubscribeArchivedShell} {
		id, err := conn.send(ctx, method, map[string]any{})
		if err != nil {
			cancel()
			_ = conn.Close()
			return nil, err
		}
		ids[id] = true
	}

	go w.run(watchCtx, ids)
	return w, nil
}

// Changed fires (coalesced) when the server reports any shell change.
func (w *ShellWatch) Changed() <-chan struct{} { return w.changed }

// Done is closed when the subscription ends, for whatever reason.
func (w *ShellWatch) Done() <-chan struct{} { return w.done }

// Err reports why the subscription ended, once Done is closed.
func (w *ShellWatch) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close ends the subscription and waits for its reader to stop.
func (w *ShellWatch) Close() {
	w.cancel()
	_ = w.conn.ws.Close(websocket.StatusNormalClosure, "")
	<-w.done
}

func (w *ShellWatch) fail(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

func (w *ShellWatch) signal() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// run reads frames until the socket or a stream ends.
//
// Every chunk is acknowledged. Effect's RPC server applies backpressure per
// stream: after each Chunk it waits for the client's Ack before sending the
// next, so a reader that never acks sees exactly one frame and then silence.
func (w *ShellWatch) run(ctx context.Context, ids map[int]bool) {
	defer close(w.done)
	for {
		_, data, err := w.conn.ws.Read(ctx)
		if err != nil {
			w.fail(fmt.Errorf("T3 shell subscription closed: %w", err))
			return
		}
		messages, err := decodeFrame(data)
		if err != nil {
			w.fail(err)
			return
		}
		for _, msg := range messages {
			if msg.Tag == "Ping" {
				_ = w.conn.writeRaw(ctx, []byte(`[{"_tag":"Pong"}]`))
				continue
			}
			id, ok := requestIDOf(msg.RequestID)
			if !ok || !ids[id] {
				continue
			}
			switch msg.Tag {
			case "Chunk":
				if err := w.conn.ack(ctx, id); err != nil {
					w.fail(err)
					return
				}
				w.signal()
			case "Exit":
				if msg.Exit != nil && msg.Exit.Tag != "Success" {
					w.fail(fmt.Errorf("T3 shell subscription failed: %s", truncate(string(msg.Exit.Cause), 400)))
				} else {
					w.fail(errors.New("T3 shell subscription ended"))
				}
				return
			}
		}
	}
}

// send writes one Request frame and returns its id without waiting.
func (c *Conn) send(ctx context.Context, method string, payload any) (int, error) {
	id := c.nextID
	c.nextID++
	encoded, err := json.Marshal([]wsRequest{{
		Tag:     "Request",
		ID:      id,
		Method:  method,
		Payload: payload,
		Headers: [][2]string{},
	}})
	if err != nil {
		return 0, fmt.Errorf("failed to encode %s request: %w", method, err)
	}
	if err := c.ws.Write(ctx, websocket.MessageText, encoded); err != nil {
		return 0, fmt.Errorf("failed to send %s: %w", method, err)
	}
	return id, nil
}

// ack acknowledges one chunk of a stream. AckEncoded carries the request id
// as a string.
func (c *Conn) ack(ctx context.Context, id int) error {
	frame := fmt.Sprintf(`[{"_tag":"Ack","requestId":"%d"}]`, id)
	return c.writeRaw(ctx, []byte(frame))
}

// requestIDOf reads a requestId encoded as a number or a numeric string.
func requestIDOf(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if _, err := fmt.Sscan(s, &n); err == nil {
			return n, true
		}
	}
	return 0, false
}
