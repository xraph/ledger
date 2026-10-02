package observability_test

import (
	"context"
	"testing"

	"github.com/xraph/ledger/observability"
)

type counter struct{ n float64 }

func (c *counter) Inc()            { c.n++ }
func (c *counter) Add(v float64)   { c.n += v }
func (c *counter) Observe(float64) {}

type factory map[string]*counter

func (f factory) Counter(name string) observability.Counter {
	if f[name] == nil {
		f[name] = &counter{}
	}
	return f[name]
}

func (f factory) Histogram(name string) observability.Histogram { return f.Counter(name).(*counter) }

// The lifecycle clock's transitions and scheduled cancels are counted.
func TestLifecycleTransitionsAreCounted(t *testing.T) {
	f := factory{}
	m := observability.NewMetricsExtension(f)
	ctx := context.Background()
	_ = m.OnSubscriptionRenewed(ctx, nil)
	_ = m.OnSubscriptionTrialEnded(ctx, nil)
	_ = m.OnSubscriptionCancelScheduled(ctx, nil)
	_ = m.OnSubscriptionCanceled(ctx, nil)
	_ = m.OnInvoicePastDue(ctx, nil)
	for _, name := range []string{
		"ledger.subscription.renewed", "ledger.subscription.trial_ended", "ledger.subscription.cancel_scheduled",
		"ledger.subscription.canceled", "ledger.invoice.past_due",
	} {
		if f[name] == nil || f[name].n != 1 {
			t.Errorf("%s counted %v, want 1", name, f[name])
		}
	}
}
