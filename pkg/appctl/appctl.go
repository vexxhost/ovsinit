package appctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/cenkalti/rpc2"
	"github.com/cenkalti/rpc2/jsonrpc"
	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/procfs"
)

const (
	RUN_DIR = "/run/openvswitch"
)

var ErrNoPidFile = errors.New("pid file does not exist")

type Client struct {
	*rpc2.Client
}

func NewClient(conn io.ReadWriteCloser) *Client {
	client := rpc2.NewClientWithCodec(jsonrpc.NewJSONCodec(conn))
	client.SetBlocking(true)
	go client.Run()

	return &Client{
		Client: client,
	}
}

func (c *Client) Close() error {
	return c.Client.Close()
}

func Dial(network, address string) (*Client, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	return NewClient(conn), nil
}

func DialBinary(binary string) (*Client, error) {
	path := fmt.Sprintf("%s/%s.pid", RUN_DIR, binary)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoPidFile
		}
		return nil, fmt.Errorf("failed to read pid file %s: %w", path, err)
	}

	var pid int
	_, err = fmt.Sscanf(string(bytes), "%d", &pid)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pid from %s: %w", path, err)
	}

	path = fmt.Sprintf("%s/%s.%d.ctl", RUN_DIR, binary, pid)
	return Dial("unix", path)
}

func Cleanup(binary string) error {
	pidPath := fmt.Sprintf("%s/%s.pid", RUN_DIR, binary)
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove pid file %s: %w", pidPath, err)
	}

	socketPath := fmt.Sprintf("%s/%s.*.ctl", RUN_DIR, binary)
	matches, err := filepath.Glob(socketPath)
	if err != nil {
		return fmt.Errorf("failed to glob socket files %s: %w", socketPath, err)
	}

	for _, match := range matches {
		if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove socket file %s: %w", match, err)
		}
	}

	return nil
}

func (c *Client) Exit(ctx context.Context, binary string) error {
	pidPath := fmt.Sprintf("%s/%s.pid", RUN_DIR, binary)
	ctlPattern := fmt.Sprintf("%s/%s.*.ctl", RUN_DIR, binary)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	err = watcher.Add(RUN_DIR)
	if err != nil {
		return fmt.Errorf("failed to watch directory %s: %w", RUN_DIR, err)
	}

	err = c.CallWithContext(ctx, "exit", []string{}, nil)
	if err != nil {
		return fmt.Errorf("failed to send exit command: %w", err)
	}

	timeout := time.After(30 * time.Second)

	pidRemoved := false
	ctlRemoved := false

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher channel closed")
			}
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				if event.Name == pidPath {
					pidRemoved = true
					slog.Info("pid file removed", "pid_file", pidPath)
				} else if matched, _ := filepath.Match(ctlPattern, event.Name); matched {
					ctlRemoved = true
					slog.Info("ctl file removed", "ctl_file", event.Name)
				}

				if pidRemoved && ctlRemoved {
					slog.Info("files removed, waiting for hugepages to be available")

					// Get initial hugepages state
					fs, err := procfs.NewDefaultFS()
					if err != nil {
						slog.Warn("cannot access procfs, skipping hugepages check", "error", err)
						return nil
					}

					// Poll until hugepages are available
					ticker := time.NewTicker(50 * time.Millisecond)
					defer ticker.Stop()
					hugepagesTimeout := time.After(5 * time.Second)

					for {
						select {
						case <-ticker.C:
							memInfo, err := fs.Meminfo()
							if err != nil {
								slog.Warn("cannot read meminfo, assuming process exited", "error", err)
								return nil
							}

							// Check if there are free hugepages
							if memInfo.HugePagesFree != nil && *memInfo.HugePagesFree > 0 {
								slog.Info("hugepages available, process fully exited",
									"hugepages_free", *memInfo.HugePagesFree)
								return nil
							}

							// Also check if no hugepages are configured
							if memInfo.HugePagesTotal != nil && *memInfo.HugePagesTotal == 0 {
								slog.Info("no hugepages configured, process fully exited")
								return nil
							}

							slog.Debug("waiting for hugepages to be freed",
								"hugepages_total", memInfo.HugePagesTotal,
								"hugepages_free", memInfo.HugePagesFree)

						case <-hugepagesTimeout:
							slog.Warn("timeout waiting for hugepages, proceeding anyway")
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("watcher error channel closed")
			}
			return fmt.Errorf("watcher error: %w", err)
		case <-timeout:
			return fmt.Errorf("timeout waiting for process to exit (pid_removed=%v, ctl_removed=%v)", pidRemoved, ctlRemoved)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
