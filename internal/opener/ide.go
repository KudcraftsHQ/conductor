package opener

import (
	"fmt"
	"os/exec"
)

// IDEType represents different IDE/editors
type IDEType string

const (
	IDECursor IDEType = "cursor"
	IDEVSCode IDEType = "vscode"
	IDEZed    IDEType = "zed"
	IDENvim   IDEType = "nvim"
)

// OpenInIDE opens a path in the specified IDE
func OpenInIDE(path string, ideType IDEType) error {
	switch ideType {
	case IDECursor:
		return openWithCommand("cursor", path)
	case IDEVSCode:
		return openWithCommand("code", path)
	case IDEZed:
		return openWithCommand("zed", path)
	case IDENvim:
		return openWithNvim(path)
	default:
		return openWithCommand(string(ideType), path)
	}
}

// openWithCommand opens a path with a command
func openWithCommand(command, path string) error {
	cmd := exec.Command(command, path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open with %s: %w", command, err)
	}
	return nil
}

// openWithNvim opens nvim in a new terminal window
func openWithNvim(path string) error {
	return OpenInTerminal(path, DetectTerminal(), fmt.Sprintf("cd %s && nvim .", path))
}

// IsIDEAvailable checks if an IDE is available
func IsIDEAvailable(ideType IDEType) bool {
	var command string
	switch ideType {
	case IDECursor:
		command = "cursor"
	case IDEVSCode:
		command = "code"
	case IDEZed:
		command = "zed"
	case IDENvim:
		command = "nvim"
	default:
		command = string(ideType)
	}

	_, err := exec.LookPath(command)
	return err == nil
}

// DetectAvailableIDEs returns a list of available IDEs
func DetectAvailableIDEs() []IDEType {
	ides := []IDEType{IDECursor, IDEVSCode, IDEZed, IDENvim}
	available := make([]IDEType, 0)

	for _, ide := range ides {
		if IsIDEAvailable(ide) {
			available = append(available, ide)
		}
	}

	return available
}
