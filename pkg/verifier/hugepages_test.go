package verifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/procfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createMeminfo(t *testing.T, dir string, hugePagesTotal, hugePagesFree uint64) {
	t.Helper()

	content := fmt.Sprintf(`HugePages_Total:    %d
HugePages_Free:     %d
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB`, hugePagesTotal, hugePagesFree)

	meminfo := filepath.Join(dir, "meminfo")
	err := os.WriteFile(meminfo, []byte(content), 0644)
	require.NoError(t, err)
}

func createTestFS(t *testing.T, hugePagesTotal, hugePagesFree uint64) (*procfs.FS, string) {
	t.Helper()

	tempDir := t.TempDir()
	createMeminfo(t, tempDir, hugePagesTotal, hugePagesFree)

	fs, err := procfs.NewFS(tempDir)
	require.NoError(t, err)

	return &fs, tempDir
}

func TestHugePagesVerifier_NoHugepagesConfigured(t *testing.T) {
	fs, _ := createTestFS(t, 0, 0)
	verifier := HugePagesWithFS(fs)

	err := verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestHugePagesVerifier_HugepagesAvailable(t *testing.T) {
	fs, _ := createTestFS(t, 512, 256)
	verifier := HugePagesWithFS(fs)

	err := verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestHugePagesVerifier_WaitForHugepages(t *testing.T) {
	fs, tempDir := createTestFS(t, 10, 0)
	verifier := HugePagesWithFS(fs)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		createMeminfo(t, tempDir, 10, 5)
	}()

	err := verifier.Verify(ctx)
	assert.NoError(t, err)
}

func TestHugePagesVerifier_Timeout(t *testing.T) {
	fs, _ := createTestFS(t, 512, 0)
	verifier := HugePagesWithFS(fs)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := verifier.Verify(ctx)
	assert.NoError(t, err)
}

func TestHugePagesVerifier_ContextCancellation(t *testing.T) {
	fs, _ := createTestFS(t, 512, 0)
	verifier := HugePagesWithFS(fs)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := verifier.Verify(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestHugePagesVerifier_MissingMeminfo(t *testing.T) {
	fs, err := procfs.NewFS(t.TempDir())
	require.NoError(t, err)

	verifier := HugePagesWithFS(&fs)

	err = verifier.Verify(t.Context())
	assert.NoError(t, err)
}

func TestHugePagesVerifier_NilFS(t *testing.T) {
	verifier := HugePagesWithFS(nil)

	err := verifier.Verify(t.Context())
	assert.NoError(t, err)
}
