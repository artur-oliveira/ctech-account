package deletion

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/observability"
)

// WorkerInterval is how often the due index is polled.
const WorkerInterval = time.Minute

// RunWorker runs ProcessDue on whichever instance wins tryLock for the tick.
// ponytail: tick-scoped lock that just expires (TTL < interval), no renewal;
// fine while one tick finishes well inside a minute.
func RunWorker(ctx context.Context, svc *Service, tryLock func(context.Context) (bool, error), interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ok, err := tryLock(ctx)
			if err != nil {
				observability.Error(ctx, "deletion worker: lock failed", err)
				continue
			}
			if ok {
				svc.ProcessDue(ctx)
			}
		}
	}
}
