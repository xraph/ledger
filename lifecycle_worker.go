package ledger

import (
	"context"
	"runtime/debug"
	"time"

	log "github.com/xraph/go-utils/log"
)

// lifecycleWorker runs Advance every lifecycleInterval until Stop. The first
// run is one interval after Start. Each run has a deadline of one interval
// and is detached from the Start context, which a caller may cancel once
// Start returns: Stop is what ends the worker, and it also cancels a run that
// is in flight, so Stop never waits out a slow Advance.
func (l *Ledger) lifecycleWorker(ctx context.Context) {
	defer l.wg.Done()

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	// Stop closes stopChan; this turns that into a cancelled run context. The
	// watcher is counted, so Stop waits for it too. Adding to the group from
	// here is safe: this goroutine still holds its own count.
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		select {
		case <-l.stopChan:
			cancel()
		case <-runCtx.Done():
		}
	}()

	ticker := time.NewTicker(l.lifecycleInterval)
	defer ticker.Stop()

	for {
		if l.stopping() {
			return
		}
		select {
		case <-l.stopChan:
			return
		case <-ticker.C:
			// A tick and a stop can be ready together, and select picks
			// either. Stop wins: no run starts once Stop has been called.
			if l.stopping() {
				return
			}
			l.runLifecycle(runCtx)
		}
	}
}

// stopping reports whether Stop has been called.
func (l *Ledger) stopping() bool {
	select {
	case <-l.stopChan:
		return true
	default:
		return false
	}
}

// runLifecycle is one tick. A panic in Advance, which a store driver or a
// store wrapper can raise, is logged and ends only this run: the next tick
// starts clean. The recover does not reach plugin hooks. They run on their own
// goroutines (plugin.Registry callWithTimeout), so a panic in a hook is not
// caught here, and was not caught before the clock existed either.
func (l *Ledger) runLifecycle(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			l.logger.Error("ledger: lifecycle run panicked",
				log.Any("panic", r),
				log.String("stack", string(debug.Stack())),
			)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, l.lifecycleInterval)
	defer cancel()

	report, err := l.Advance(ctx, l.now())
	if err != nil {
		// A run that Stop cancelled is not work left undone.
		if l.stopping() {
			l.logger.Debug("ledger: lifecycle run cut short by stop", log.Error(err))
		} else {
			l.logger.Warn("ledger: lifecycle run left work undone", log.Error(err))
		}
	}
	if !report.Empty() {
		l.logger.Info("ledger: lifecycle run",
			log.Int("periods_advanced", len(report.PeriodsAdvanced)),
			log.Int("cancels_enacted", len(report.CancelsEnacted)),
			log.Int("trials_ended", len(report.TrialsEnded)),
			log.Int("invoices_past_due", len(report.InvoicesPastDue)),
		)
	}
}
