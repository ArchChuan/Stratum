package integration

import (
	"context"
	"errors"
	"fmt"
	"time"

	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

var ErrMilvusReadinessTimeout = errors.New("Milvus readiness timeout")

func waitForMilvusReady(
	ctx context.Context,
	required bool,
	retryInterval time.Duration,
	connect func(context.Context) error,
	probe func(context.Context) error,
) error {
	started := time.Now()
	var lastErr error
	for {
		err := connect(ctx)
		if err == nil {
			err = probe(ctx)
		}
		if err == nil {
			return nil
		}
		lastErr = err
		if !required || errors.Is(err, context.Canceled) {
			return err
		}
		var unavailable *storagemilvus.UnavailableError
		if !errors.As(err, &unavailable) {
			return err
		}

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
			return fmt.Errorf("%w after %v: %v", ErrMilvusReadinessTimeout, time.Since(started).Round(time.Millisecond), lastErr)
		case <-timer.C:
		}
	}
}
