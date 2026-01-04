// Package store provides a centralized state management layer for Conductor.
// All reads and writes to the config go through this store, ensuring
// consistent state and automatic persistence to conductor.json.
package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/hammashamzah/conductor/internal/config"
)

// SaveError represents an error that occurred during save
type SaveError struct {
	Err       error
	Timestamp time.Time
	Retries   int
}

// Store is the centralized state management for Conductor.
// All config reads and writes must go through this store.
type Store struct {
	mu     sync.RWMutex
	config *config.Config

	// Save queue management
	dirty      bool
	saveChan   chan struct{}
	closeChan  chan struct{}
	closedChan chan struct{}
	saveErr    *SaveError

	// Configuration
	debounceTime time.Duration
	maxRetries   int
	disableSave  bool // For testing - prevents saving to disk

	// Callbacks for notifications
	onSaveError func(err error)
}

// Option is a functional option for configuring the Store
type Option func(*Store)

// WithDebounceTime sets the debounce duration for saves
func WithDebounceTime(d time.Duration) Option {
	return func(s *Store) {
		s.debounceTime = d
	}
}

// WithMaxRetries sets the maximum number of save retries
func WithMaxRetries(n int) Option {
	return func(s *Store) {
		s.maxRetries = n
	}
}

// WithSaveErrorCallback sets a callback for save errors
func WithSaveErrorCallback(fn func(err error)) Option {
	return func(s *Store) {
		s.onSaveError = fn
	}
}

// WithDisableSave disables saving to disk (for testing)
func WithDisableSave() Option {
	return func(s *Store) {
		s.disableSave = true
	}
}

// New creates a new Store with the given config
func New(cfg *config.Config, opts ...Option) *Store {
	s := &Store{
		config:       cfg,
		saveChan:     make(chan struct{}, 1),
		closeChan:    make(chan struct{}),
		closedChan:   make(chan struct{}),
		debounceTime: 100 * time.Millisecond,
		maxRetries:   3,
	}

	for _, opt := range opts {
		opt(s)
	}

	go s.saveWorker()
	return s
}

// Load creates a new Store by loading config from disk
func Load(opts ...Option) (*Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return New(cfg, opts...), nil
}

// saveWorker runs in a background goroutine and handles debounced saves
func (s *Store) saveWorker() {
	defer close(s.closedChan)

	for {
		select {
		case <-s.saveChan:
			// Debounce: wait for more mutations to coalesce
			time.Sleep(s.debounceTime)
			s.performSave()

		case <-s.closeChan:
			// Flush any pending saves before closing
			s.performSave()
			return
		}
	}
}

// performSave executes the actual save with retry logic
func (s *Store) performSave() {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return
	}

	// Skip saving if disabled (for testing)
	if s.disableSave {
		s.dirty = false
		s.mu.Unlock()
		return
	}

	// Make a copy of config for saving (so we can release lock)
	cfgCopy := s.config
	s.mu.Unlock()

	var lastErr error
	for i := 0; i <= s.maxRetries; i++ {
		if err := config.Save(cfgCopy); err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond) // Exponential backoff
			continue
		}

		// Success
		s.mu.Lock()
		s.dirty = false
		s.saveErr = nil
		s.mu.Unlock()
		return
	}

	// All retries failed
	s.mu.Lock()
	s.saveErr = &SaveError{
		Err:       lastErr,
		Timestamp: time.Now(),
		Retries:   s.maxRetries,
	}
	s.mu.Unlock()

	if s.onSaveError != nil {
		s.onSaveError(lastErr)
	}
}

// markDirty marks the store as having unsaved changes and signals the save worker
func (s *Store) markDirty() {
	s.dirty = true
	// Non-blocking send to save channel
	select {
	case s.saveChan <- struct{}{}:
	default:
		// Already has a pending save signal
	}
}

// Close shuts down the store and flushes pending saves.
// Returns true if there were pending saves that were flushed.
func (s *Store) Close() (hadPending bool, err error) {
	s.mu.RLock()
	hadPending = s.dirty
	s.mu.RUnlock()

	close(s.closeChan)
	<-s.closedChan // Wait for save worker to finish

	s.mu.RLock()
	if s.saveErr != nil {
		err = s.saveErr.Err
	}
	s.mu.RUnlock()

	return hadPending, err
}

// Reload reloads the config from disk, discarding any unsaved changes
func (s *Store) Reload() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	s.mu.Lock()
	s.config = cfg
	s.dirty = false
	s.mu.Unlock()

	return nil
}

// HasPendingSaves returns true if there are unsaved changes
func (s *Store) HasPendingSaves() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

// LastSaveError returns the last save error, if any
func (s *Store) LastSaveError() *SaveError {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveErr
}

// ForceSave immediately saves the config, bypassing the debounce queue
func (s *Store) ForceSave() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := config.Save(s.config); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
