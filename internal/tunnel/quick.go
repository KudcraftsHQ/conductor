package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// QuickTunnel represents a running quick tunnel process
type QuickTunnel struct {
	ProjectName  string
	WorktreeName string
	Port         int
	URL          string
	PID          int
	Cmd          *exec.Cmd
	Cancel       context.CancelFunc
	LogBuffer    *LogBuffer
	StartedAt    time.Time
}

// LogBuffer is a thread-safe circular buffer for tunnel logs
type LogBuffer struct {
	mu    sync.RWMutex
	lines []string
	max   int
}

// NewLogBuffer creates a new log buffer with max lines
func NewLogBuffer(maxLines int) *LogBuffer {
	return &LogBuffer{
		lines: make([]string, 0, maxLines),
		max:   maxLines,
	}
}

// Write adds a line to the buffer
func (b *LogBuffer) Write(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[1:]
	}
}

// Lines returns all lines in the buffer
func (b *LogBuffer) Lines() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]string, len(b.lines))
	copy(result, b.lines)
	return result
}

// String returns all lines joined by newlines
func (b *LogBuffer) String() string {
	return strings.Join(b.Lines(), "\n")
}

// urlPatterns are regex patterns to extract tunnel URL from cloudflared output
var urlPatterns = []*regexp.Regexp{
	// Standard output format: "Your quick Tunnel has been created! Visit it at (propagates globally within seconds):"
	// followed by the URL on the next line
	regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`),
	// Alternative format with INF prefix
	regexp.MustCompile(`INF\s+\|\s+(https://[a-zA-Z0-9-]+\.trycloudflare\.com)`),
}

// parseQuickTunnelURL attempts to extract the tunnel URL from cloudflared output
func parseQuickTunnelURL(output string) string {
	for _, pattern := range urlPatterns {
		matches := pattern.FindStringSubmatch(output)
		if len(matches) > 0 {
			// Return the full match or the first capturing group
			if len(matches) > 1 {
				return matches[1]
			}
			return matches[0]
		}
	}
	return ""
}

// StartQuickTunnel starts a cloudflared quick tunnel for the given port
// Command: cloudflared tunnel --url http://localhost:PORT
func StartQuickTunnel(ctx context.Context, projectName, worktreeName string, port int) (*QuickTunnel, error) {
	// Check if cloudflared is installed
	if _, err := exec.LookPath("cloudflared"); err != nil {
		return nil, fmt.Errorf("cloudflared not found. Install with: brew install cloudflared")
	}

	// Create cancellable context
	tunnelCtx, cancel := context.WithCancel(ctx)

	// Build command
	cmd := exec.CommandContext(tunnelCtx, "cloudflared", "tunnel", "--url", fmt.Sprintf("http://localhost:%d", port))

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start cloudflared: %w", err)
	}

	tunnel := &QuickTunnel{
		ProjectName:  projectName,
		WorktreeName: worktreeName,
		Port:         port,
		PID:          cmd.Process.Pid,
		Cmd:          cmd,
		Cancel:       cancel,
		LogBuffer:    NewLogBuffer(100),
		StartedAt:    time.Now(),
	}

	// Channel to receive the URL
	urlChan := make(chan string, 1)
	urlFound := false
	var urlMu sync.Mutex

	// Read output and look for URL
	readOutput := func(r io.Reader, prefix string) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			tunnel.LogBuffer.Write(prefix + line)

			// Try to find URL
			urlMu.Lock()
			if !urlFound {
				if url := parseQuickTunnelURL(line); url != "" {
					urlFound = true
					tunnel.URL = url
					select {
					case urlChan <- url:
					default:
					}
				}
			}
			urlMu.Unlock()
		}
	}

	// Start reading in goroutines
	go readOutput(stdout, "")
	go readOutput(stderr, "")

	// Wait for URL with timeout
	select {
	case url := <-urlChan:
		tunnel.URL = url

		// Save PID file
		pidFile := &PIDFile{
			PID:          tunnel.PID,
			ProjectName:  projectName,
			WorktreeName: worktreeName,
			Mode:         config.TunnelModeQuick,
			Port:         port,
			URL:          url,
			StartedAt:    tunnel.StartedAt,
		}
		if err := WritePIDFile(projectName, worktreeName, pidFile); err != nil {
			// Log but don't fail
			tunnel.LogBuffer.Write(fmt.Sprintf("Warning: failed to write PID file: %v", err))
		}

		return tunnel, nil

	case <-time.After(30 * time.Second):
		// Timeout waiting for URL
		cancel()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("timeout waiting for tunnel URL. Check logs: %s", tunnel.LogBuffer.String())

	case <-tunnelCtx.Done():
		return nil, fmt.Errorf("tunnel context cancelled")
	}
}

// Stop stops the quick tunnel
func (t *QuickTunnel) Stop() error {
	if t.Cancel != nil {
		t.Cancel()
	}

	if t.Cmd != nil && t.Cmd.Process != nil {
		if err := KillProcess(t.PID); err != nil {
			return err
		}
	}

	// Delete PID file
	return DeletePIDFile(t.ProjectName, t.WorktreeName)
}

// ToTunnelState converts the quick tunnel to a TunnelState for config storage
func (t *QuickTunnel) ToTunnelState() *config.TunnelState {
	return &config.TunnelState{
		Active:    true,
		Mode:      config.TunnelModeQuick,
		URL:       t.URL,
		Port:      t.Port,
		PID:       t.PID,
		StartedAt: t.StartedAt,
	}
}
