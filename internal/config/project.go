package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AddProject registers a new project
func (c *Config) AddProject(path string, defaultPorts int) (string, error) {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Validate it's a git repo
	if !isGitRepo(absPath) {
		return "", fmt.Errorf("not a git repository: %s", absPath)
	}

	// Derive project name from directory
	name := filepath.Base(absPath)

	// Check if already registered
	if _, exists := c.Projects[name]; exists {
		return "", fmt.Errorf("project '%s' already registered", name)
	}

	// Get current branch
	branch, err := getCurrentBranch(absPath)
	if err != nil {
		branch = "main" // fallback
	}

	// Use default ports if not specified
	if defaultPorts <= 0 {
		defaultPorts = c.Defaults.PortsPerWorktree
	}

	// Allocate ports for root worktree
	ports, err := c.AllocatePorts(name, "root", defaultPorts)
	if err != nil {
		return "", fmt.Errorf("failed to allocate ports: %w", err)
	}

	// Create project
	project := NewProject(absPath, defaultPorts)
	project.Worktrees["root"] = NewWorktree(absPath, branch, true, ports)

	c.Projects[name] = project

	return name, nil
}

// RemoveProject unregisters a project and frees all its ports
func (c *Config) RemoveProject(name string) error {
	project, exists := c.Projects[name]
	if !exists {
		return fmt.Errorf("project '%s' not found", name)
	}

	// Free all ports for all worktrees
	for wtName := range project.Worktrees {
		c.FreeWorktreePorts(name, wtName)
	}

	delete(c.Projects, name)
	return nil
}

// GetProject returns a project by name
func (c *Config) GetProject(name string) (*Project, bool) {
	proj, ok := c.Projects[name]
	return proj, ok
}

// GetProjectByPath finds a project by its path
func (c *Config) GetProjectByPath(path string) (string, *Project, bool) {
	absPath, _ := filepath.Abs(path)

	for name, proj := range c.Projects {
		if proj.Path == absPath {
			return name, proj, true
		}
		// Also check worktree paths
		for _, wt := range proj.Worktrees {
			if wt.Path == absPath {
				return name, proj, true
			}
		}
	}
	return "", nil, false
}

// DetectProject tries to find which project the current directory belongs to
func (c *Config) DetectProject(path string) (string, *Project, *Worktree, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, nil, err
	}

	// Walk up the directory tree
	for {
		name, proj, ok := c.GetProjectByPath(absPath)
		if ok {
			// Find which worktree we're in
			for wtName, wt := range proj.Worktrees {
				if strings.HasPrefix(absPath, wt.Path) || wt.Path == absPath {
					return name, proj, proj.Worktrees[wtName], nil
				}
			}
			return name, proj, proj.Worktrees["root"], nil
		}

		parent := filepath.Dir(absPath)
		if parent == absPath {
			break
		}
		absPath = parent
	}

	return "", nil, nil, fmt.Errorf("not in a registered project")
}

// ListProjects returns all project names
func (c *Config) ListProjects() []string {
	names := make([]string, 0, len(c.Projects))
	for name := range c.Projects {
		names = append(names, name)
	}
	return names
}

// isGitRepo checks if a path is a git repository
func isGitRepo(path string) bool {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	// .git can be a directory (normal repo) or a file (worktree)
	return info.IsDir() || info.Mode().IsRegular()
}

// getCurrentBranch gets the current git branch
func getCurrentBranch(path string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
