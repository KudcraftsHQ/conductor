package ipc

import (
	"net"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

const socketName = "tui.sock"

// RefreshRequestMsg is sent when CLI requests a refresh
type RefreshRequestMsg struct{}

// Server listens for refresh requests from CLI commands
type Server struct {
	listener net.Listener
	program  *tea.Program
	sockPath string
}

// SocketPath returns the path to the Unix socket
func SocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".conductor", socketName), nil
}

// NewServer creates a new IPC server
func NewServer(program *tea.Program) (*Server, error) {
	sockPath, err := SocketPath()
	if err != nil {
		return nil, err
	}

	// Remove stale socket if exists (ignore error - may not exist)
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}

	return &Server{
		listener: listener,
		program:  program,
		sockPath: sockPath,
	}, nil
}

// Start begins accepting connections (run in goroutine)
func (s *Server) Start() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // Server closed
		}

		// Read message (simple: any connection = refresh request)
		buf := make([]byte, 64)
		_, _ = conn.Read(buf)
		_ = conn.Close()

		// Send message to TUI
		s.program.Send(RefreshRequestMsg{})
	}
}

// Close shuts down the server and removes the socket
func (s *Server) Close() error {
	err := s.listener.Close()
	_ = os.Remove(s.sockPath) // Ignore error - best effort cleanup
	return err
}
