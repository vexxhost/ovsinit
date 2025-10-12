// Package succession provides a file-based succession tracking mechanism
// that maintains a history of all pods that have owned the resource.
package succession

import (
	"context"
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	MAX_HISTORY = 25
)

// HistoryEntry represents one entry in the succession history
type HistoryEntry struct {
	ID    uint   `gorm:"primarykey"`
	Owner string `gorm:"index;not null"`
}

func (h *HistoryEntry) AfterCreate(tx *gorm.DB) (err error) {
	ctx := tx.Statement.Context

	count, err := gorm.G[HistoryEntry](tx).Count(ctx, "id")
	if err != nil {
		return fmt.Errorf("failed to count entries: %w", err)
	}

	if count > MAX_HISTORY {
		subquery := tx.Model(&HistoryEntry{}).Select("id").Order("id DESC").Limit(MAX_HISTORY)

		if _, err := gorm.G[HistoryEntry](tx).Where("id NOT IN (?)", subquery).Delete(ctx); err != nil {
			return fmt.Errorf("failed to trim old entries: %w", err)
		}
	}

	return nil
}

// Marker tracks succession using a history of all owners
type Marker struct {
	db       *gorm.DB
	identity string
}

// New creates a new succession marker with history tracking
func New(path, identity string) (*Marker, error) {
	// Open SQLite database with GORM
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // Disable logging
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.AutoMigrate(&HistoryEntry{}); err != nil {
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	return &Marker{
		db:       db,
		identity: identity,
	}, nil
}

func (m *Marker) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}

// CheckSuccession determines if this pod should proceed
// Returns: shouldProceed, isReplaced, error
func (m *Marker) CheckSuccession(ctx context.Context) (bool, bool, error) {
	// Get the current owner (most recent entry)
	currentOwner, err := m.CurrentOwner(ctx)
	if err != nil {
		return false, false, err
	}

	// We're the current owner (or first) - proceed
	if currentOwner == "" || currentOwner == m.identity {
		return true, false, nil
	}

	// Someone else is current owner
	// Check if we ever owned before (to determine if replaced)
	var wasOwner bool
	err = m.db.WithContext(ctx).
		Model(&HistoryEntry{}).
		Select("COUNT(*) > 0").
		Where("owner = ?", m.identity).
		Find(&wasOwner).Error

	if err != nil {
		return false, false, fmt.Errorf("failed to check history: %w", err)
	}

	// If we're not current AND we were an owner before = we got replaced
	// If we're not current AND we were never an owner = we're new and taking over
	// In both cases: new pods proceed, replaced pods don't
	return !wasOwner, wasOwner, nil
}

func (m *Marker) Claim(ctx context.Context) error {
	return gorm.G[HistoryEntry](m.db).Create(ctx, &HistoryEntry{Owner: m.identity})
}

func (m *Marker) CurrentOwner(ctx context.Context) (string, error) {
	entry, err := gorm.G[HistoryEntry](m.db).Order("id DESC").First(ctx)
	switch {
	case err == gorm.ErrRecordNotFound:
		return "", nil
	case err != nil:
		return "", fmt.Errorf("failed to get current owner: %w", err)
	}

	return entry.Owner, nil
}

func (m *Marker) GetHistory(ctx context.Context) ([]HistoryEntry, error) {
	return gorm.G[HistoryEntry](m.db).Order("id DESC").Find(ctx)
}
