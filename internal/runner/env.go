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

	return result
}
