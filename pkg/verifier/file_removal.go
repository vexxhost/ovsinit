package verifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type FileRemovalVerifier struct {
	pattern string
}

func FileRemoval(pattern string) *FileRemovalVerifier {
	return &FileRemovalVerifier{
		pattern: pattern,
	}
}

func (v *FileRemovalVerifier) String() string {
	return fmt.Sprintf("file_removal(%s)", v.pattern)
}

func (v *FileRemovalVerifier) Verify(ctx context.Context) error {
	// Check if files matching the pattern exist
	matches, err := filepath.Glob(v.pattern)
	if err != nil {
		return fmt.Errorf("failed to check pattern %s: %w", v.pattern, err)
	}

	if len(matches) == 0 {
		slog.Info(fmt.Sprintf("%s: no files found, already removed", v.String()))
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Warn("failed to close watcher", "error", err)
		}
	}()

	dir := filepath.Dir(v.pattern)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("failed to watch directory %s: %w", dir, err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher channel closed")
			}

			if event.Op&fsnotify.Remove == fsnotify.Remove {
				matches, _ := filepath.Match(v.pattern, event.Name)

				if matches {
					slog.Info(fmt.Sprintf("%s: file removed", v.String()),
						"file", event.Name)
					return nil
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("watcher error channel closed")
			}

			return fmt.Errorf("watcher error: %w", err)
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s: %w", v.String(), ctx.Err())
		}
	}
}
