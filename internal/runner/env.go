package runner

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hammashamzah/conductor/internal/config"
)

// BuildEnv creates environment variables for script execution
func BuildEnv(projectName string, project *config.Project, worktreeName string, worktree *config.Worktree, projectConfig *config.ProjectConfig) []string {
	env := os.Environ()

	// Basic info
	env = append(env, fmt.Sprintf("CONDUCTOR_PROJECT_NAME=%s", projectName))
	env = append(env, fmt.Sprintf("CONDUCTOR_WORKSPACE_NAME=%s", worktreeName))
	env = append(env, fmt.Sprintf("CONDUCTOR_ROOT_PATH=%s", project.Path))
	env = append(env, fmt.Sprintf("CONDUCTOR_WORKTREE_PATH=%s", worktree.Path))
	env = append(env, fmt.Sprintf("CONDUCTOR_IS_ROOT=%t", worktree.IsRoot))
	env = append(env, fmt.Sprintf("CONDUCTOR_BRANCH=%s", worktree.Branch))

	// Ports
	if len(worktree.Ports) > 0 {
		// First port as main port
		env = append(env, fmt.Sprintf("CONDUCTOR_PORT=%d", worktree.Ports[0]))
		env = append(env, fmt.Sprintf("PORT=%d", worktree.Ports[0]))

		// Port count
		env = append(env, fmt.Sprintf("CONDUCTOR_PORT_COUNT=%d", len(worktree.Ports)))

		// All ports as comma-separated
		portStrs := make([]string, len(worktree.Ports))
		for i, p := range worktree.Ports {
			portStrs[i] = strconv.Itoa(p)
		}
		env = append(env, fmt.Sprintf("CONDUCTOR_PORTS=%s", strings.Join(portStrs, ",")))

		// Indexed ports
		for i, port := range worktree.Ports {
			env = append(env, fmt.Sprintf("CONDUCTOR_PORT_%d=%d", i, port))
		}

		// Labeled ports (if project config exists)
		if projectConfig != nil && len(projectConfig.Ports.Labels) > 0 {
			for i, label := range projectConfig.Ports.Labels {
				if i < len(worktree.Ports) {
					envName := fmt.Sprintf("CONDUCTOR_PORT_%s", strings.ToUpper(label))
					env = append(env, fmt.Sprintf("%s=%d", envName, worktree.Ports[i]))
				}
			}
		}
	}

	// Tunnel environment variables
	if worktree.Tunnel != nil && worktree.Tunnel.Active {
		env = append(env, "CONDUCTOR_TUNNEL_ACTIVE=true")
		env = append(env, fmt.Sprintf("CONDUCTOR_TUNNEL_URL=%s", worktree.Tunnel.URL))
		env = append(env, fmt.Sprintf("CONDUCTOR_TUNNEL_PORT=%d", worktree.Tunnel.Port))
		env = append(env, fmt.Sprintf("CONDUCTOR_TUNNEL_MODE=%s", worktree.Tunnel.Mode))
	} else {
		env = append(env, "CONDUCTOR_TUNNEL_ACTIVE=false")
	}

	// Database environment variables (only if database is configured for worktree)
	if worktree.DatabaseName != "" {
		env = append(env, fmt.Sprintf("CONDUCTOR_DB_NAME=%s", worktree.DatabaseName))
		env = append(env, fmt.Sprintf("CONDUCTOR_DB_URL=%s", worktree.DatabaseURL))
		env = append(env, fmt.Sprintf("DATABASE_URL=%s", worktree.DatabaseURL)) // Common convention
	}

	// Source database reference (read-only, for reference in scripts)
	if project.Database != nil && project.Database.Source != "" {
		env = append(env, fmt.Sprintf("CONDUCTOR_DB_SOURCE=%s", project.Database.Source))
	}

	// ClickUp task environment variables
	if worktree.ClickUpTaskID != "" {
		env = append(env, fmt.Sprintf("CONDUCTOR_TASK_ID=%s", worktree.ClickUpTaskID))
		env = append(env, fmt.Sprintf("CONDUCTOR_TASK_URL=%s", worktree.ClickUpTaskURL))
	}

	// Tooling detection environment variables
	if project.Tooling != nil {
		env = append(env, fmt.Sprintf("CONDUCTOR_FRAMEWORK=%s", project.Tooling.Framework))
		env = append(env, fmt.Sprintf("CONDUCTOR_WEB_ELIGIBLE=%t", project.Tooling.WebEligible))
		env = append(env, fmt.Sprintf("CONDUCTOR_UI_TYPE=%s", project.Tooling.UIType))

		// ProofShot environment variables (only for web-eligible projects with ProofShot installed)
		if project.Tooling.ProofShotReady && project.Tooling.WebEligible && len(worktree.Ports) > 0 {
			env = append(env, fmt.Sprintf("PROOFSHOT_PORT=%d", worktree.Ports[0]))
			if projectConfig != nil {
				if runCmd, ok := projectConfig.Scripts["run"]; ok {
					env = append(env, fmt.Sprintf("PROOFSHOT_RUN_CMD=%s", runCmd))
				}
			}
		}

		// TrustLayer environment variable
		if project.Tooling.TrustLayerInit {
			env = append(env, "CONDUCTOR_TRUSTLAYER=true")
		}
	}

	// Auth environment variables
	if projectConfig != nil && projectConfig.Auth != nil {
		env = append(env, fmt.Sprintf("CONDUCTOR_AUTH_TYPE=%s", projectConfig.Auth.Type))
		if projectConfig.Auth.LoginURL != "" {
			env = append(env, fmt.Sprintf("CONDUCTOR_AUTH_LOGIN_URL=%s", projectConfig.Auth.LoginURL))
		}
		if projectConfig.Auth.CallbackURL != "" {
			env = append(env, fmt.Sprintf("CONDUCTOR_AUTH_CALLBACK_URL=%s", projectConfig.Auth.CallbackURL))
		}
		if projectConfig.Auth.SeedCommand != "" {
			env = append(env, fmt.Sprintf("CONDUCTOR_AUTH_SEED_CMD=%s", projectConfig.Auth.SeedCommand))
		}
	}

	return env
}

// GetEnvMap returns environment as a map for display
func GetEnvMap(projectName string, project *config.Project, worktreeName string, worktree *config.Worktree, projectConfig *config.ProjectConfig) map[string]string {
	result := make(map[string]string)

	result["CONDUCTOR_PROJECT_NAME"] = projectName
	result["CONDUCTOR_WORKSPACE_NAME"] = worktreeName
	result["CONDUCTOR_ROOT_PATH"] = project.Path
	result["CONDUCTOR_WORKTREE_PATH"] = worktree.Path
	result["CONDUCTOR_IS_ROOT"] = strconv.FormatBool(worktree.IsRoot)
	result["CONDUCTOR_BRANCH"] = worktree.Branch

	if len(worktree.Ports) > 0 {
		result["CONDUCTOR_PORT"] = strconv.Itoa(worktree.Ports[0])
		result["PORT"] = strconv.Itoa(worktree.Ports[0])
		result["CONDUCTOR_PORT_COUNT"] = strconv.Itoa(len(worktree.Ports))

		portStrs := make([]string, len(worktree.Ports))
		for i, p := range worktree.Ports {
			portStrs[i] = strconv.Itoa(p)
		}
		result["CONDUCTOR_PORTS"] = strings.Join(portStrs, ",")

		for i, port := range worktree.Ports {
			result[fmt.Sprintf("CONDUCTOR_PORT_%d", i)] = strconv.Itoa(port)
		}

		if projectConfig != nil {
			for i, label := range projectConfig.Ports.Labels {
				if i < len(worktree.Ports) {
					envName := fmt.Sprintf("CONDUCTOR_PORT_%s", strings.ToUpper(label))
					result[envName] = strconv.Itoa(worktree.Ports[i])
				}
			}
		}
	}

	// Tunnel environment variables
	if worktree.Tunnel != nil && worktree.Tunnel.Active {
		result["CONDUCTOR_TUNNEL_ACTIVE"] = "true"
		result["CONDUCTOR_TUNNEL_URL"] = worktree.Tunnel.URL
		result["CONDUCTOR_TUNNEL_PORT"] = strconv.Itoa(worktree.Tunnel.Port)
		result["CONDUCTOR_TUNNEL_MODE"] = string(worktree.Tunnel.Mode)
	} else {
		result["CONDUCTOR_TUNNEL_ACTIVE"] = "false"
	}

	// Database environment variables (only if database is configured for worktree)
	if worktree.DatabaseName != "" {
		result["CONDUCTOR_DB_NAME"] = worktree.DatabaseName
		result["CONDUCTOR_DB_URL"] = worktree.DatabaseURL
		result["DATABASE_URL"] = worktree.DatabaseURL
	}

	// Source database reference
	if project.Database != nil && project.Database.Source != "" {
		result["CONDUCTOR_DB_SOURCE"] = project.Database.Source
	}

	// ClickUp task environment variables
	if worktree.ClickUpTaskID != "" {
		result["CONDUCTOR_TASK_ID"] = worktree.ClickUpTaskID
		result["CONDUCTOR_TASK_URL"] = worktree.ClickUpTaskURL
	}

	// Tooling detection environment variables
	if project.Tooling != nil {
		result["CONDUCTOR_FRAMEWORK"] = project.Tooling.Framework
		result["CONDUCTOR_WEB_ELIGIBLE"] = strconv.FormatBool(project.Tooling.WebEligible)
		result["CONDUCTOR_UI_TYPE"] = project.Tooling.UIType

		// ProofShot environment variables
		if project.Tooling.ProofShotReady && project.Tooling.WebEligible && len(worktree.Ports) > 0 {
			result["PROOFSHOT_PORT"] = strconv.Itoa(worktree.Ports[0])
			if projectConfig != nil {
				if runCmd, ok := projectConfig.Scripts["run"]; ok {
					result["PROOFSHOT_RUN_CMD"] = runCmd
				}
			}
		}

		// TrustLayer environment variable
		if project.Tooling.TrustLayerInit {
			result["CONDUCTOR_TRUSTLAYER"] = "true"
		}
	}

	// Auth environment variables
	if projectConfig != nil && projectConfig.Auth != nil {
		result["CONDUCTOR_AUTH_TYPE"] = projectConfig.Auth.Type
		if projectConfig.Auth.LoginURL != "" {
			result["CONDUCTOR_AUTH_LOGIN_URL"] = projectConfig.Auth.LoginURL
		}
		if projectConfig.Auth.CallbackURL != "" {
			result["CONDUCTOR_AUTH_CALLBACK_URL"] = projectConfig.Auth.CallbackURL
		}
		if projectConfig.Auth.SeedCommand != "" {
			result["CONDUCTOR_AUTH_SEED_CMD"] = projectConfig.Auth.SeedCommand
		}
	}

	return result
}
