package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hammashamzah/conductor/internal/codingagent"
	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/mux"
	"github.com/hammashamzah/conductor/internal/ready"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/hammashamzah/conductor/internal/t3"
	"github.com/hammashamzah/conductor/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	adoptPath        string
	adoptRepo        string
	adoptName        string
	adoptPorts       int
	adoptBind        bool
	adoptReprovision bool
)

// adoptCmd provisions a worktree directory somebody else created.
//
// T3 Code creates worktrees itself — from the composer, or for a pull request —
// and then runs the project's runOnWorktreeCreate script inside the new tree.
// That script is this command: T3 owns the git worktree, conductor owns
// everything around it (ports, database, setup, dev server).
//
// It is also how a worktree that predates the arrangement joins it, so it can
// be run by hand over trees conductor created the old way.
var adoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Provision a worktree directory that already exists",
	Long: `Registers an existing worktree directory with conductor and provisions it:
allocates ports, clones the dev database, runs the project setup script and
starts the dev server.

Intended to be run by T3 Code's runOnWorktreeCreate hook:

  conductor adopt --path "$T3CODE_WORKTREE_PATH" --repo "$T3CODE_PROJECT_ROOT"

Safe to run more than once on the same directory. Under T3 orchestration V2 the
hook runs on every thread launch — a new thread in an existing worktree, a
thread at the project root, a thread conductor itself launched — not only when
T3 creates a worktree. So an already-registered worktree is never provisioned
again here unless its last setup failed or stalled:

  repository root              nothing to do
  newly registered             provisioned
  being provisioned elsewhere  bound only
  hibernated                   bound only — the T3 watcher wakes it
  set up                       bound only
  setup failed or stalled      provisioned again

--reprovision forces a fresh provision (drops and re-clones the database);
--bind-only never provisions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		worktreePath, err := resolveAdoptPath(adoptPath)
		if err != nil {
			return err
		}
		repoPath, err := resolveAdoptRepo(adoptRepo, worktreePath)
		if err != nil {
			return err
		}

		// T3 V2 runs this hook for a thread launched at the project root too,
		// with the root as the "worktree". The root is the user's checkout:
		// conductor never provisions it, and registering it as a worktree
		// would hand it ports and a cloned database.
		if pathsEqualClean(worktreePath, repoPath) {
			fmt.Println("Repository root — nothing to provision")
			return nil
		}

		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		projectName, _, ok := s.GetConfigSnapshot().GetProjectByPath(repoPath)
		if !ok {
			return fmt.Errorf("%s is not a conductor project — register it with 'conductor project add' first", repoPath)
		}

		// Readiness is not signalled by a file any more — it is derived from the
		// worktree's setup status in conductor.json, which every provisioning
		// path writes and which survives this process being killed. See
		// internal/ready. Any sentinel left by an older conductor is cleared so
		// that a `while [ -f ... ]` loop in an old agent context file does not
		// spin forever against a marker nothing will ever remove.
		_ = os.Remove(filepath.Join(worktreePath, config.ProvisioningSentinel))
		excludeConductorFiles(worktreePath)

		name, plan, err := claimAdopt(s, projectName, worktreePath, adoptName, adoptPorts)
		if err != nil {
			return err
		}
		if plan.root {
			fmt.Printf("%s/%s is the repository root — nothing to provision\n", projectName, name)
			return nil
		}
		if plan.registered {
			fmt.Printf("Registered %s/%s at %s\n", projectName, name, worktreePath)
		} else {
			fmt.Printf("Already registered as %s/%s — %s\n", projectName, name, plan.reason)
		}

		// Provisioning is slow — a database clone and a setup script — so it
		// runs against a snapshot with writes going through the store, rather
		// than holding the store's lock for its whole duration.
		manager := workspace.NewManagerWithStore(s.GetConfigSnapshot(), s)
		var provisionErr error
		if plan.provision {
			provisionErr = manager.Provision(projectName, name)
			if provisionErr != nil {
				// The registration stands either way: the tree exists, and
				// setup can be re-run by hand once whatever failed is fixed.
				fmt.Fprintf(os.Stderr, "setup failed: %v\n", provisionErr)
			}
		} else {
			fmt.Println("Binding only — skipping provisioning")
		}

		bindThreads(s, projectName, name, worktreePath)

		// Write the agent's context file. Under this arrangement T3 creates the
		// thread, so nothing else does — and it is the only channel conductor
		// has to tell an agent not to start a second dev server, not to
		// hardcode a port that changes on every wake, and to wait for the
		// database before running anything against it.
		if wt, err := manager.GetWorktree(projectName, name); err == nil && wt != nil {
			if err := codingagent.WriteContextFile(worktreePath, mux.T3AgentPrompt(projectName, wt.Branch, wt.Ports)); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write the agent context file: %v\n", err)
			}
		}

		if wt, err := manager.GetWorktree(projectName, name); err == nil && wt != nil {
			if len(wt.Ports) > 0 {
				fmt.Printf("Ports: %v\n", wt.Ports)
			}
			if wt.DatabaseName != "" {
				fmt.Printf("Database: %s\n", wt.DatabaseName)
			}
		}
		return provisionErr
	},
}

// resolveAdoptPath defaults to the working directory, so the command can be run
// from inside the worktree with no arguments at all.
func resolveAdoptPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
		path = cwd
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// resolveAdoptRepo falls back to asking git which repository the worktree
// belongs to, so --repo is optional when the caller did not pass one.
func resolveAdoptRepo(repo, worktreePath string) (string, error) {
	if strings.TrimSpace(repo) != "" {
		return filepath.Abs(repo)
	}
	root, err := workspace.GetRootPath(worktreePath)
	if err != nil {
		return "", fmt.Errorf("could not determine the repository for %s: %w (pass --repo)", worktreePath, err)
	}
	return root, nil
}

// adoptPlan is what one run of the hook will do, decided under the lock.
type adoptPlan struct {
	registered bool   // a new entry was created
	provision  bool   // run Provision
	root       bool   // the path is the project's root worktree
	reason     string // why, for an already-registered worktree
}

// claimAdopt registers the path if needed and decides, atomically, whether
// this run provisions it.
//
// The lock is cross-process: conductor.json is shared, and T3 launches run
// this hook concurrently — two worktrees created back to back, or a second
// thread launched into a worktree that is still provisioning. Without it both
// can read the port table before either has written to it and be handed the
// same ports, or both decide to provision and clone the database twice. The
// decision is written ("creating"/"running") and flushed before the lock is
// released, so the next run sees it.
func claimAdopt(s *store.Store, projectName, worktreePath, name string, ports int) (string, adoptPlan, error) {
	unlock, err := config.AcquireLock("adopt", 30*time.Second)
	if err != nil {
		return "", adoptPlan{}, err
	}
	defer unlock()

	if err := s.Reload(); err != nil {
		return "", adoptPlan{}, err
	}
	if existing, ok := findWorktreeByPath(s.GetConfigSnapshot(), projectName, worktreePath); ok {
		project, _ := s.GetConfigSnapshot().GetProject(projectName)
		plan := planExisting(project.Worktrees[existing], adoptBind, adoptReprovision)
		if plan.provision {
			if err := s.SetWorktreeStatus(projectName, existing, config.SetupStatusRunning); err != nil {
				return "", adoptPlan{}, err
			}
			if err := s.ForceSave(); err != nil {
				return "", adoptPlan{}, err
			}
		}
		return existing, plan, nil
	}

	branch, branchErr := workspace.GitCurrentBranch(worktreePath)
	if branchErr != nil {
		branch = ""
	}
	err = s.BatchMutate(func(cfg *config.Config) error {
		manager := workspace.NewManager(cfg)
		if name == "" {
			generated, nameErr := manager.NextWorktreeName(projectName)
			if nameErr != nil {
				return nameErr
			}
			name = generated
		}
		worktree, registerErr := manager.RegisterWorktree(projectName, name, branch, worktreePath, ports)
		if registerErr != nil {
			return registerErr
		}
		// Marks this as a tree conductor provisioned but did not create, which
		// is what makes it visible to the T3 watcher.
		worktree.T3Adopted = true
		// Registered and "creating" have to land in the same write. Readiness is
		// derived from this status, and a worktree that exists in conductor.json
		// with no status at all reads as one that predates status tracking —
		// which is to say, as ready. That gap is a handful of milliseconds wide
		// and it is exactly the moment T3 opens the first turn in.
		worktree.MarkSetup(config.SetupStatusCreating)
		return nil
	})
	if err != nil {
		return "", adoptPlan{}, err
	}
	if err := s.ForceSave(); err != nil {
		return "", adoptPlan{}, err
	}
	return name, adoptPlan{registered: true, provision: !adoptBind, reason: "newly registered"}, nil
}

// planExisting decides what the hook does with an already-registered
// worktree. It provisions only when nothing else will and the worktree is not
// already set up: provisioning drops and re-clones the database.
func planExisting(wt *config.Worktree, bindOnly, reprovision bool) adoptPlan {
	switch {
	case wt == nil:
		return adoptPlan{reason: "entry vanished"}
	case wt.IsRoot:
		return adoptPlan{root: true}
	case bindOnly:
		return adoptPlan{reason: "bind-only requested"}
	case wt.SetupStatus.InProgress() && !wt.SetupStalled(ready.StaleAfter):
		// Checked before --reprovision: two provisions at once is the one
		// outcome this guard exists to prevent, whoever asked.
		return adoptPlan{reason: fmt.Sprintf("already being provisioned (pid %d)", wt.SetupPID)}
	case reprovision:
		return adoptPlan{provision: true, reason: "re-provisioning as requested"}
	case wt.Hibernated:
		// The T3 watcher wakes a hibernated worktree as soon as it sees a live
		// thread on it. It is the one waker, so two never race.
		return adoptPlan{reason: "hibernated; the T3 watcher wakes it"}
	case wt.SetupStatus == config.SetupStatusFailed:
		return adoptPlan{provision: true, reason: "last setup failed — provisioning again"}
	case wt.SetupStatus.InProgress():
		return adoptPlan{provision: true, reason: "last setup stalled — provisioning again"}
	default:
		return adoptPlan{reason: "already set up"}
	}
}

// pathsEqualClean compares two absolute paths after cleaning.
func pathsEqualClean(a, b string) bool {
	return strings.TrimRight(filepath.Clean(a), string(filepath.Separator)) ==
		strings.TrimRight(filepath.Clean(b), string(filepath.Separator))
}

// excludeConductorFiles hides conductor's own droppings from git.
//
// .conductor-t3-thread lives in the worktree root and belongs to conductor, not
// to the repository, so it has no business in `git status` — and an agent
// tidying up before a commit will otherwise commit it. This goes in
// .git/info/exclude rather than .gitignore because the worktree's .gitignore is
// a tracked file of somebody else's project.
//
// Failure is not reported: an unwritable exclude file is untidy, not broken.
func excludeConductorFiles(worktreePath string) {
	gitDir, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--git-dir").Output()
	if err != nil {
		return
	}
	dir := strings.TrimSpace(string(gitDir))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktreePath, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}

	existing, _ := os.ReadFile(path)
	var missing []string
	for _, entry := range []string{t3.MarkerFileName, config.ProvisioningSentinel} {
		if !strings.Contains(string(existing), entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = file.WriteString("\n")
	}
	_, _ = file.WriteString("\n# conductor\n" + strings.Join(missing, "\n") + "\n")
}

// findWorktreeByPath looks a worktree up by where it is rather than what it is
// called, because adopt is handed a path and nothing else.
func findWorktreeByPath(cfg *config.Config, projectName, path string) (string, bool) {
	project, ok := cfg.GetProject(projectName)
	if !ok {
		return "", false
	}
	target := strings.TrimRight(filepath.Clean(path), string(filepath.Separator))
	for name, wt := range project.Worktrees {
		if wt == nil {
			continue
		}
		if strings.TrimRight(filepath.Clean(wt.Path), string(filepath.Separator)) == target {
			return name, true
		}
	}
	return "", false
}

// bindThreads records which T3 threads own this worktree.
//
// The hook environment carries the worktree path but not the thread id, so the
// binding is resolved from T3's own snapshot. It matters more than it looks: a
// deleted thread arrives as an id with no path attached and is gone from every
// snapshot by then, so this index is the only way conductor can later tell
// which worktree died with it.
func bindThreads(s *store.Store, projectName, worktreeName, worktreePath string) {
	client, err := t3.New()
	if err != nil {
		return // Not a T3 setup; adopt still works, there is just nothing to bind.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := client.Shell(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not reach T3 Code to bind threads: %v\n", err)
		return
	}

	ids := snapshot.ThreadIDsByWorktree(worktreePath)
	if len(ids) == 0 {
		return
	}
	if err := s.SetWorktreeThreads(projectName, worktreeName, ids); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record the bound threads: %v\n", err)
	}
	if err := t3.WriteMarker(worktreePath, ids); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not mark %s as T3-hosted: %v\n", worktreePath, err)
	}
	fmt.Printf("Bound to %d T3 thread(s)\n", len(ids))
}

func init() {
	adoptCmd.Flags().StringVar(&adoptPath, "path", "", "Worktree directory to adopt (defaults to the working directory)")
	adoptCmd.Flags().StringVar(&adoptRepo, "repo", "", "Project repository root (defaults to the worktree's own repository)")
	adoptCmd.Flags().StringVar(&adoptName, "name", "", "Worktree name to register under (defaults to a free city name)")
	adoptCmd.Flags().IntVarP(&adoptPorts, "ports", "p", 0, "Number of ports to allocate")
	adoptCmd.Flags().BoolVar(&adoptBind, "bind-only", false,
		"Record the T3 thread binding without provisioning (for a worktree that is already set up)")
	adoptCmd.Flags().BoolVar(&adoptReprovision, "reprovision", false,
		"Provision an already-registered worktree again (drops and re-clones its database)")
}
