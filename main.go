package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vexxhost/ovsinit/pkg/appctl"
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

	var restartStart time.Time

	// TODO: handle "flip-flop" case when kubelet restarts old process while new one is starting

	client, err := appctl.DialBinary(binary)
	switch {
	case errors.Is(err, appctl.ErrNoPidFile):
		slog.Info("no existing process found")
	case err != nil:
		slog.Error("failed to connect to process", "error", err)
		os.Exit(1)
	// TODO: handle case where pid exists but ctl files doesnt
	default:
		defer client.Close()

		var version string
		err = client.CallWithContext(context.TODO(), "version", []string{}, &version)
		if err != nil {
			slog.Error("failed to get version", "error", err)
			os.Exit(1)
		}

		version = strings.TrimSuffix(version, "\n")
		slog.Info("stopping existing process", "version", version)

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
