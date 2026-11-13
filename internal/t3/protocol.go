package t3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// T3 has shipped two orchestration protocols, and a nightly can move a machine
// from one to the other overnight. They are not wire-compatible:
//
//	V1 (≤ v0.0.45-nightly.20261002)
//	  GET  /api/orchestration/shell     active threads only
//	  GET  /api/orchestration/snapshot  every thread, archived included
//	  POST /api/orchestration/dispatch  the command bus (project.*, thread.*)
//	  /ws                                no protocol gate
//
//	V2 (Orchestrator V2, t3code de34391427)
//	  GET  /api/orchestration/shell     requires x-t3-orchestration-protocol: 2;
//	                                     active threads only, archivedThreads: []
//	  /api/orchestration/snapshot and /dispatch are gone
//	  POST /api/projects/mutate         project.create / project.delete
//	  /ws?orchestrationProtocol=2        426 without the query parameter;
//	                                     orchestration.dispatchCommand,
//	                                     orchestration.launchThread,
//	                                     orchestration.getArchivedShellSnapshot,
//	                                     orchestration.subscribeShell, …
//
// So the protocol is detected once per client from the server's own
// descriptor, cached, and thrown away whenever a request fails in a way that
// could mean the server underneath has changed — an unreachable origin, a 404
// on a V1 route, a 426 on the socket. The next call detects again.

// Protocol versions conductor speaks.
const (
	ProtocolV1 = 1
	ProtocolV2 = 2
)

// Wire names from packages/contracts/src/environment.ts.
const (
	protocolHeader     = "x-t3-orchestration-protocol"
	protocolQueryParam = "orchestrationProtocol"
	descriptorPath     = "/.well-known/t3/environment"
)

// EnvironmentDescriptor is the subset of ExecutionEnvironmentDescriptor
// conductor reads. orchestrationProtocolVersion is optionalKey in the schema
// and absent on hosts from before negotiation existed, which all speak V1.
type EnvironmentDescriptor struct {
	ServerVersion                string `json:"serverVersion"`
	OrchestrationProtocolVersion *int   `json:"orchestrationProtocolVersion"`
	Capabilities                 struct {
		// ServerResolvedCommandContext means the server picks the dispatch
		// mode for message.dispatch itself (deliveryIntent "auto"), so a client
		// need not read the thread's runs first.
		ServerResolvedCommandContext bool `json:"serverResolvedCommandContext"`
	} `json:"capabilities"`
}

// Protocol returns the orchestration protocol version of the descriptor.
func (d EnvironmentDescriptor) Protocol() int {
	if d.OrchestrationProtocolVersion == nil || *d.OrchestrationProtocolVersion < ProtocolV2 {
		return ProtocolV1
	}
	return *d.OrchestrationProtocolVersion
}

// errProtocolMismatch marks a failure that means the cached protocol is wrong.
var errProtocolMismatch = errors.New("T3 orchestration protocol changed")

// Protocol returns the server's orchestration protocol, detecting it on first
// use and after any reset.
func (c *Client) Protocol(ctx context.Context) (int, error) {
	c.mu.Lock()
	if c.protocol != 0 {
		p := c.protocol
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	descriptor, err := c.fetchDescriptor(ctx)
	if err != nil {
		return 0, err
	}
	p := descriptor.Protocol()
	if p > ProtocolV2 {
		return 0, fmt.Errorf("T3 Code at %s speaks orchestration protocol %d; this conductor knows 1 and 2 — update conductor", c.Origin, p)
	}

	c.mu.Lock()
	c.protocol = p
	c.descriptor = descriptor
	c.mu.Unlock()
	return p, nil
}

// SetProtocol pins the protocol, bypassing detection. Tests use it; so can a
// caller that already knows.
func (c *Client) SetProtocol(p int) {
	c.mu.Lock()
	c.protocol = p
	c.mu.Unlock()
}

// ResetProtocol drops the cached protocol so the next call re-detects it.
func (c *Client) ResetProtocol() {
	c.mu.Lock()
	c.protocol = 0
	c.descriptor = nil
	c.mu.Unlock()
}

// serverResolvesCommandContext reports the capability of the same name, from
// the descriptor read at detection.
func (c *Client) serverResolvesCommandContext() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.descriptor != nil && c.descriptor.Capabilities.ServerResolvedCommandContext
}

// fetchDescriptor reads /.well-known/t3/environment. The route is public and
// present on both protocols. A 404 means a server older than the descriptor,
// which can only be V1.
func (c *Client) fetchDescriptor(ctx context.Context) (*EnvironmentDescriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Origin+descriptorPath, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("T3 Code server unreachable at %s: %w", c.Origin, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		return &EnvironmentDescriptor{}, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("T3 Code GET %s: HTTP %d: %s", descriptorPath, res.StatusCode, truncate(string(data), 200))
	}
	var descriptor EnvironmentDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, fmt.Errorf("failed to decode T3 environment descriptor: %w", err)
	}
	return &descriptor, nil
}

// withProtocol runs fn under the detected protocol. When fn fails in a way that
// suggests the server changed protocol underneath a cached answer, the cache is
// dropped, the protocol re-detected, and fn retried once — but only if the
// answer actually changed, so a genuine 404 is not retried pointlessly.
func (c *Client) withProtocol(ctx context.Context, fn func(protocol int) error) error {
	p, err := c.Protocol(ctx)
	if err != nil {
		return err
	}
	err = fn(p)
	if err == nil || !errors.Is(err, errProtocolMismatch) {
		return err
	}
	c.ResetProtocol()
	next, detectErr := c.Protocol(ctx)
	if detectErr != nil || next == p {
		return err
	}
	return fn(next)
}

// protocolHeaders returns the extra headers an orchestration HTTP route needs.
func protocolHeaders(p int) map[string]string {
	if p >= ProtocolV2 {
		return map[string]string{protocolHeader: fmt.Sprint(p)}
	}
	return nil
}

// looksLikeProtocolMismatch classifies an HTTP failure on an orchestration
// route. V1-only routes 404 on V2; V2's shell rejects a request without the
// protocol header as a bad request.
func looksLikeProtocolMismatch(status int, body string) bool {
	switch status {
	case http.StatusNotFound, http.StatusUpgradeRequired:
		return true
	case http.StatusBadRequest:
		// Live V2 (0.0.46-nightly.20261003) rejects a shell request without
		// the protocol header with an empty 400 body.
		return strings.TrimSpace(body) == "" ||
			strings.Contains(strings.ToLower(body), protocolHeader) ||
			strings.Contains(body, "orchestration_protocol")
	}
	return false
}

// looksLikeHTML reports whether a response body is an HTML page.
func looksLikeHTML(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "<")
}
