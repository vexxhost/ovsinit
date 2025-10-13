package succession

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoExistingDB(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create first marker - should become owner immediately
	marker1, err := New(dbPath, "pod-1")
	require.NoError(t, err)
	defer marker1.Close()

	ctx := t.Context()

	// Check succession - should proceed as first pod
	shouldProceed, isReplaced, err := marker1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	// Claim ownership
	err = marker1.Claim(ctx)
	require.NoError(t, err)

	// Verify current owner
	owner, err := marker1.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-1", owner)
}

func TestNoExistingPod(t *testing.T) {
	ctx := t.Context()

	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and claim with first pod
	marker1, err := New(dbPath, "pod-1")
	require.NoError(t, err)
	defer marker1.Close()

	err = marker1.Claim(ctx)
	require.NoError(t, err)
	marker1.Close()

	// Simulate pod-1 is gone, new pod-2 starts
	marker2, err := New(dbPath, "pod-2")
	require.NoError(t, err)
	defer marker2.Close()

	// pod-2 checks succession - should proceed since pod-1 is gone
	shouldProceed, isReplaced, err := marker2.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed) // New pod should proceed
	assert.False(t, isReplaced)    // It's not a replacement, it's new

	// pod-2 claims ownership
	err = marker2.Claim(ctx)
	require.NoError(t, err)

	// Verify current owner is pod-2
	owner, err := marker2.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-2", owner)
}

func TestExistingPodWithNewOneStarting(t *testing.T) {
	ctx := t.Context()

	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and claim with first pod
	marker1, err := New(dbPath, "pod-1")
	require.NoError(t, err)
	defer marker1.Close()

	err = marker1.Claim(ctx)
	require.NoError(t, err)

	// pod-1 is still running, check its succession
	shouldProceed, isReplaced, err := marker1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)  // Current owner should proceed
	assert.False(t, isReplaced)

	// Now pod-2 starts while pod-1 is still running
	marker2, err := New(dbPath, "pod-2")
	require.NoError(t, err)
	defer marker2.Close()

	// pod-2 checks succession - should proceed as new pod taking over
	shouldProceed, isReplaced, err = marker2.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)  // New pod should proceed
	assert.False(t, isReplaced)    // It's not replaced, it's new

	// pod-2 claims ownership
	err = marker2.Claim(ctx)
	require.NoError(t, err)

	// Now pod-1 checks again - should not proceed (replaced)
	shouldProceed, isReplaced, err = marker1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.False(t, shouldProceed) // Old pod should not proceed
	assert.True(t, isReplaced)     // It has been replaced
}

func TestExistingPodRestart(t *testing.T) {
	ctx := t.Context()

	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and claim with first pod
	marker1, err := New(dbPath, "pod-1")
	require.NoError(t, err)
	defer marker1.Close()

	err = marker1.Claim(ctx)
	require.NoError(t, err)
	marker1.Close()

	// Simulate pod-1 restarts (same identity)
	marker1Restarted, err := New(dbPath, "pod-1")
	require.NoError(t, err)
	defer marker1Restarted.Close()

	// pod-1 after restart checks succession - should proceed as current owner
	shouldProceed, isReplaced, err := marker1Restarted.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)  // Same pod after restart should proceed
	assert.False(t, isReplaced)    // It's not replaced, same identity

	// Verify current owner is still pod-1
	owner, err := marker1Restarted.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-1", owner)

	// pod-1 can claim again (will add another entry)
	err = marker1Restarted.Claim(ctx)
	require.NoError(t, err)

	// Check history has two entries for pod-1
	history, err := marker1Restarted.GetHistory(ctx)
	require.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, "pod-1", history[0].Owner) // Most recent
	assert.Equal(t, "pod-1", history[1].Owner) // Previous
}

func TestRotationWithLimit(t *testing.T) {
	ctx := t.Context()

	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create initial marker
	marker, err := New(dbPath, "pod-0")
	require.NoError(t, err)
	defer marker.Close()

	// Add MAX_HISTORY + 5 entries to test trimming
	totalEntries := MAX_HISTORY + 5
	for i := 0; i < totalEntries; i++ {
		// Create a new marker for each pod
		m, err := New(dbPath, fmt.Sprintf("pod-%d", i))
		require.NoError(t, err)

		err = m.Claim(ctx)
		require.NoError(t, err)

		m.Close()
	}

	// Reopen to check final state
	finalMarker, err := New(dbPath, "pod-final")
	require.NoError(t, err)
	defer finalMarker.Close()

	// Check history is trimmed to MAX_HISTORY
	history, err := finalMarker.GetHistory(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(history), MAX_HISTORY, "History should be trimmed to MAX_HISTORY entries")

	// Verify most recent entries are preserved
	if len(history) > 0 {
		// Most recent should be the last pod we added
		assert.Equal(t, fmt.Sprintf("pod-%d", totalEntries-1), history[0].Owner)
	}

	// Add one more entry to ensure trimming still works
	err = finalMarker.Claim(ctx)
	require.NoError(t, err)

	history, err = finalMarker.GetHistory(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(history), MAX_HISTORY, "History should still be trimmed after adding new entry")
	assert.Equal(t, "pod-final", history[0].Owner, "Most recent entry should be pod-final")
}

func TestMultiplePodRotations(t *testing.T) {
	ctx := t.Context()

	// Create a temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Simulate a series of pod rotations
	pods := []string{"pod-a", "pod-b", "pod-c", "pod-a", "pod-b"}

	for i, podName := range pods {
		// Create marker for each pod
		marker, err := New(dbPath, podName)
		require.NoError(t, err)

		shouldProceed, isReplaced, err := marker.CheckSuccession(ctx)
		require.NoError(t, err)

		if i == 0 {
			// First pod should proceed
			assert.True(t, shouldProceed)
			assert.False(t, isReplaced)
		} else if podName == "pod-a" && i == 3 {
			// pod-a coming back should not proceed (was replaced)
			assert.False(t, shouldProceed)
			assert.True(t, isReplaced)
		} else if podName == "pod-b" && i == 4 {
			// pod-b coming back should not proceed (was replaced)
			assert.False(t, shouldProceed)
			assert.True(t, isReplaced)
		} else {
			// New pods should proceed
			assert.True(t, shouldProceed)
			assert.False(t, isReplaced)
		}

		// Only claim if should proceed
		if shouldProceed {
			err = marker.Claim(ctx)
			require.NoError(t, err)
		}

		marker.Close()
	}

	// Verify final history
	finalMarker, err := New(dbPath, "pod-final")
	require.NoError(t, err)
	defer finalMarker.Close()

	history, err := finalMarker.GetHistory(ctx)
	require.NoError(t, err)

	// Should have pod-a, pod-b, pod-c (pod-a and pod-b rotations didn't claim)
	assert.Equal(t, 3, len(history))
	assert.Equal(t, "pod-c", history[0].Owner)
	assert.Equal(t, "pod-b", history[1].Owner)
	assert.Equal(t, "pod-a", history[2].Owner)
}