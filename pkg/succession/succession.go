// Package succession provides a file-based succession tracking mechanism
// that maintains a history of all pods that have owned the resource.
package succession

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/renameio/v2"
	"github.com/samber/lo"
)

// HistoryEntry represents one entry in the succession history
type HistoryEntry struct {
	Owner     string    `json:"owner"`
	Timestamp time.Time `json:"timestamp"`
}

// HistoryData is the complete succession history
type HistoryData struct {
	Current HistoryEntry   `json:"current"` // Most recent owner
	History []HistoryEntry `json:"history"` // All previous owners (including current)
	Version int            `json:"version"` // For detecting concurrent updates
}

// Marker tracks succession using a history of all owners
type Marker struct {
	path        string
	identity    string
	maxHistory  int           // Maximum history entries to keep
	lockTimeout time.Duration // How long to wait for file lock
	mu          sync.Mutex    // Local mutex for this process
}

// Option configures a Marker
type Option func(*Marker)

// WithMaxHistory sets the maximum number of history entries to keep
func WithMaxHistory(n int) Option {
	return func(m *Marker) {
		m.maxHistory = n
	}
}

// WithLockTimeout sets how long to wait for file lock
func WithLockTimeout(d time.Duration) Option {
	return func(m *Marker) {
		m.lockTimeout = d
	}
}

// New creates a new succession marker with history tracking
func New(path, identity string, opts ...Option) *Marker {
	m := &Marker{
		path:        path,
		identity:    identity,
		maxHistory:  100,             // Keep last 100 entries by default
		lockTimeout: 5 * time.Second, // Wait up to 5 seconds for lock
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// CheckSuccession determines what action this pod should take
// Returns: shouldProceed, isReplaced, error
func (m *Marker) CheckSuccession() (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Use file locking to ensure atomic read
	data, err := m.readWithLock()
	if err != nil {
		if os.IsNotExist(err) {
			// No history file, we're the first
			return true, false, nil
		}
		return false, false, err
	}

	// Check our position in the history
	_, position, _ := lo.FindIndexOf(data.History, func(entry HistoryEntry) bool {
		return entry.Owner == m.identity
	})

	switch position {
	case -1:
		// Not in history at all - we're a NEW pod in a rolling update
		// We should take over
		return true, false, nil

	case 0:
		// We're the current owner (top of the list)
		// This might be a restart of the current pod or a re-run
		return true, false, nil

	default:
		// We're in the history but not current
		// We've been replaced by a newer pod
		return false, true, nil
	}
}

// Claim adds this pod to the top of the succession history
func (m *Marker) Claim() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fileLock := flock.New(m.path)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), m.lockTimeout)
	defer cancel()

	// Try to acquire exclusive lock
	locked, err := fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to acquire write lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("timeout acquiring write lock")
	}
	defer fileLock.Unlock()

	// Open or create file
	file, err := os.OpenFile(m.path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open history file: %w", err)
	}
	defer file.Close()

	// Load existing data or create new
	var data HistoryData
	stat, err := file.Stat()
	if err == nil && stat.Size() > 0 {
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&data); err != nil {
			// File exists but is corrupted, start fresh
			data = HistoryData{History: make([]HistoryEntry, 0)}
		}
	} else {
		// New file
		data = HistoryData{History: make([]HistoryEntry, 0)}
	}

	// Create new entry
	newEntry := HistoryEntry{
		Owner:     m.identity,
		Timestamp: time.Now(),
	}

	// If we're already at the top, just update timestamp
	if len(data.History) > 0 && data.History[0].Owner == m.identity {
		data.History[0].Timestamp = newEntry.Timestamp
		data.Current = newEntry
	} else {
		// Add ourselves to the top of the history
		data.Current = newEntry

		// Filter out any existing entries for us and prepend new entry
		filteredHistory := lo.Filter(data.History, func(entry HistoryEntry, _ int) bool {
			return entry.Owner != m.identity
		})

		// Prepend new entry and trim to max history
		data.History = lo.Slice(append([]HistoryEntry{newEntry}, filteredHistory...), 0, m.maxHistory)
	}
	data.Version++

	// Write back atomically using renameio
	t, err := renameio.TempFile("", m.path)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer t.Cleanup()

	encoder := json.NewEncoder(t)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&data); err != nil {
		return fmt.Errorf("failed to write history: %w", err)
	}

	if err := t.CloseAtomicallyReplace(); err != nil {
		return fmt.Errorf("failed to update history file: %w", err)
	}

	return nil
}

// CurrentOwner returns the current owner (top of the history)
func (m *Marker) CurrentOwner() (string, error) {
	data, err := m.readWithLock()
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	return data.Current.Owner, nil
}

// GetHistory returns the full succession history
func (m *Marker) GetHistory() ([]HistoryEntry, error) {
	data, err := m.readWithLock()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return data.History, nil
}

// readWithLock reads the history file with a shared lock
func (m *Marker) readWithLock() (*HistoryData, error) {
	fileLock := flock.New(m.path)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), m.lockTimeout)
	defer cancel()

	// Try to acquire shared lock
	locked, err := fileLock.TryRLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire read lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("timeout acquiring read lock")
	}
	defer fileLock.Unlock()

	// Read the file
	file, err := os.Open(m.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var data HistoryData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse history: %w", err)
	}

	return &data, nil
}


// String implements fmt.Stringer
func (m *Marker) String() string {
	return fmt.Sprintf("Marker{path=%s, identity=%s}", m.path, m.identity)
}
