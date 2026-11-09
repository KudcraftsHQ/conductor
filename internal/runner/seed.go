package runner

import (
	"io"
	"os/exec"

	"github.com/hammashamzah/conductor/internal/config"
)

// RunSeedCommand runs the project's database seedCommand, if one is configured,
// in the worktree directory with the same environment the setup script gets.
// It reports whether a command ran. Output goes to stdout and stderr.
func RunSeedCommand(projectName string, project *config.Project, worktreeName string, worktree *config.Worktree, stdout, stderr io.Writer) (bool, error) {
	if project.Database == nil || project.Database.SeedCommand == "" {
		return false, nil
	}
	projectConfig, _ := config.LoadProjectConfig(project.Path)

	cmd := exec.Command("bash", "-c", project.Database.SeedCommand)
	cmd.Dir = worktree.Path
	cmd.Env = BuildEnv(projectName, project, worktreeName, worktree, projectConfig)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return true, cmd.Run()
}
