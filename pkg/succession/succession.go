// Package succession provides a file-based succession tracking mechanism
// that maintains a history of all pods that have owned the resource.
package succession

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
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
	position := m.findPosition(data, m.identity)

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

	return m.updateHistory(func(data *HistoryData) error {
		newEntry := HistoryEntry{
			Owner:     m.identity,
			Timestamp: time.Now(),
		}

		// If we're already at the top, just update timestamp
		if len(data.History) > 0 && data.History[0].Owner == m.identity {
			data.History[0].Timestamp = newEntry.Timestamp
			data.Current = newEntry
			return nil
		}

		// Add ourselves to the top of the history
		data.Current = newEntry

		// Prepend to history (most recent first)
		newHistory := make([]HistoryEntry, 0, len(data.History)+1)
		newHistory = append(newHistory, newEntry)

		// Add existing history, but skip any existing entries for us
		// (in case we're reclaiming after being in the middle)
		for _, entry := range data.History {
			if entry.Owner != m.identity {
				newHistory = append(newHistory, entry)
			}
		}

		// Trim history if it's too long
		if len(newHistory) > m.maxHistory {
			newHistory = newHistory[:m.maxHistory]
		}

		data.History = newHistory
		data.Version++

		return nil
	})
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

// Clear removes the succession history file
func (m *Marker) Clear() error {
	return os.Remove(m.path)
}

// findPosition returns the position of identity in the history
// Returns -1 if not found, 0 if current (top), 1+ for historical positions
func (m *Marker) findPosition(data *HistoryData, identity string) int {
	for i, entry := range data.History {
		if entry.Owner == identity {
			return i
		}
	}
	return -1
}

// readWithLock reads the history file with a shared lock
func (m *Marker) readWithLock() (*HistoryData, error) {
	// Open file for reading
	file, err := os.Open(m.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Acquire shared lock for reading
	if err := m.lockFile(file, false); err != nil {
		return nil, fmt.Errorf("failed to acquire read lock: %w", err)
	}
	defer m.unlockFile(file)

	// Read and parse
	var data HistoryData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse history: %w", err)
	}

	return &data, nil
}

// updateHistory updates the history file with an exclusive lock
func (m *Marker) updateHistory(updateFunc func(*HistoryData) error) error {
	// Open or create file
	file, err := os.OpenFile(m.path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open history file: %w", err)
	}
	defer file.Close()

	// Acquire exclusive lock for writing
	if err := m.lockFile(file, true); err != nil {
		return fmt.Errorf("failed to acquire write lock: %w", err)
	}
	defer m.unlockFile(file)

	// Read existing data
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

	// Apply the update
	if err := updateFunc(&data); err != nil {
		return err
	}

	// Write back atomically
	tmpPath := m.path + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write history: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, m.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to update history file: %w", err)
	}

	return nil
}

// lockFile acquires a file lock (exclusive or shared)
func (m *Marker) lockFile(file *os.File, exclusive bool) error {
	// Use flock for file locking
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}

	// Try to acquire lock with timeout
	deadline := time.Now().Add(m.lockTimeout)
	for {
		err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return nil
		}

		if err != syscall.EWOULDBLOCK {
			return err
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout acquiring lock")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// unlockFile releases a file lock
func (m *Marker) unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// String implements fmt.Stringer
func (m *Marker) String() string {
	return fmt.Sprintf("Marker{path=%s, identity=%s}", m.path, m.identity)
}
