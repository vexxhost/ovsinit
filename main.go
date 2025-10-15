package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vexxhost/ovsinit/pkg/appctl"
	"github.com/vexxhost/ovsinit/pkg/succession" // Uses the history.go version
)

func main() {
	flag.Parse()

	cmdArgs := flag.Args()
	if len(cmdArgs) == 0 {
		slog.Error("usage: ovsinit [flags] -- <binary> <args...>")
		os.Exit(1)
	}

	binaryPath := cmdArgs[0]
	binary := filepath.Base(binaryPath)
	processArgs := cmdArgs[1:]

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("binary", binary)
	slog.SetDefault(logger)

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		slog.Error("POD_NAME environment variable must be set for succession tracking")
		os.Exit(1)
	}

	marker, err := succession.New(
		filepath.Join("/run/openvswitch", fmt.Sprintf(".%s.succession.db", binary)),
		podName,
	)
	if err != nil {
		slog.Error("failed to create succession marker", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := marker.Close(); err != nil {
			slog.Error("failed to close marker", "error", err)
		}
	}()

	shouldProceed, wasReplaced, err := marker.CheckSuccession(context.TODO())
	if err != nil {
		slog.Warn("failed to check succession", "error", err)
		shouldProceed = true
	}

	if wasReplaced {
		currentOwner, _ := marker.CurrentOwner(context.TODO())
		slog.Info("we've been replaced, exiting gracefully",
			"our_pod", podName,
			"current_owner", currentOwner)

		if history, err := marker.GetHistory(context.TODO()); err == nil && len(history) > 0 {
			slog.Debug("succession history",
				"entries", len(history),
				"latest", history[0].Owner)
		}

		os.Exit(0)
	}

	if !shouldProceed {
		slog.Error("succession check says we shouldn't proceed")
		os.Exit(1)
	}

	var restartStart time.Time

	client, err := appctl.DialBinary(binary)
	switch {
	case errors.Is(err, appctl.ErrNoPidFile):
		slog.Info("no existing process found")

		if err := marker.Claim(context.TODO()); err != nil {
			slog.Warn("failed to claim succession", "error", err)
		} else {
			slog.Info("claimed succession", "pod", podName)
		}
	case err != nil:
		slog.Error("failed to connect to process", "error", err)
		os.Exit(1)

	default:
		defer func() {
			if err := client.Close(); err != nil {
				slog.Error("failed to close client", "error", err)
			}
		}()

		var version string
		err = client.CallWithContext(context.TODO(), "version", []string{}, &version)
		if err != nil {
			slog.Error("failed to get version", "error", err)
			os.Exit(1)
		}

		version = strings.TrimSuffix(version, "\n")
		slog.Info("stopping existing process", "version", version)

		if err := marker.Claim(context.TODO()); err != nil {
			slog.Warn("failed to claim succession", "error", err)
		} else {
			slog.Info("claimed succession", "pod", podName)

			if history, err := marker.GetHistory(context.TODO()); err == nil && len(history) > 1 {
				slog.Debug("succession history updated",
					"new_owner", history[0].Owner,
					"previous_owner", history[1].Owner,
					"total_entries", len(history))
			}
		}

		restartStart = time.Now()
		err = client.CallWithContext(context.TODO(), "exit", []string{}, nil)
		if err != nil {
			slog.Error("failed to stop existing process", "error", err)
			os.Exit(1)
		}

		slog.Info("stopped existing process")
	}

	if !restartStart.IsZero() {
		restartDuration := time.Since(restartStart)
		slog.Info("restarting process", "restart_duration_ms", restartDuration.Milliseconds())
	} else {
		slog.Info("starting process")
	}

	err = syscall.Exec(binaryPath, append([]string{binaryPath}, processArgs...), os.Environ())
	if err != nil {
		slog.Error("failed to exec process", "error", err)
		os.Exit(1)
	}
}
