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

func TestBuildEnv_WithTooling(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
		Tooling: &config.ProjectTooling{
			Framework:      "nextjs",
			Language:       "typescript",
			PackageManager: "bun",
			WebEligible:    true,
			UIType:         "browser",
			ProofShotReady: true,
			TrustLayerInit: true,
		},
	}
	worktree := &config.Worktree{
		Path:   "/path/to/worktree",
		Branch: "main",
		Ports:  []int{3100},
	}
	projectConfig := &config.ProjectConfig{
		Scripts: map[string]string{
			"run": "bun run dev",
		},
	}

	env := BuildEnv("myproject", project, "tokyo", worktree, projectConfig)

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, "nextjs", envMap["CONDUCTOR_FRAMEWORK"])
	assert.Equal(t, "true", envMap["CONDUCTOR_WEB_ELIGIBLE"])
	assert.Equal(t, "browser", envMap["CONDUCTOR_UI_TYPE"])
	assert.Equal(t, "3100", envMap["PROOFSHOT_PORT"])
	assert.Equal(t, "bun run dev", envMap["PROOFSHOT_RUN_CMD"])
	assert.Equal(t, "true", envMap["CONDUCTOR_TRUSTLAYER"])
}

func TestBuildEnv_ToolingNotWebEligible(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
		Tooling: &config.ProjectTooling{
			Framework:   "go",
			Language:    "go",
			WebEligible: false,
			UIType:      "none",
		},
	}
	worktree := &config.Worktree{
		Path:   "/path/to/worktree",
		Branch: "main",
		Ports:  []int{3100},
	}

	env := BuildEnv("myproject", project, "tokyo", worktree, nil)

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	assert.Equal(t, "go", envMap["CONDUCTOR_FRAMEWORK"])
	assert.Equal(t, "false", envMap["CONDUCTOR_WEB_ELIGIBLE"])
	assert.Equal(t, "none", envMap["CONDUCTOR_UI_TYPE"])
	// ProofShot vars should NOT be set
	_, hasProofPort := envMap["PROOFSHOT_PORT"]
	assert.False(t, hasProofPort)
	_, hasProofCmd := envMap["PROOFSHOT_RUN_CMD"]
	assert.False(t, hasProofCmd)
	// TrustLayer should NOT be set
	_, hasTL := envMap["CONDUCTOR_TRUSTLAYER"]
	assert.False(t, hasTL)
}

func TestGetEnvMap_WithTooling(t *testing.T) {
	project := &config.Project{
		Path: "/path/to/project",
		Tooling: &config.ProjectTooling{
			Framework:      "vite",
			Language:       "typescript",
			WebEligible:    true,
			UIType:         "browser",
			ProofShotReady: true,
		},
	}
	worktree := &config.Worktree{
		Path:   "/path/to/worktree",
		Branch: "main",
		Ports:  []int{3200},
	}
	projectConfig := &config.ProjectConfig{
		Scripts: map[string]string{
			"run": "pnpm dev",
		},
	}

	envMap := GetEnvMap("myproject", project, "tokyo", worktree, projectConfig)

	assert.Equal(t, "vite", envMap["CONDUCTOR_FRAMEWORK"])
	assert.Equal(t, "true", envMap["CONDUCTOR_WEB_ELIGIBLE"])
	assert.Equal(t, "browser", envMap["CONDUCTOR_UI_TYPE"])
	assert.Equal(t, "3200", envMap["PROOFSHOT_PORT"])
	assert.Equal(t, "pnpm dev", envMap["PROOFSHOT_RUN_CMD"])
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
