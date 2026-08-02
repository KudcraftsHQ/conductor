package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "state"))
	s.SetClock(newTestClock().Now)
	return s
}

func TestStoreRoundTripsATask(t *testing.T) {
	s := newTestStore(t)
	in := &Task{ID: "demo-1", Project: "demo", Gate: GateUndecided,
		Origin: Origin{RequestID: "msg-1"}, Tests: TestsUnknown}
	if err := s.Create(in); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A second handle over the same files is what a restarted process gets.
	reopened := NewStore(filepath.Dir(s.Path()))
	got, err := reopened.Get("demo-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Project != "demo" || got.Origin.RequestID != "msg-1" {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created time was not stamped")
	}
}

func TestStoreRefusesTwoTasksForOneMessage(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(&Task{ID: "a", Origin: Origin{RequestID: "msg-1"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := s.Create(&Task{ID: "b", Origin: Origin{RequestID: "msg-1"}})
	if err == nil {
		t.Fatal("a second task for the same originating message was accepted")
	}
}

func TestGetByRequestFindsTheTask(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(&Task{ID: "a", Origin: Origin{RequestID: "msg-1"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetByRequest("msg-1")
	if err != nil || got.ID != "a" {
		t.Fatalf("GetByRequest = %v, %v", got, err)
	}
	if _, err := s.GetByRequest("msg-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for an unknown message, got %v", err)
	}
	if _, err := s.GetByRequest(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for an empty message id, got %v", err)
	}
}

// A returned task is a copy. A caller that mutates it must not be able to
// change stored state behind the lock's back.
func TestReturnedTasksAreCopies(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(&Task{ID: "a", Origin: Origin{RequestID: "m"},
		Progress: []ProgressEvent{{Kind: "launched"}}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.Get("a")
	got.State = StateCompleted
	got.Progress[0].Kind = "tampered"

	fresh, _ := s.Get("a")
	if fresh.State == StateCompleted || fresh.Progress[0].Kind == "tampered" {
		t.Fatal("mutating a returned task changed stored state")
	}
}

// Updates are read-modify-write under a cross-process lock, so concurrent
// writers cannot lose each other's changes.
func TestConcurrentUpdatesDoNotLoseWrites(t *testing.T) {
	s := newTestStore(t)
	if err := s.Create(&Task{ID: "a", Origin: Origin{RequestID: "m"}}); err != nil {
		t.Fatalf("create: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update("a", func(t *Task) error {
				t.LaunchAttempts++
				return nil
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	got, _ := s.Get("a")
	if got.LaunchAttempts != writers {
		t.Fatalf("counter is %d after %d concurrent increments", got.LaunchAttempts, writers)
	}
}

// A lock left behind by a process that died must not wedge every future
// dispatch.
func TestStaleLockIsBroken(t *testing.T) {
	s := newTestStore(t)
	clock := newTestClock()
	s.SetClock(clock.Now)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lockPath := filepath.Join(filepath.Dir(s.Path()), "tasks.lock")
	if err := os.WriteFile(lockPath, []byte("999999\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := s.Create(&Task{ID: "a", Origin: Origin{RequestID: "m"}}); err != nil {
		t.Fatalf("a stale lock blocked a write: %v", err)
	}
}

func TestCorruptStoreIsReportedNotIgnored(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Get("a"); err == nil {
		t.Fatal("a corrupt store was read as empty; that would silently relaunch every live task")
	}
}

func TestNewerStoreVersionIsRefused(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte(`{"version":99,"tasks":{}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("a store written by a newer conductor should not be downgraded")
	}
}

func TestActiveExcludesFinishedTasks(t *testing.T) {
	s := newTestStore(t)
	for id, state := range map[string]TaskState{
		"live": StateWorking, "done": StateCompleted,
		"lost": StateAgentLost, "failed": StateFailed,
		"waiting": StateAwaitingReadbackDecision,
	} {
		if err := s.Create(&Task{ID: id, State: state, Origin: Origin{RequestID: id}}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	active, err := s.Active()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active returned %d tasks, want 2 (working and awaiting a decision)", len(active))
	}
}

// The progress log is bounded: a day-long agent must not grow the record
// without limit.
func TestProgressLogIsBounded(t *testing.T) {
	task := &Task{ID: "a"}
	for i := 0; i < maxProgressEvents*3; i++ {
		task.appendProgress(ProgressEvent{Kind: "status", Detail: "tick"})
	}
	if len(task.Progress) != maxProgressEvents {
		t.Fatalf("progress log holds %d events, want %d", len(task.Progress), maxProgressEvents)
	}
}

func TestUpdateOnAnUnknownTaskIsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Update("nope", func(*Task) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
