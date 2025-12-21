package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	conductorDir = ".conductor"
	configFile   = "conductor.json"
)

// ConductorDir returns the main conductor directory path (~/.conductor)
func ConductorDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, conductorDir), nil
}

// WorktreeBasePath returns the path where worktrees are stored for a project
func WorktreeBasePath(projectName string) (string, error) {
	dir, err := ConductorDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectName), nil
}

// WorktreePath returns the full path for a specific worktree
func WorktreePath(projectName, worktreeName string) (string, error) {
	base, err := WorktreeBasePath(projectName)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, worktreeName), nil
}

// ConfigPath returns the full path to the config file
func ConfigPath() (string, error) {
	dir, err := ConductorDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFile), nil
}

// Exists checks if the config file exists
func Exists() bool {
	path, err := ConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Load reads the config from disk
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("conductor not initialized. Run 'conductor init' first")
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Initialize maps if nil
	if cfg.PortAllocations == nil {
		cfg.PortAllocations = make(map[string]*PortAlloc)
	}
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]*Project)
	}

	return &cfg, nil
}

// Save writes the config to disk
func Save(cfg *Config) error {
	dir, err := ConductorDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create conductor directory: %w", err)
	}

	path, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// Init creates a new config file with defaults
func Init() error {
	if Exists() {
		return fmt.Errorf("conductor already initialized")
	}

	cfg := NewConfig()
	return Save(cfg)
}

// LoadProjectConfig reads a project's conductor.json
func LoadProjectConfig(projectPath string) (*ProjectConfig, error) {
	path := filepath.Join(projectPath, "conductor.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No project config is fine
		}
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &cfg, nil
}

// SaveProjectConfig writes a project's conductor.json
func SaveProjectConfig(projectPath string, cfg *ProjectConfig) error {
	path := filepath.Join(projectPath, "conductor.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}
