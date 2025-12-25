package workspace

import (
	"sync"

	"github.com/hammashamzah/conductor/internal/config"
)

// WorktreeJob represents a worktree creation job
type WorktreeJob struct {
	ProjectName  string
	WorktreeName string
	Worktree     *config.Worktree
	Config       *config.Config
	Manager      *Manager
	OnComplete   func(success bool, err error)
}

// WorktreeQueue serializes worktree creation to avoid git lock conflicts
type WorktreeQueue struct {
	mu       sync.Mutex
	jobs     []*WorktreeJob
	running  bool
	jobsDone chan struct{}
}

var (
	globalQueue     *WorktreeQueue
	globalQueueOnce sync.Once
)

// GetWorktreeQueue returns the global worktree queue singleton
func GetWorktreeQueue() *WorktreeQueue {
	globalQueueOnce.Do(func() {
		globalQueue = &WorktreeQueue{
			jobs:     make([]*WorktreeJob, 0),
			jobsDone: make(chan struct{}, 1),
		}
	})
	return globalQueue
}

// Enqueue adds a worktree creation job to the queue
func (q *WorktreeQueue) Enqueue(job *WorktreeJob) {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	shouldStart := !q.running
	q.running = true
	q.mu.Unlock()

	if shouldStart {
		go q.processQueue()
	}
}

// processQueue processes jobs sequentially
func (q *WorktreeQueue) processQueue() {
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.running = false
			q.mu.Unlock()
			// Signal that queue is empty
			select {
			case q.jobsDone <- struct{}{}:
			default:
			}
			return
		}

		// Dequeue first job
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()

		// Process job synchronously
		q.processJob(job)
	}
}

// processJob executes a single worktree creation job
func (q *WorktreeQueue) processJob(job *WorktreeJob) {
	project, ok := job.Config.GetProject(job.ProjectName)
	if !ok {
		if job.OnComplete != nil {
			job.OnComplete(false, nil)
		}
		return
	}

	worktree := job.Worktree

	// Create git worktree synchronously
	var createErr error
	if GitBranchExists(project.Path, worktree.Branch) {
		createErr = GitWorktreeAddExisting(project.Path, worktree.Path, worktree.Branch)
	} else {
		createErr = GitWorktreeAdd(project.Path, worktree.Path, worktree.Branch)
	}

	if createErr != nil {
		worktree.SetupStatus = config.SetupStatusFailed
		_ = config.Save(job.Config)
		if job.OnComplete != nil {
			job.OnComplete(false, createErr)
		}
		return
	}

	// Git worktree created successfully, now run setup
	worktree.SetupStatus = config.SetupStatusRunning
	_ = config.Save(job.Config)

	// Run setup asynchronously (setup scripts can run in parallel)
	_ = job.Manager.RunSetupAsync(job.ProjectName, job.WorktreeName, func(setupSuccess bool, setupErr error) {
		if setupSuccess {
			worktree.SetupStatus = config.SetupStatusDone
		} else {
			worktree.SetupStatus = config.SetupStatusFailed
		}
		_ = config.Save(job.Config)

		if job.OnComplete != nil {
			job.OnComplete(setupSuccess, setupErr)
		}
	})
}

// QueueSize returns the current number of jobs in the queue
func (q *WorktreeQueue) QueueSize() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// IsRunning returns whether the queue is currently processing jobs
func (q *WorktreeQueue) IsRunning() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running
}
