package verifier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileRemovalVerifier(t *testing.T) {
	tempDir := t.TempDir()

	// Test exact path matching
	testFile := filepath.Join(tempDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	verifier := FileRemoval(testFile)
	go func() {
		time.Sleep(100 * time.Millisecond)
		err = os.Remove(testFile)
		require.NoError(t, err)
	}()

	err = verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestFileRemovalVerifierWithGlob(t *testing.T) {
	tempDir := t.TempDir()

	testFile := filepath.Join(tempDir, "test.12345.log")
	err := os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	pattern := filepath.Join(tempDir, "test.*.log")
	verifier := FileRemoval(pattern)
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := os.Remove(testFile)
		require.NoError(t, err)
	}()

	err = verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestFileRemovalVerifier_AlreadyRemoved(t *testing.T) {
	tempDir := t.TempDir()

	pattern := filepath.Join(tempDir, "nonexistent.txt")
	verifier := FileRemoval(pattern)

	err := verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestFileRemovalVerifier_AlreadyRemovedGlob(t *testing.T) {
	tempDir := t.TempDir()

	pattern := filepath.Join(tempDir, "*.nonexistent")
	verifier := FileRemoval(pattern)

	err := verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestFileRemovalVerifier_Timeout(t *testing.T) {
	tempDir := t.TempDir()

	testFile := filepath.Join(tempDir, "persistent.txt")
	err := os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	verifier := FileRemoval(testFile)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	err = verifier.Verify(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestFileRemovalVerifier_ContextCancellation(t *testing.T) {
	tempDir := t.TempDir()

	testFile := filepath.Join(tempDir, "persistent.txt")
	err := os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	verifier := FileRemoval(testFile)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err = verifier.Verify(ctx)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
