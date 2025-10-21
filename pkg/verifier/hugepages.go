package verifier

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/procfs"
)

type HugePagesVerifier struct {
	fs *procfs.FS
}

func HugePages() *HugePagesVerifier {
	return &HugePagesVerifier{}
}

func HugePagesWithFS(fs *procfs.FS) *HugePagesVerifier {
	return &HugePagesVerifier{
		fs: fs,
	}
}

func (v *HugePagesVerifier) String() string {
	return "hugepages"
}

func (v *HugePagesVerifier) Verify(ctx context.Context) error {
	if v.fs == nil {
		fs, err := procfs.NewDefaultFS()
		if err != nil {
			slog.Warn("procfs not available, skipping hugepages check", "error", err)
			return nil
		}

		v.fs = &fs
	}

	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			memInfo, err := v.fs.Meminfo()
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

		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				slog.Warn("timeout waiting for hugepages, proceeding anyway")
				return nil
			}

			return ctx.Err()
		}
	}
}
