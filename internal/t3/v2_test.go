package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixtures ---

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "v2", name))
	require.NoError(t, err)
	return data
}

func decodeFixture[T any](t *testing.T, name string) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(fixture(t, name), &out))
	return out
}

// --- pure conversion ---

func TestDescriptorProtocol(t *testing.T) {
	v2 := decodeFixture[EnvironmentDescriptor](t, "descriptor.json")
	assert.Equal(t, ProtocolV2, v2.Protocol())
	assert.True(t, v2.Capabilities.ServerResolvedCommandContext)

	v1 := decodeFixture[EnvironmentDescriptor](t, "descriptor_v1.json")
	assert.Equal(t, ProtocolV1, v1.Protocol())

	// Absent on hosts from before negotiation existed: those are V1.
	var legacy EnvironmentDescriptor
	require.NoError(t, json.Unmarshal([]byte(`{"serverVersion":"0.0.30","capabilities":{}}`), &legacy))
	assert.Equal(t, ProtocolV1, legacy.Protocol())
}

func TestV2ShellConversion(t *testing.T) {
	raw := decodeFixture[v2ShellSnapshot](t, "shell.json")
	snapshot := raw.toShellSnapshot()

	require.Len(t, snapshot.Projects, 1)
	assert.Equal(t, "/home/u/Projects/kudtrading", snapshot.Projects[0].WorkspaceRoot)
	require.Len(t, snapshot.Threads, 5)
	assert.Equal(t, 4812, snapshot.SnapshotSequence)

	byID := map[string]Thread{}
	for _, th := range snapshot.Threads {
		byID[th.ID] = th
	}

	running := byID["thr-running"]
	assert.Equal(t, "/home/u/.t3/worktrees/kudtrading/feat-a", running.Worktree())
	assert.Equal(t, "feat-a", *running.Branch)
	assert.False(t, running.Archived())
	assert.True(t, running.Started())
	assert.False(t, running.Settled(), "a running thread is never settled")
	assert.Equal(t, "claudeAgent/claude-opus-5?effort=medium", running.ModelSelection.String())
	assert.Equal(t, "run-a2", running.RunID)

	settled := byID["thr-settled"]
	assert.True(t, settled.Settled(), "settledAt carries over from V2")
	assert.True(t, settled.Done())

	// settledOverride "settled" is overridden by a pending user-input request:
	// a thread waiting on a human keeps its dev server.
	asking := byID["thr-asking"]
	assert.True(t, asking.HasPendingUserInput)
	assert.False(t, asking.HasPendingApprovals)
	assert.False(t, asking.Settled())

	failed := byID["thr-failed"]
	reason, isFailed := failed.Failed()
	assert.True(t, isFailed)
	assert.Contains(t, reason, "unknown provider instance")
	assert.False(t, failed.Started())

	// A run still preparing (setup script, checkout) has not started: the
	// provider has not been asked to do anything yet.
	preparing := byID["thr-preparing"]
	assert.False(t, preparing.Started())
	assert.Equal(t, "starting", preparing.Session.Status)
	assert.False(t, preparing.Settled())
	_, isFailed = preparing.Failed()
	assert.False(t, isFailed)
}

func TestV2PendingApprovalBlocksSettling(t *testing.T) {
	var th v2Thread
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"t","status":"completed","settledOverride":"settled","settledAt":"2026-10-03T00:00:00.000Z",
		"pendingRuntimeRequest":{"id":"rr","kind":"command","createdAt":"2026-10-03T00:00:00.000Z"}}`), &th))
	thread := th.toThread()
	assert.True(t, thread.HasPendingApprovals)
	assert.False(t, thread.Settled())
}

func TestV2ArchivedThreadsAreArchived(t *testing.T) {
	archived := decodeFixture[v2ArchivedShellSnapshot](t, "archived.json")
	active := decodeFixture[v2ShellSnapshot](t, "shell.json")
	merged := mergeV2Snapshots(&archived, &active, &archived)

	require.Len(t, merged.Threads, 6)
	last := merged.Threads[len(merged.Threads)-1]
	assert.Equal(t, "thr-archived", last.ID)
	assert.True(t, last.Archived())

	// Reconciliation and the TUI read the merged snapshot exactly as they read
	// V1's /snapshot: the archived worktree holds, the live ones are counted.
	assert.Equal(t, []string{"thr-archived"}, merged.ThreadIDsByWorktree("/home/u/.t3/worktrees/kudtrading/old"))
	_, live := merged.FindThreadByWorktree("/home/u/.t3/worktrees/kudtrading/old")
	assert.False(t, live)
}

// A thread moving between the archive and the active list between reads must
// never vanish from the merged snapshot: absence means deletion, and the
// watcher tears a worktree down on deletion.
func TestMergeV2SnapshotsNeverLosesAMovingThread(t *testing.T) {
	mk := func(id string, archived bool) v2Thread {
		th := v2Thread{ID: id, Status: "idle", WorktreePath: ptr("/w/" + id)}
		if archived {
			th.ArchivedAt = ptr("2026-10-03T00:00:00.000Z")
		}
		return th
	}
	empty := &v2ArchivedShellSnapshot{}
	none := &v2ShellSnapshot{}

	// Archived after the active read: only the second archive read has it.
	merged := mergeV2Snapshots(empty, none, &v2ArchivedShellSnapshot{Threads: []v2Thread{mk("a", true)}})
	require.Len(t, merged.Threads, 1)
	assert.True(t, merged.Threads[0].Archived())

	// Archived before the active read but after the first archive read: the
	// active read misses it, the second archive read has it.
	merged = mergeV2Snapshots(empty, none, &v2ArchivedShellSnapshot{Threads: []v2Thread{mk("b", true)}})
	require.Len(t, merged.Threads, 1)

	// Unarchived after the first archive read: the active read has it, and
	// being later it wins over the stale archived copy.
	merged = mergeV2Snapshots(
		&v2ArchivedShellSnapshot{Threads: []v2Thread{mk("c", true)}},
		&v2ShellSnapshot{Threads: []v2Thread{mk("c", false)}},
		empty)
	require.Len(t, merged.Threads, 1)
	assert.False(t, merged.Threads[0].Archived())

	// Unarchived after the active read: only the first archive read has it.
	merged = mergeV2Snapshots(&v2ArchivedShellSnapshot{Threads: []v2Thread{mk("d", true)}}, none, empty)
	require.Len(t, merged.Threads, 1)
}

// The three reads are only an exact picture when the server did not move
// between them. Equal sequences say it did not; anything else is marked, so
// the watcher never tears down on it.
func TestMergeV2SnapshotsMarksAMovingServerInconsistent(t *testing.T) {
	at := func(seq int) *v2ArchivedShellSnapshot { return &v2ArchivedShellSnapshot{SnapshotSequence: seq} }
	active := &v2ShellSnapshot{SnapshotSequence: 7}

	assert.False(t, mergeV2Snapshots(at(7), active, at(7)).Inconsistent)
	assert.True(t, mergeV2Snapshots(at(6), active, at(7)).Inconsistent, "moved before the active read")
	assert.True(t, mergeV2Snapshots(at(7), active, at(8)).Inconsistent, "moved after the active read")
	assert.True(t, mergeV2Snapshots(nil, active, at(7)).Inconsistent, "a missing read is never consistent")
}

func TestBuildMessageDispatchV2(t *testing.T) {
	command := buildMessageDispatchV2("thr-1", "hello", true, nil)
	encoded, err := json.Marshal(command)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))

	// Required by the OrchestrationV2Command schema.
	assert.Equal(t, "message.dispatch", wire["type"])
	assert.Equal(t, "user", wire["createdBy"])
	assert.Equal(t, "web", wire["creationSource"])
	assert.Equal(t, "thr-1", wire["threadId"])
	assert.Equal(t, "hello", wire["text"])
	assert.NotEmpty(t, wire["commandId"])
	assert.NotEmpty(t, wire["messageId"])
	assert.Equal(t, []any{}, wire["attachments"])
	assert.Equal(t, map[string]any{"type": "start_immediately"}, wire["dispatchMode"])
	assert.Equal(t, "auto", wire["deliveryIntent"])
	assert.NotContains(t, wire, "modelSelection")

	// An older V2 server without serverResolvedCommandContext: queue behind an
	// active run rather than guessing a steering mode.
	active := "run-9"
	queued := buildMessageDispatchV2("thr-1", "hi", false, &active)
	assert.Equal(t, "queue_after_active", queued.DispatchMode.Type)
	assert.Empty(t, queued.DeliveryIntent)

	idle := buildMessageDispatchV2("thr-1", "hi", false, nil)
	assert.Equal(t, "start_immediately", idle.DispatchMode.Type)
}

// --- a fake T3 server, both protocols ---

type rpcCall struct {
	Method  string
	Payload json.RawMessage
}

type fakeT3 struct {
	t        *testing.T
	protocol int
	srv      *httptest.Server

	mu       sync.Mutex
	http     []string // "METHOD path [protocol-header]"
	bodies   map[string][]json.RawMessage
	calls    []rpcCall
	wsQuery  []string
	acks     []string
	shell    []byte
	archived []byte
	// streamItems, when set, are sent as successive chunks of a subscription;
	// each after the previous chunk has been acknowledged.
	streamItems [][]byte
}

func newFakeT3(t *testing.T, protocol int) *fakeT3 {
	f := &fakeT3{
		t:        t,
		protocol: protocol,
		bodies:   map[string][]json.RawMessage{},
		shell:    fixture(t, "shell.json"),
		archived: fixture(t, "archived.json"),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeT3) client() *Client {
	return &Client{Origin: f.srv.URL, Token: "tok", HTTP: f.srv.Client()}
}

func (f *fakeT3) setProtocol(p int) {
	f.mu.Lock()
	f.protocol = p
	f.mu.Unlock()
}

func (f *fakeT3) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := r.Method + " " + r.URL.Path
	if h := r.Header.Get(protocolHeader); h != "" {
		entry += " [" + h + "]"
	}
	f.http = append(f.http, entry)
	if r.Body != nil && r.Method == http.MethodPost {
		var body json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], body)
		}
	}
}

func (f *fakeT3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	p := f.protocol
	f.mu.Unlock()
	if r.URL.Path != "/ws" {
		f.record(r)
	}

	switch {
	case r.URL.Path == descriptorPath:
		if p == ProtocolV2 {
			_, _ = w.Write(fixture(f.t, "descriptor.json"))
		} else {
			_, _ = w.Write(fixture(f.t, "descriptor_v1.json"))
		}
	case r.URL.Path == "/api/auth/websocket-ticket":
		_, _ = w.Write([]byte(`{"ticket":"tkt"}`))
	case r.URL.Path == "/api/orchestration/shell":
		if p == ProtocolV2 {
			if r.Header.Get(protocolHeader) != "2" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"_tag":"HttpApiDecodeError","message":"Missing key x-t3-orchestration-protocol"}`))
				return
			}
			f.mu.Lock()
			body := f.shell
			f.mu.Unlock()
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write([]byte(`{"snapshotSequence":1,"projects":[],"threads":[{"id":"v1-live","worktreePath":"/w/one"}]}`))
	case r.URL.Path == "/api/orchestration/snapshot" && p == ProtocolV1:
		_, _ = w.Write([]byte(`{"snapshotSequence":1,"projects":[],"threads":[
			{"id":"v1-live","worktreePath":"/w/one"},
			{"id":"v1-arch","worktreePath":"/w/two","archivedAt":"2026-10-01T00:00:00.000Z"}]}`))
	case r.URL.Path == "/api/orchestration/dispatch" && p == ProtocolV1:
		_, _ = w.Write([]byte(`{"sequence":7}`))
	case r.URL.Path == v2ProjectsMutatePath && p == ProtocolV2:
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == "/ws":
		f.serveWS(w, r, p)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"_tag":"RouteNotFound"}`))
	}
}

func (f *fakeT3) serveWS(w http.ResponseWriter, r *http.Request, p int) {
	f.mu.Lock()
	f.wsQuery = append(f.wsQuery, r.URL.RawQuery)
	f.mu.Unlock()
	if p == ProtocolV2 && r.URL.Query().Get(protocolQueryParam) != "2" {
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = w.Write([]byte(`{"code":"orchestration_protocol_incompatible","orchestrationProtocolVersion":2}`))
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	ctx := r.Context()

	acked := make(chan struct{}, 16)
	write := func(v any) {
		data, _ := json.Marshal([]any{v})
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
	exit := func(id json.RawMessage, value any) {
		write(map[string]any{"_tag": "Exit", "requestId": id, "exit": map[string]any{"_tag": "Success", "value": value}})
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msgs []struct {
			Tag       string          `json:"_tag"`
			ID        json.RawMessage `json:"id"`
			RequestID string          `json:"requestId"`
			Method    string          `json:"tag"`
			Payload   json.RawMessage `json:"payload"`
		}
		require.NoError(f.t, json.Unmarshal(data, &msgs))
		for _, m := range msgs {
			if m.Tag == "Ack" {
				f.mu.Lock()
				f.acks = append(f.acks, m.RequestID)
				f.mu.Unlock()
				acked <- struct{}{}
				continue
			}
			if m.Tag != "Request" {
				continue
			}
			f.mu.Lock()
			f.calls = append(f.calls, rpcCall{Method: m.Method, Payload: m.Payload})
			items := f.streamItems
			f.mu.Unlock()

			switch m.Method {
			case MethodV2GetArchivedShellSnapshot:
				f.mu.Lock()
				archived := f.archived
				f.mu.Unlock()
				exit(m.ID, json.RawMessage(archived))
			case MethodV2DispatchCommand:
				exit(m.ID, map[string]any{"sequence": 4813})
			case MethodV2LaunchThread:
				var in map[string]any
				_ = json.Unmarshal(m.Payload, &in)
				exit(m.ID, map[string]any{"threadId": in["threadId"], "projection": map[string]any{}, "resumed": false})
			case MethodSubscribeServerConfig:
				write(map[string]any{"_tag": "Chunk", "requestId": m.ID, "values": []any{map[string]any{
					"type": "snapshot",
					"config": map[string]any{"providers": []any{map[string]any{
						"instanceId": "claudeAgent", "driver": "claudeAgent", "enabled": true, "installed": true,
						"status": "ready", "models": []any{map[string]any{"slug": "claude-opus-5"}},
					}}},
				}}})
			case MethodV2SubscribeShell, MethodV2SubscribeArchivedShell:
				if m.Method == MethodV2SubscribeArchivedShell {
					continue // Silent: the test drives the active stream.
				}
				id := m.ID
				go func() {
					for i, item := range items {
						if i > 0 {
							select {
							case <-acked:
							case <-time.After(2 * time.Second):
								return // No ack: backpressure holds the stream.
							}
						}
						write(map[string]any{"_tag": "Chunk", "requestId": id, "values": []json.RawMessage{item}})
					}
				}()
			case MethodTerminalOpen:
				exit(m.ID, map[string]any{"threadId": "t", "terminalId": "x", "cwd": "/w", "status": "running", "history": ""})
			case MethodTerminalWrite:
				exit(m.ID, nil)
			default:
				write(map[string]any{"_tag": "Exit", "requestId": m.ID, "exit": map[string]any{"_tag": "Failure", "cause": "unknown method " + m.Method}})
			}
		}
	}
}

func (f *fakeT3) callsTo(method string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []json.RawMessage
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c.Payload)
		}
	}
	return out
}

func (f *fakeT3) sawHTTP(entry string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.http {
		if e == entry {
			return true
		}
	}
	return false
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// --- V2 against the fake ---

func TestV2ShellAndSnapshot(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)

	p, err := c.Protocol(ctx)
	require.NoError(t, err)
	assert.Equal(t, ProtocolV2, p)

	shell, err := c.Shell(ctx)
	require.NoError(t, err)
	assert.Len(t, shell.Threads, 5)
	assert.True(t, f.sawHTTP("GET /api/orchestration/shell [2]"), "V2 shell needs the protocol header")

	snapshot, err := c.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Threads, 6)
	found, ok := snapshot.FindThreadByID("thr-archived")
	require.True(t, ok)
	assert.True(t, found.Archived())
	assert.Len(t, f.callsTo(MethodV2GetArchivedShellSnapshot), 2, "archive is read on both sides of the active read")
	assert.False(t, f.sawHTTP("GET /api/orchestration/snapshot"), "V2 has no /snapshot route")
	assert.Contains(t, f.wsQuery[0], "orchestrationProtocol=2")
}

func TestV2StartTurnDispatchesMessage(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	require.NoError(t, c.StartTurn(testCtx(t), "thr-running", "keep going", RuntimeModeFullAccess))

	calls := f.callsTo(MethodV2DispatchCommand)
	require.Len(t, calls, 1)
	var cmd map[string]any
	require.NoError(t, json.Unmarshal(calls[0], &cmd))
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "thr-running", cmd["threadId"])
	assert.Equal(t, "keep going", cmd["text"])
	assert.Equal(t, "auto", cmd["deliveryIntent"], "descriptor advertises serverResolvedCommandContext")
	assert.False(t, f.sawHTTP("POST /api/orchestration/dispatch"))

	// The run that was latest before the dispatch is the baseline, so
	// WaitForTurn does not take the previous run as this turn's verdict.
	baseline, ok := c.baseline("thr-running")
	require.True(t, ok)
	assert.Equal(t, "run-a2", baseline.runID)
	assert.True(t, baseline.active, "thr-running had an active run when the message was sent")
}

func TestV2WaitForTurnIgnoresThePreviousRun(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)

	// thr-failed's latest run failed. Dispatching a new turn records it as the
	// baseline; until a new run appears, its failure must not be reported.
	require.NoError(t, c.StartTurn(ctx, "thr-failed", "retry", ""))
	err := c.WaitForTurn(ctx, "thr-failed", 1500*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no turn started", "the old run's failure is not this turn's")

	// Once the shell shows a new run, its verdict counts.
	f.mu.Lock()
	f.shell = []byte(`{"schemaVersion":3,"snapshotSequence":1,"projects":[],"archivedThreads":[],"threads":[
		{"id":"thr-failed","status":"running","latestRunId":"run-d2","latestRunStartedAt":"2026-10-03T04:00:00.000Z","activeRunId":"run-d2","pendingRuntimeRequest":null,"archivedAt":null,"settledAt":null,"settledOverride":null,"deletedAt":null}]}`)
	f.mu.Unlock()
	require.NoError(t, c.StartTurn(ctx, "thr-failed", "retry", ""))
	f.mu.Lock()
	f.shell = []byte(`{"schemaVersion":3,"snapshotSequence":2,"projects":[],"archivedThreads":[],"threads":[
		{"id":"thr-failed","status":"running","latestRunId":"run-d3","latestRunStartedAt":"2026-10-03T04:01:00.000Z","activeRunId":"run-d3","pendingRuntimeRequest":null,"archivedAt":null,"settledAt":null,"settledOverride":null,"deletedAt":null}]}`)
	f.mu.Unlock()
	require.NoError(t, c.WaitForTurn(ctx, "thr-failed", 3*time.Second))
}

func TestV2CreateThreadLaunchesIntoExistingWorktree(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	threadID, err := c.CreateThread(testCtx(t), CreateThreadOptions{
		ProjectID:    "proj-repo",
		Title:        "kudtrading/feat-z",
		Branch:       "feat-z",
		WorktreePath: "/home/u/.t3/worktrees/kudtrading/feat-z",
		Model:        ModelSelection{InstanceID: "claudeAgent", Model: "claude-opus-5"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, threadID)

	launches := f.callsTo(MethodV2LaunchThread)
	require.Len(t, launches, 1)
	var in map[string]any
	require.NoError(t, json.Unmarshal(launches[0], &in))
	assert.Equal(t, threadID, in["threadId"])
	assert.Equal(t, "proj-repo", in["projectId"])
	assert.Equal(t, "full-access", in["runtimeMode"])
	assert.Equal(t, "default", in["interactionMode"])
	assert.Equal(t, map[string]any{
		"type":         "existing_worktree",
		"worktreePath": "/home/u/.t3/worktrees/kudtrading/feat-z",
		"branch":       "feat-z",
	}, in["workspaceStrategy"])
	assert.NotContains(t, in, "initialMessage", "the first turn is held, not folded into the launch")
	assert.Empty(t, f.callsTo(MethodV2DispatchCommand), "no prompt, no turn")
}

func TestV2ArchiveDeleteAndProjects(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)

	require.NoError(t, c.ArchiveThread(ctx, "thr-settled"))
	require.NoError(t, c.DeleteThread(ctx, "thr-settled"))
	calls := f.callsTo(MethodV2DispatchCommand)
	require.Len(t, calls, 2)
	assert.JSONEq(t, fmt.Sprintf(`{"type":"thread.archive","commandId":%q,"threadId":"thr-settled"}`, commandID(t, calls[0])), string(calls[0]))
	assert.Contains(t, string(calls[1]), `"type":"thread.delete"`)

	// Existing project by root: no mutation.
	id, err := c.EnsureProject(ctx, "kudtrading", "/home/u/Projects/kudtrading")
	require.NoError(t, err)
	assert.Equal(t, "proj-repo", id)

	// A new root goes through /api/projects/mutate, not the removed dispatch.
	id, err = c.EnsureProject(ctx, "other", "/home/u/Projects/other")
	require.NoError(t, err)
	f.mu.Lock()
	bodies := f.bodies[v2ProjectsMutatePath]
	f.mu.Unlock()
	require.Len(t, bodies, 1)
	var mutation map[string]any
	require.NoError(t, json.Unmarshal(bodies[0], &mutation))
	assert.Equal(t, "project.create", mutation["type"])
	assert.Equal(t, id, mutation["projectId"])
	assert.Equal(t, "/home/u/Projects/other", mutation["workspaceRoot"])

	models := c.ProjectDefaultModels(ctx, "proj-repo")
	require.Len(t, models, 1)
	assert.Equal(t, "claudeAgent/claude-opus-5?effort=medium", models[0].String())
}

func commandID(t *testing.T, raw json.RawMessage) string {
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m["commandId"].(string)
}

func TestV2TerminalsUseTheProtocolQuery(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)
	conn, err := c.Dial(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.TerminalOpen(ctx, "thr-running", "conductor-dev-logs", "/w", "/w")
	require.NoError(t, err)
	require.NoError(t, conn.TerminalWrite(ctx, "thr-running", "conductor-dev-logs", "ls\n"))
	assert.Contains(t, f.wsQuery[0], "orchestrationProtocol=2")

	providers, err := c.ProviderInstances(ctx)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	assert.Equal(t, "claudeAgent", providers[0].InstanceID)
}

// --- V1 stays V1 ---

func TestV1PathsUnchanged(t *testing.T) {
	f := newFakeT3(t, ProtocolV1)
	c := f.client()
	ctx := testCtx(t)

	snapshot, err := c.Snapshot(ctx)
	require.NoError(t, err)
	assert.Len(t, snapshot.Threads, 2)
	assert.True(t, f.sawHTTP("GET /api/orchestration/snapshot"))

	require.NoError(t, c.ArchiveThread(ctx, "v1-live"))
	require.NoError(t, c.StartTurn(ctx, "v1-live", "hi", ""))
	f.mu.Lock()
	bodies := f.bodies["/api/orchestration/dispatch"]
	f.mu.Unlock()
	require.Len(t, bodies, 2)
	assert.Contains(t, string(bodies[0]), `"type":"thread.archive"`)
	assert.Contains(t, string(bodies[1]), `"type":"thread.turn.start"`)
	assert.Contains(t, string(bodies[1]), `"runtimeMode":"full-access"`)

	conn, err := c.Dial(ctx)
	require.NoError(t, err)
	_ = conn.Close()
	assert.NotContains(t, f.wsQuery[0], protocolQueryParam, "V1 socket carries no protocol query")

	_, err = c.WatchShell(ctx)
	assert.ErrorIs(t, err, ErrShellWatchUnsupported)
}

// A nightly upgrade under a long-running client: the cached V1 answer meets a
// V2 server, the V1 route 404s, and the client re-detects and carries on.
func TestProtocolRedetectedAfterUpgrade(t *testing.T) {
	f := newFakeT3(t, ProtocolV1)
	c := f.client()
	ctx := testCtx(t)

	_, err := c.Snapshot(ctx)
	require.NoError(t, err)
	p, _ := c.Protocol(ctx)
	require.Equal(t, ProtocolV1, p)

	f.setProtocol(ProtocolV2)
	snapshot, err := c.Snapshot(ctx)
	require.NoError(t, err, "a 404 on /snapshot re-detects rather than failing")
	assert.Len(t, snapshot.Threads, 6)
	p, _ = c.Protocol(ctx)
	assert.Equal(t, ProtocolV2, p)

	// And the socket: a stale V1 answer gets 426, re-detects, retries.
	c.SetProtocol(ProtocolV1)
	conn, err := c.Dial(ctx)
	require.NoError(t, err)
	assert.Equal(t, ProtocolV2, conn.Protocol)
	_ = conn.Close()

	// And back down.
	f.setProtocol(ProtocolV1)
	_, err = c.Snapshot(ctx)
	require.NoError(t, err)
}

// --- shell subscription ---

func TestWatchShellAcksAndSignals(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	f.streamItems = [][]byte{
		[]byte(`{"kind":"snapshot","snapshot":` + string(fixture(t, "shell.json")) + `}`),
		[]byte(`{"kind":"thread.removed","sequence":4813,"location":"active","threadId":"thr-settled"}`),
		[]byte(`{"kind":"thread.updated","sequence":4814,"location":"active","thread":{"id":"thr-running"}}`),
	}
	c := f.client()
	w, err := c.WatchShell(testCtx(t))
	require.NoError(t, err)
	defer w.Close()

	// Three chunks only arrive if each was acknowledged: the fake withholds
	// the next one otherwise, as Effect's RPC server does. Signals coalesce, so
	// the count to check is the acks, and Changed only has to have fired.
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.acks) == 3
	}, 5*time.Second, 10*time.Millisecond)
	select {
	case <-w.Changed():
	case <-time.After(time.Second):
		t.Fatal("no change signal")
	}
	select {
	case <-w.Done():
		t.Fatalf("subscription ended early: %v", w.Err())
	default:
	}
	f.mu.Lock()
	assert.Equal(t, "1", f.acks[0])
	f.mu.Unlock()

	methods := []string{}
	for _, call := range f.calls {
		methods = append(methods, call.Method)
	}
	assert.Contains(t, methods, MethodV2SubscribeShell)
	assert.Contains(t, methods, MethodV2SubscribeArchivedShell)
}

// --- rediscovery and launch intent ---

func TestRediscoverPicksUpAMovedServer(t *testing.T) {
	t.Setenv("CONDUCTOR_T3_ORIGIN", "http://127.0.0.1:4000")
	t.Setenv("CONDUCTOR_T3_TOKEN", "tok2")
	c := &Client{Origin: "http://127.0.0.1:3773", Token: "tok"}
	c.SetProtocol(ProtocolV1)

	assert.True(t, c.Rediscover())
	assert.Equal(t, "http://127.0.0.1:4000", c.Origin)
	assert.Equal(t, "tok2", c.Token)
	c.mu.Lock()
	assert.Zero(t, c.protocol, "the protocol is re-detected after a move")
	c.mu.Unlock()
	assert.False(t, c.Rediscover())
}

// A server that never holds still across the three reads is retried, then
// reported as Inconsistent rather than as an exact picture.
func TestV2SnapshotRetriesThenReportsInconsistent(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	f.mu.Lock()
	f.archived = []byte(`{"schemaVersion":3,"snapshotSequence":1,"projects":[],"threads":[]}`)
	f.mu.Unlock()

	snapshot, err := c.Snapshot(testCtx(t))
	require.NoError(t, err)
	assert.True(t, snapshot.Inconsistent)
	assert.Len(t, f.callsTo(MethodV2GetArchivedShellSnapshot), 2*v2SnapshotAttempts)
}

// A turn whose run is preparing — the launch's setup script, a database clone
// — is not a turn that failed to start. The wait extends past Timeout while
// the run is visibly preparing, and succeeds once it starts.
func TestV2WaitForTurnWaitsThroughPreparing(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)
	setShell := func(body string) {
		f.mu.Lock()
		f.shell = []byte(`{"schemaVersion":3,"snapshotSequence":1,"projects":[],"archivedThreads":[],"threads":[` + body + `]}`)
		f.mu.Unlock()
	}
	setShell(`{"id":"thr-new","status":"idle","latestRunId":null,"activeRunId":null,"archivedAt":null,"deletedAt":null}`)
	require.NoError(t, c.StartTurn(ctx, "thr-new", "hello", ""))

	setShell(`{"id":"thr-new","status":"preparing","activityRunStatus":"preparing","latestRunId":"run-1","activeRunId":"run-1","archivedAt":null,"deletedAt":null}`)
	go func() {
		time.Sleep(2500 * time.Millisecond)
		setShell(`{"id":"thr-new","status":"running","activityRunStatus":"running","latestRunId":"run-1","latestRunStartedAt":"2026-10-03T04:00:00.000Z","activeRunId":"run-1","archivedAt":null,"deletedAt":null}`)
	}()
	outcome, err := c.WaitForTurnWith(ctx, "thr-new", TurnWait{Timeout: time.Second, MaxWait: 8 * time.Second})
	require.NoError(t, err, "preparing must extend the one-second timeout")
	assert.Equal(t, TurnStarted, outcome)
}

// Preparing is waited through only up to MaxWait.
func TestV2WaitForTurnPreparingIsCapped(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)
	f.mu.Lock()
	f.shell = []byte(`{"schemaVersion":3,"snapshotSequence":1,"projects":[],"archivedThreads":[],"threads":[
		{"id":"thr-new","status":"preparing","activityRunStatus":"preparing","latestRunId":"run-1","activeRunId":"run-1","archivedAt":null,"deletedAt":null}]}`)
	f.mu.Unlock()
	_, err := c.WaitForTurnWith(ctx, "thr-new", TurnWait{Timeout: time.Second, MaxWait: 2 * time.Second})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still preparing")
}

// Conductor's own provisioning counts as preparing too.
func TestWaitForTurnExtendsWhileProvisioning(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)
	f.mu.Lock()
	f.shell = []byte(`{"schemaVersion":3,"snapshotSequence":1,"projects":[],"archivedThreads":[],"threads":[
		{"id":"thr-new","status":"idle","latestRunId":null,"activeRunId":null,"archivedAt":null,"deletedAt":null}]}`)
	f.mu.Unlock()
	start := time.Now()
	_, err := c.WaitForTurnWith(ctx, "thr-new", TurnWait{
		Timeout: time.Second, MaxWait: 3 * time.Second,
		Provisioning: func() bool { return true },
	})
	require.Error(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 2500*time.Millisecond, "provisioning extended the wait to MaxWait")
}

// A message sent while a run is active is handed to it (steered or queued),
// which is a success as soon as the active run is seen running.
func TestV2WaitForTurnReportsJoiningTheActiveRun(t *testing.T) {
	f := newFakeT3(t, ProtocolV2)
	c := f.client()
	ctx := testCtx(t)
	require.NoError(t, c.StartTurn(ctx, "thr-running", "also this", ""))
	outcome, err := c.WaitForTurnWith(ctx, "thr-running", TurnWait{Timeout: 2 * time.Second})
	require.NoError(t, err)
	assert.Equal(t, TurnJoinedActive, outcome)
}
