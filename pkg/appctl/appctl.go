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
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher channel closed")
			}
			if event.Name == pidPath && event.Op&fsnotify.Remove == fsnotify.Remove {
				slog.Info("process exited, pid file removed", "pid_file", pidPath)
				return nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("watcher error channel closed")
			}
			return fmt.Errorf("watcher error: %w", err)
		case <-timeout:
			return fmt.Errorf("timeout waiting for process to exit, pid file: %s", pidPath)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
