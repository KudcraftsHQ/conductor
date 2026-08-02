package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// storeVersion guards the on-disk format. A record written by a newer Conductor
// is left alone rather than silently downgraded.
const storeVersion = 1

// ErrNotFound is returned for an unknown task id.
var ErrNotFound = errors.New("task not found")

// Store persists orchestration tasks so that a restarted bridge, a restarted
// Conductor, or a second process handling a duplicate Discord message all see
// the same truth.
//
// Every mutation takes a cross-process lock, re-reads the file, applies the
// change and writes atomically. That is slower than holding state in memory and
// it is the only version that survives the cases this contract cares about:
// two deliveries of the same message racing, and a crash between "worktree
// created" and "agent started".
type Store struct {
	path     string
	lockPath string
	now      func() time.Time
	// lockTimeout bounds how long an acquirer waits; lockStale bounds how long
	// a lock left by a dead process is honoured.
	lockTimeout time.Duration
	lockStale   time.Duration
}

type fileState struct {
	Version int              `json:"version"`
	Tasks   map[string]*Task `json:"tasks"`
}

// NewStore opens (without creating) the task store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{
		path:        filepath.Join(dir, "tasks.json"),
		lockPath:    filepath.Join(dir, "tasks.lock"),
		now:         time.Now,
		lockTimeout: 5 * time.Second,
		lockStale:   30 * time.Second,
	}
}

// SetClock overrides the clock, for tests.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Path is where the store lives, for diagnostics.
func (s *Store) Path() string { return s.path }

func (s *Store) load() (*fileState, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &fileState{Version: storeVersion, Tasks: map[string]*Task{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task store: %w", err)
	}
	var st fileState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("task store %s is corrupt: %w", s.path, err)
	}
	if st.Tasks == nil {
		st.Tasks = map[string]*Task{}
	}
	if st.Version > storeVersion {
		return nil, fmt.Errorf(
			"task store %s was written by a newer conductor (version %d); refusing to downgrade it",
			s.path, st.Version)
	}
	st.Version = storeVersion
	return &st, nil
}

func (s *Store) save(st *fileState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create task store directory: %w", err)
	}
	st.Version = storeVersion
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tasks-*.json")
	if err != nil {
		return fmt.Errorf("create temp task store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp task store: %w", err)
	}
	// Flushed before the rename so a crash cannot leave a renamed-but-empty
	// file where the previous good state used to be.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp task store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp task store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace task store: %w", err)
	}
	return nil
}

// lock takes the cross-process lock. A lock file older than lockStale is
// assumed to belong to a process that died holding it and is broken — the
// alternative is a single crash wedging every future dispatch.
//
// Waiting and staleness are measured against the wall clock, never the
// injectable one: the injectable clock exists so tests can control the
// *timestamps on records*, and a test that freezes it must not turn a bounded
// wait into an infinite one.
func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create task store directory: %w", err)
	}
	deadline := time.Now().Add(s.lockTimeout)
	for {
		f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(s.lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("acquire task store lock: %w", err)
		}
		if info, statErr := os.Stat(s.lockPath); statErr == nil {
			if time.Since(info.ModTime()) > s.lockStale {
				_ = os.Remove(s.lockPath)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("task store is locked by another process (%s)", s.lockPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Update applies mutate to the task under lock and persists the result.
// The task passed to mutate is the freshly-read stored copy, so a caller that
// has been holding a stale record cannot overwrite a concurrent change it never
// saw.
func (s *Store) Update(id string, mutate func(*Task) error) (*Task, error) {
	var out *Task
	err := s.withState(func(st *fileState) error {
		t, ok := st.Tasks[id]
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err := mutate(t); err != nil {
			return err
		}
		t.UpdatedAt = s.now()
		out = t.Clone()
		return nil
	})
	return out, err
}

// Create stores a task that must not already exist.
func (s *Store) Create(t *Task) error {
	return s.withState(func(st *fileState) error {
		if _, exists := st.Tasks[t.ID]; exists {
			return fmt.Errorf("task %s already exists", t.ID)
		}
		if other := findByRequest(st, t.Origin.RequestID); other != nil {
			return fmt.Errorf("request %s is already bound to task %s",
				t.Origin.RequestID, other.ID)
		}
		now := s.now()
		t.CreatedAt = now
		t.UpdatedAt = now
		st.Tasks[t.ID] = t.Clone()
		return nil
	})
}

// withState is the single place that reads, mutates and writes under lock.
func (s *Store) withState(fn func(*fileState) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.save(st)
}

// Get returns a copy of a stored task.
func (s *Store) Get(id string) (*Task, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	t, ok := st.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return t.Clone(), nil
}

// GetByRequest resolves the originating message id to its task, which is how a
// duplicate delivery finds the work it already started.
func (s *Store) GetByRequest(requestID string) (*Task, error) {
	if requestID == "" {
		return nil, fmt.Errorf("%w: empty request id", ErrNotFound)
	}
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	if t := findByRequest(st, requestID); t != nil {
		return t.Clone(), nil
	}
	return nil, fmt.Errorf("%w: request %s", ErrNotFound, requestID)
}

func findByRequest(st *fileState, requestID string) *Task {
	if requestID == "" {
		return nil
	}
	for _, t := range st.Tasks {
		if t.Origin.RequestID == requestID {
			return t
		}
	}
	return nil
}

// List returns every task, newest first.
func (s *Store) List() ([]*Task, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]*Task, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		out = append(out, t.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Active returns tasks that still expect agent progress — the work list for a
// monitor that has just restarted and needs to re-attach to whatever is
// running.
func (s *Store) Active() ([]*Task, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, t := range all {
		if !t.State.Terminal() {
			out = append(out, t)
		}
	}
	return out, nil
}
