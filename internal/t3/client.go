// Package t3 is a client for a locally running T3 Code server
// (https://github.com/pingdotgg/t3code).
//
// Two transports are involved, because T3 splits its surface across them:
//
//   - HTTP, for the orchestration command bus (projects, threads, turns) and
//     for read-only snapshots.
//   - WebSocket, for everything else — terminals, worktrees, previews. These
//     have no HTTP equivalent, so terminal control requires the socket.
//
// The server is alpha and its API is unversioned: the schemas are compiled
// into its bundle rather than published. Decoding is therefore deliberately
// lenient about unknown fields, and every call reports the server's own error
// payload rather than swallowing it, so drift surfaces loudly.
package t3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultOrigin is used when the running server cannot be discovered.
const DefaultOrigin = "http://127.0.0.1:3773"

// tokenFileName is the file under the conductor directory holding the bearer
// token issued by `t3 auth session issue`.
const tokenFileName = "t3-token"

// Client talks to one T3 Code server.
type Client struct {
	Origin string
	Token  string
	HTTP   *http.Client
}

// New builds a Client, discovering the origin and token from the environment.
func New() (*Client, error) {
	origin, err := DiscoverOrigin()
	if err != nil {
		return nil, err
	}
	token, err := DiscoverToken()
	if err != nil {
		return nil, err
	}
	return &Client{
		Origin: origin,
		Token:  token,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// serverRuntime mirrors the file T3 writes when its server starts. It is the
// authoritative source for the port, which is not fixed.
type serverRuntime struct {
	PID    int    `json:"pid"`
	Port   int    `json:"port"`
	Origin string `json:"origin"`
}

// DiscoverOrigin resolves the base URL of the running T3 server.
//
// CONDUCTOR_T3_ORIGIN wins. Otherwise the server's own runtime-state file is
// read, since the port is chosen at startup and is not guaranteed to be 3773.
func DiscoverOrigin() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CONDUCTOR_T3_ORIGIN")); v != "" {
		return strings.TrimRight(v, "/"), nil
	}

	path, err := runtimeStatePath()
	if err != nil {
		return DefaultOrigin, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// No runtime file means the server is probably not running. Fall back
		// rather than failing here: the caller's first request will produce a
		// far clearer error than a missing file would.
		return DefaultOrigin, nil
	}
	var rt serverRuntime
	if err := json.Unmarshal(data, &rt); err != nil || rt.Origin == "" {
		return DefaultOrigin, nil
	}
	return strings.TrimRight(rt.Origin, "/"), nil
}

// runtimeStatePath returns the path to T3's server-runtime.json.
func runtimeStatePath() (string, error) {
	if home := strings.TrimSpace(os.Getenv("T3CODE_HOME")); home != "" {
		return filepath.Join(home, "userdata", "server-runtime.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".t3", "userdata", "server-runtime.json"), nil
}

// TokenPath returns the file conductor reads its T3 bearer token from.
func TokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".conductor", tokenFileName), nil
}

// DiscoverToken loads the bearer token, preferring the environment.
func DiscoverToken() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CONDUCTOR_T3_TOKEN")); v != "" {
		return v, nil
	}
	path, err := TokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"no T3 Code token found.\n\nIssue one and save it with:\n  t3 auth session issue --label conductor --ttl 365d --token-only > %s\n  chmod 600 %s",
				path, path)
		}
		return "", fmt.Errorf("failed to read T3 token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("T3 token file %s is empty", path)
	}
	return token, nil
}

// SaveToken writes a bearer token to the conductor token file with 0600.
func SaveToken(token string) error {
	path, err := TokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0600)
}

// apiError is the error envelope T3 returns on a non-2xx response.
type apiError struct {
	Tag     string `json:"_tag"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	TraceID string `json:"traceId"`
}

func (e apiError) Error() string {
	parts := []string{}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	if len(parts) == 0 {
		parts = append(parts, e.Tag)
	}
	msg := strings.Join(parts, ": ")
	if e.TraceID != "" {
		msg += " (trace " + e.TraceID + ")"
	}
	return msg
}

// do performs an authenticated request and decodes a JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.Origin+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("T3 Code server unreachable at %s: %w", c.Origin, err)
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var apiErr apiError
		if json.Unmarshal(data, &apiErr) == nil && (apiErr.Code != "" || apiErr.Tag != "") {
			if apiErr.Code == "auth_invalid" {
				return fmt.Errorf("T3 Code rejected conductor's token (%s). Re-issue it with `t3 auth session issue`", apiErr.Error())
			}
			return fmt.Errorf("T3 Code %s %s: %s", method, path, apiErr.Error())
		}
		return fmt.Errorf("T3 Code %s %s: HTTP %d: %s", method, path, res.StatusCode, truncate(string(data), 200))
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode T3 response for %s: %w", path, err)
	}
	return nil
}

// Ping reports whether the server is reachable and the token is accepted.
func (c *Client) Ping(ctx context.Context) error {
	var state struct {
		Authenticated bool     `json:"authenticated"`
		Scopes        []string `json:"scopes"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/auth/session", nil, &state); err != nil {
		return err
	}
	if !state.Authenticated {
		return fmt.Errorf("T3 Code did not accept conductor's token")
	}
	for _, required := range []string{"orchestration:operate", "terminal:operate"} {
		if !contains(state.Scopes, required) {
			return fmt.Errorf("T3 token is missing the %q scope (has: %s)", required, strings.Join(state.Scopes, ", "))
		}
	}
	return nil
}

// Shell returns the lightweight snapshot of projects and threads.
func (c *Client) Shell(ctx context.Context) (*ShellSnapshot, error) {
	var snapshot ShellSnapshot
	if err := c.do(ctx, http.MethodGet, "/api/orchestration/shell", nil, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// Dispatch submits one orchestration command to the command bus.
func (c *Client) Dispatch(ctx context.Context, command any) error {
	return c.do(ctx, http.MethodPost, "/api/orchestration/dispatch", command, nil)
}

// websocketTicket mints the short-lived ticket the /ws upgrade requires. The
// bearer token is not accepted on the upgrade itself.
func (c *Client) websocketTicket(ctx context.Context) (string, error) {
	var result struct {
		Ticket string `json:"ticket"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/auth/websocket-ticket", nil, &result); err != nil {
		return "", err
	}
	if result.Ticket == "" {
		return "", fmt.Errorf("T3 Code returned an empty websocket ticket")
	}
	return result.Ticket, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
