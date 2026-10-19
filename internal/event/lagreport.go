// SPDX-License-Identifier: MIT

package event

import (
	"context"
	"log/slog"
	"time"
)

// LagReporter logs the drop rate of the bus subscriptions (roadmap 6.2.7):
// every interval it asks the bus for the live drop counters and logs one
// warning per subscription that dropped events in the window. It pulls, so
// the bus stays free of timers and foreign code; the reporter itself runs
// in its own goroutine, started and stopped through Run.
type LagReporter struct {
	bus      *Bus
	logger   *slog.Logger
	interval time.Duration
}

// NewLagReporter returns a reporter that logs once a minute unless
// WithReportInterval sets another window; a nil logger discards.
func NewLagReporter(b *Bus, logger *slog.Logger) *LagReporter {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &LagReporter{bus: b, logger: logger, interval: time.Minute}
}

// WithReportInterval sets the window of the reporter.
func (r *LagReporter) WithReportInterval(d time.Duration) *LagReporter {
	if d > 0 {
		r.interval = d
	}
	return r
}

// Run polls the drop counters until ctx ends.
func (r *LagReporter) Run(ctx context.Context) {
	last := make(map[DropCounter]uint64)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.poll(ctx, last)
		}
	}
}

func (r *LagReporter) poll(ctx context.Context, last map[DropCounter]uint64) {
	live := make(map[DropCounter]struct{}, len(last))
	for _, c := range r.bus.DropCounters() {
		live[c] = struct{}{}
		n := c.Count()
		prev, seen := last[c]
		if seen && n > prev {
			r.logger.WarnContext(ctx, "event subscription is dropping events",
				"subscription", c.Name(), "drops", n-prev, "window", r.interval, "total", n)
		}
		last[c] = n
	}
	for c := range last {
		if _, ok := live[c]; !ok {
			delete(last, c)
		}
	}
}
