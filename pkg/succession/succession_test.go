package succession

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createMarker(t *testing.T, dir, podName string) *Marker {
	t.Helper()

	dbPath := filepath.Join(dir, "test.db")

	marker, err := New(dbPath, podName)
	require.NoError(t, err)

	return marker
}

func TestNoExistingDB(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pod1 := createMarker(t, dir, "pod-1")
	defer func() {
		if err := pod1.Close(); err != nil {
			t.Errorf("failed to close pod1: %v", err)
		}
	}()

	shouldProceed, isReplaced, err := pod1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	err = pod1.Claim(ctx)
	require.NoError(t, err)

	owner, err := pod1.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-1", owner)
}

func TestNoExistingPod(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pod1 := createMarker(t, dir, "pod-1")
	defer func() {
		if err := pod1.Close(); err != nil {
			t.Errorf("failed to close pod1: %v", err)
		}
	}()

	err := pod1.Claim(ctx)
	require.NoError(t, err)
	if err := pod1.Close(); err != nil {
		t.Errorf("failed to close pod1: %v", err)
	}

	pod2 := createMarker(t, dir, "pod-2")
	defer func() {
		if err := pod2.Close(); err != nil {
			t.Errorf("failed to close pod2: %v", err)
		}
	}()

	shouldProceed, isReplaced, err := pod2.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	err = pod2.Claim(ctx)
	require.NoError(t, err)

	owner, err := pod2.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-2", owner)
}

func TestExistingPodWithNewOneStarting(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pod1 := createMarker(t, dir, "pod-1")
	defer func() {
		if err := pod1.Close(); err != nil {
			t.Errorf("failed to close pod1: %v", err)
		}
	}()

	err := pod1.Claim(ctx)
	require.NoError(t, err)

	shouldProceed, isReplaced, err := pod1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	pod2 := createMarker(t, dir, "pod-2")
	defer func() {
		if err := pod2.Close(); err != nil {
			t.Errorf("failed to close pod2: %v", err)
		}
	}()

	shouldProceed, isReplaced, err = pod2.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	err = pod2.Claim(ctx)
	require.NoError(t, err)

	shouldProceed, isReplaced, err = pod1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.False(t, shouldProceed)
	assert.True(t, isReplaced)
}

func TestExistingPodRestart(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pod1 := createMarker(t, dir, "pod-1")
	defer func() {
		if err := pod1.Close(); err != nil {
			t.Errorf("failed to close pod1: %v", err)
		}
	}()

	err := pod1.Claim(ctx)
	require.NoError(t, err)
	if err := pod1.Close(); err != nil {
		t.Errorf("failed to close pod1: %v", err)
	}

	pod1 = createMarker(t, dir, "pod-1")
	defer func() {
		if err := pod1.Close(); err != nil {
			t.Errorf("failed to close pod1: %v", err)
		}
	}()

	shouldProceed, isReplaced, err := pod1.CheckSuccession(ctx)
	require.NoError(t, err)
	assert.True(t, shouldProceed)
	assert.False(t, isReplaced)

	owner, err := pod1.CurrentOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pod-1", owner)

	err = pod1.Claim(ctx)
	require.NoError(t, err)

	history, err := pod1.GetHistory(ctx)
	require.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, "pod-1", history[0].Owner) // Most recent
	assert.Equal(t, "pod-1", history[1].Owner) // Previous
}

func TestRotationWithLimit(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pod0 := createMarker(t, dir, "pod-0")
	defer func() {
		if err := pod0.Close(); err != nil {
			t.Errorf("failed to close pod0: %v", err)
		}
	}()

	totalEntries := MAX_HISTORY + 5
	for i := 0; i < totalEntries; i++ {
		pod := createMarker(t, dir, fmt.Sprintf("pod-%d", i))

		err := pod.Claim(ctx)
		require.NoError(t, err)

		if err := pod.Close(); err != nil {
			t.Errorf("failed to close pod: %v", err)
		}
	}

	pod := createMarker(t, dir, "pod-final")
	defer func() {
		if err := pod.Close(); err != nil {
			t.Errorf("failed to close pod: %v", err)
		}
	}()

	history, err := pod.GetHistory(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(history), MAX_HISTORY, "History should be trimmed to MAX_HISTORY entries")

	if len(history) > 0 {
		assert.Equal(t, fmt.Sprintf("pod-%d", totalEntries-1), history[0].Owner)
	}

	err = pod.Claim(ctx)
	require.NoError(t, err)

	history, err = pod.GetHistory(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(history), MAX_HISTORY, "History should still be trimmed after adding new entry")
	assert.Equal(t, "pod-final", history[0].Owner, "Most recent entry should be pod-final")
}

func TestMultiplePodRotations(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	pods := []string{"pod-a", "pod-b", "pod-c", "pod-a", "pod-b"}
	for i, podName := range pods {
		marker := createMarker(t, dir, podName)
		defer func() {
			if err := marker.Close(); err != nil {
				t.Errorf("failed to close marker: %v", err)
			}
		}()

		shouldProceed, isReplaced, err := marker.CheckSuccession(ctx)
		require.NoError(t, err)

		if i == 0 {
			// First pod should proceed
			assert.True(t, shouldProceed)
			assert.False(t, isReplaced)
		} else if podName == "pod-a" && i == 3 {
			assert.False(t, shouldProceed)
			assert.True(t, isReplaced)
		} else if podName == "pod-b" && i == 4 {
			assert.False(t, shouldProceed)
			assert.True(t, isReplaced)
		} else {
			assert.True(t, shouldProceed)
			assert.False(t, isReplaced)
		}

		if shouldProceed {
			err = marker.Claim(ctx)
			require.NoError(t, err)
		}

		if err := marker.Close(); err != nil {
			t.Errorf("failed to close marker: %v", err)
		}
	}

	marker := createMarker(t, dir, "pod-final")
	defer func() {
		if err := marker.Close(); err != nil {
			t.Errorf("failed to close marker: %v", err)
		}
	}()

	history, err := marker.GetHistory(ctx)
	require.NoError(t, err)

	assert.Equal(t, 3, len(history))
	assert.Equal(t, "pod-c", history[0].Owner)
	assert.Equal(t, "pod-b", history[1].Owner)
	assert.Equal(t, "pod-a", history[2].Owner)
}
