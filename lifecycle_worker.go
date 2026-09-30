package ledger

import (
	"context"
	"time"

	log "github.com/xraph/go-utils/log"
)

// lifecycleWorker runs Advance every lifecycleInterval until Stop. The first
// run is one interval after Start. Each run has a deadline of one interval
// and is detached from the Start context, which a caller may cancel once
// Start returns: Stop is what ends the worker.
func (l *Ledger) lifecycleWorker(ctx context.Context) {
	defer l.wg.Done()

	base := context.WithoutCancel(ctx)
	ticker := time.NewTicker(l.lifecycleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopChan:
			return
		case <-ticker.C:
			l.runLifecycle(base)
		}
	}
}

func (l *Ledger) runLifecycle(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, l.lifecycleInterval)
	defer cancel()

	report, err := l.Advance(ctx, l.now())
	if err != nil {
		l.logger.Warn("ledger: lifecycle run left work undone", log.Error(err))
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
