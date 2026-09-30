package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
)

// dueDate returns the reader for the date a DueField names; it answers nil
// when the row has no such date.
func dueDate(f subscription.DueField) (func(*subscription.Subscription) *time.Time, error) {
	switch f {
	case subscription.DueCancel:
		return func(s *subscription.Subscription) *time.Time { return s.CancelAt }, nil
	case subscription.DueTrialEnd:
		return func(s *subscription.Subscription) *time.Time { return s.TrialEnd }, nil
	case subscription.DuePeriodEnd:
		return func(s *subscription.Subscription) *time.Time { return &s.CurrentPeriodEnd }, nil
	}
	return nil, fmt.Errorf("ledger/memory: unknown due field %q: %w", f, ledger.ErrInvalidInput)
}

func statusIn(st subscription.Status, in []subscription.Status) bool {
	if len(in) == 0 {
		return true
	}
	for _, want := range in {
		if st == want {
			return true
		}
	}
	return false
}

func (s *Store) ListDueSubscriptions(_ context.Context, opts subscription.DueOpts) ([]*subscription.Subscription, error) {
	date, err := dueDate(opts.Field)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*subscription.Subscription, 0)
	for _, sub := range s.subscriptions {
		d := date(sub)
		if (opts.AppID == "" || sub.AppID == opts.AppID) && statusIn(sub.Status, opts.Statuses) &&
			d != nil && !d.After(opts.Before) {
			result = append(result, copySubscription(sub))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		di, dj := date(result[i]), date(result[j])
		if !di.Equal(*dj) {
			return di.Before(*dj)
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	return window(result, opts.Limit, opts.Offset), nil
}

func (s *Store) ListOverdueInvoices(_ context.Context, opts invoice.OverdueOpts) ([]*invoice.Invoice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Each row is a copy, so a caller reading one while another goroutine
	// marks it paid or past due reads its own value and not the stored one.
	result := make([]*invoice.Invoice, 0)
	for _, inv := range s.invoices {
		if (opts.AppID == "" || inv.AppID == opts.AppID) && inv.Status == invoice.StatusPending &&
			inv.DueDate != nil && inv.DueDate.Before(opts.Before) {
			cp := *inv
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].DueDate.Equal(*result[j].DueDate) {
			return result[i].DueDate.Before(*result[j].DueDate)
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	return window(result, opts.Limit, opts.Offset), nil
}

func (s *Store) EndSubscriptionTrial(_ context.Context, subID id.SubscriptionID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || sub.Status != subscription.StatusTrialing || sub.TrialEnd == nil || sub.TrialEnd.After(now) {
		return false, nil
	}
	sub.Status = subscription.StatusActive
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *Store) EnactSubscriptionCancel(_ context.Context, subID id.SubscriptionID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	if !ok || sub.CancelAt == nil || sub.CancelAt.After(now) ||
		sub.Status == subscription.StatusCanceled || sub.Status == subscription.StatusExpired {
		return false, nil
	}
	canceledAt := *sub.CancelAt
	sub.Status = subscription.StatusCanceled
	sub.CanceledAt = &canceledAt
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (s *Store) AdvanceSubscriptionPeriod(_ context.Context, subID id.SubscriptionID, start, end, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, ok := s.subscriptions[subID.String()]
	running := ok && (sub.Status == subscription.StatusActive || sub.Status == subscription.StatusTrialing ||
		sub.Status == subscription.StatusPastDue)
	if !running || sub.CurrentPeriodEnd.After(now) || !sub.CurrentPeriodEnd.Before(end) ||
		(sub.CancelAt != nil && !sub.CancelAt.After(sub.CurrentPeriodEnd)) {
		return false, nil
	}
	sub.CurrentPeriodStart = start.UTC()
	sub.CurrentPeriodEnd = end.UTC()
	sub.UpdatedAt = time.Now().UTC()
	return true, nil
}

// MarkInvoicePastDue stores a changed copy in place of the invoice rather than
// writing to it. This store keeps invoices by the pointer it was handed and
// hands the same pointer back from GetInvoice and the older lists, so a write
// to it would race any caller still reading an invoice the clock is moving.
// The copy removes this method from that race, not the store: other writers
// still share the stored pointer, Ledger.MarkInvoicePaid among them, which
// sets Status, PaidAt and PaymentRef on the invoice GetInvoice returned, after
// the store call and outside this store's lock.
func (s *Store) MarkInvoicePastDue(_ context.Context, invID id.InvoiceID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inv, ok := s.invoices[invID.String()]
	if !ok || inv.Status != invoice.StatusPending || inv.DueDate == nil || !inv.DueDate.Before(now) {
		return false, nil
	}
	cp := *inv
	cp.Status = invoice.StatusPastDue
	cp.UpdatedAt = time.Now().UTC()
	s.invoices[invID.String()] = &cp
	return true, nil
}
