package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestBuildEnv_SinglePort(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "feature-x",
		Ports:     []int{3100},
		IsRoot:    false,
		CreatedAt: time.Now(),
	}

	env := BuildEnv("myproject", project, "tokyo", worktree, nil)

	// Convert to map for easier assertion
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, "myproject", envMap["CONDUCTOR_PROJECT_NAME"])
	assert.Equal(t, "tokyo", envMap["CONDUCTOR_WORKSPACE_NAME"])
	assert.Equal(t, "/path/to/project", envMap["CONDUCTOR_ROOT_PATH"])
	assert.Equal(t, "/path/to/worktree", envMap["CONDUCTOR_WORKTREE_PATH"])
	assert.Equal(t, "false", envMap["CONDUCTOR_IS_ROOT"])
	assert.Equal(t, "feature-x", envMap["CONDUCTOR_BRANCH"])
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT"])
	assert.Equal(t, "3100", envMap["PORT"])
	assert.Equal(t, "1", envMap["CONDUCTOR_PORT_COUNT"])
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORTS"])
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT_0"])
}

func TestBuildEnv_MultiplePorts(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "main",
		Ports:     []int{3100, 3101, 3102},
		IsRoot:    true,
		CreatedAt: time.Now(),
	}

	env := BuildEnv("myproject", project, "root", worktree, nil)

	// Convert to map for easier assertion
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT"])
	assert.Equal(t, "3", envMap["CONDUCTOR_PORT_COUNT"])
	assert.Equal(t, "3100,3101,3102", envMap["CONDUCTOR_PORTS"])
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT_0"])
	assert.Equal(t, "3101", envMap["CONDUCTOR_PORT_1"])
	assert.Equal(t, "3102", envMap["CONDUCTOR_PORT_2"])
	assert.Equal(t, "true", envMap["CONDUCTOR_IS_ROOT"])
}

func TestBuildEnv_WithLabels(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "main",
		Ports:     []int{3100, 3101, 3102},
		IsRoot:    false,
		CreatedAt: time.Now(),
	}
	projectConfig := &config.ProjectConfig{
		Ports: config.PortConfig{
			Default: 3,
			Labels:  []string{"web", "api", "db"},
		},
	}

	env := BuildEnv("myproject", project, "tokyo", worktree, projectConfig)

	// Convert to map for easier assertion
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT_WEB"])
	assert.Equal(t, "3101", envMap["CONDUCTOR_PORT_API"])
	assert.Equal(t, "3102", envMap["CONDUCTOR_PORT_DB"])
}

func TestBuildEnv_NoPorts(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "main",
		Ports:     []int{},
		IsRoot:    true,
		CreatedAt: time.Now(),
	}

	env := BuildEnv("myproject", project, "root", worktree, nil)

	// Convert to map for easier assertion
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Should still have basic info
	assert.Equal(t, "myproject", envMap["CONDUCTOR_PROJECT_NAME"])
	assert.Equal(t, "root", envMap["CONDUCTOR_WORKSPACE_NAME"])

	// Should not have port-related vars
	_, hasPort := envMap["CONDUCTOR_PORT"]
	assert.False(t, hasPort)
	_, hasPorts := envMap["CONDUCTOR_PORTS"]
	assert.False(t, hasPorts)
}

func TestGetEnvMap_SinglePort(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "feature-x",
		Ports:     []int{3100},
		IsRoot:    false,
		CreatedAt: time.Now(),
	}

	envMap := GetEnvMap("myproject", project, "tokyo", worktree, nil)

	assert.Equal(t, "myproject", envMap["CONDUCTOR_PROJECT_NAME"])
	assert.Equal(t, "tokyo", envMap["CONDUCTOR_WORKSPACE_NAME"])
	assert.Equal(t, "/path/to/project", envMap["CONDUCTOR_ROOT_PATH"])
	assert.Equal(t, "/path/to/worktree", envMap["CONDUCTOR_WORKTREE_PATH"])
	assert.Equal(t, "false", envMap["CONDUCTOR_IS_ROOT"])
	assert.Equal(t, "feature-x", envMap["CONDUCTOR_BRANCH"])
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT"])
	assert.Equal(t, "3100", envMap["PORT"])
}

func TestGetEnvMap_WithLabels(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "main",
		Ports:     []int{3100, 3101},
		IsRoot:    false,
		CreatedAt: time.Now(),
	}
	projectConfig := &config.ProjectConfig{
		Ports: config.PortConfig{
			Default: 2,
			Labels:  []string{"frontend", "backend"},
		},
	}

	envMap := GetEnvMap("myproject", project, "tokyo", worktree, projectConfig)

	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT_FRONTEND"])
	assert.Equal(t, "3101", envMap["CONDUCTOR_PORT_BACKEND"])
}

func TestGetEnvMap_MoreLabelsThanPorts(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
	}
	worktree := &config.Worktree{
		Path:      "/path/to/worktree",
		Branch:    "main",
		Ports:     []int{3100},
		IsRoot:    false,
		CreatedAt: time.Now(),
	}
	projectConfig := &config.ProjectConfig{
		Ports: config.PortConfig{
			Default: 3,
			Labels:  []string{"web", "api", "db"},
		},
	}

	envMap := GetEnvMap("myproject", project, "tokyo", worktree, projectConfig)

	// Only first label should be set since we only have 1 port
	assert.Equal(t, "3100", envMap["CONDUCTOR_PORT_WEB"])
	_, hasApi := envMap["CONDUCTOR_PORT_API"]
	assert.False(t, hasApi)
	_, hasDb := envMap["CONDUCTOR_PORT_DB"]
	assert.False(t, hasDb)
}
