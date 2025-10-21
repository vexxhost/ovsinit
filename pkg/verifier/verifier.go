package verifier

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"
)

type Verifier interface {
	fmt.Stringer
	Verify(ctx context.Context) error
}

func Run(ctx context.Context, verifiers ...Verifier) error {
	g, ctx := errgroup.WithContext(ctx)

	for _, v := range verifiers {
		g.Go(func() error {
			slog.Debug("starting verifier", "name", v.String())

			err := v.Verify(ctx)
			if err != nil {
				slog.Error("verifier failed", "name", v.String(), "error", err)
				return fmt.Errorf("%s: %w", v.String(), err)
			}

			slog.Info("verifier completed successfully", "name", v.String())
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	return nil
}
