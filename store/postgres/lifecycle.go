package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
)

// dueQueries holds, per DueField, the condition that selects due rows and the
// order that walks them. They are constants: no caller text reaches SQL.
var dueQueries = map[subscription.DueField]struct{ where, order string }{
	subscription.DueCancel:    {"cancel_at <= ?", "cancel_at ASC, id ASC"},
	subscription.DueTrialEnd:  {"trial_end <= ?", "trial_end ASC, id ASC"},
	subscription.DuePeriodEnd: {"current_period_end <= ?", "current_period_end ASC, id ASC"},
}

// runningStatuses are the subscriptions whose periods roll.
var runningStatuses = []subscription.Status{
	subscription.StatusActive, subscription.StatusTrialing, subscription.StatusPastDue,
}

// statusIn is "status IN (?, ...)" with one placeholder per status, and the
// statuses as its arguments.
func statusIn(statuses []subscription.Status) (where string, args []any) {
	args = make([]any, len(statuses))
	for i, st := range statuses {
		args[i] = string(st)
	}
	return "status IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(statuses)), ", ") + ")", args
}

// changed reports whether a conditional update matched a row.
func changed(res interface{ RowsAffected() (int64, error) }, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) ListDueSubscriptions(ctx context.Context, opts subscription.DueOpts) ([]*subscription.Subscription, error) {
	dq, ok := dueQueries[opts.Field]
	if !ok {
		return nil, fmt.Errorf("ledger/postgres: unknown due field %q: %w", opts.Field, ledger.ErrInvalidInput)
	}
	var models []subscriptionModel
	q := s.pg.NewSelect(&models).Where(dq.where, opts.Before.UTC())
	if len(opts.Statuses) > 0 {
		where, args := statusIn(opts.Statuses)
		q = q.Where(where, args...)
	}
	if opts.AppID != "" {
		q = q.Where("app_id = ?", opts.AppID)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.OrderExpr(dq.order).Scan(ctx); err != nil {
		return nil, err
	}
	result := make([]*subscription.Subscription, len(models))
	for i := range models {
		sub, err := fromSubscriptionModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = sub
	}
	return result, nil
}

func (s *Store) ListOverdueInvoices(ctx context.Context, opts invoice.OverdueOpts) ([]*invoice.Invoice, error) {
	var models []invoiceModel
	q := s.pg.NewSelect(&models).
		Where("status = ?", string(invoice.StatusPending)).
		Where("due_date < ?", opts.Before.UTC())
	if opts.AppID != "" {
		q = q.Where("app_id = ?", opts.AppID)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.OrderExpr("due_date ASC, id ASC").Scan(ctx); err != nil {
		return nil, err
	}
	result := make([]*invoice.Invoice, len(models))
	for i := range models {
		inv, err := fromInvoiceModel(&models[i])
		if err != nil {
			return nil, err
		}
		result[i] = inv
	}
	return result, nil
}

func (s *Store) EndSubscriptionTrial(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	return changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("status = ?", string(subscription.StatusActive)).
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Where("status = ?", string(subscription.StatusTrialing)).
		Where("trial_end <= ?", at.UTC()).
		Exec(ctx))
}

func (s *Store) EnactSubscriptionCancel(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	return changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("status = ?", string(subscription.StatusCanceled)).
		Set("canceled_at = cancel_at").
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Where("cancel_at <= ?", at.UTC()).
		Where("status NOT IN (?, ?)", string(subscription.StatusCanceled), string(subscription.StatusExpired)).
		Exec(ctx))
}

func (s *Store) AdvanceSubscriptionPeriod(ctx context.Context, subID id.SubscriptionID, from, start, end, at time.Time) (bool, error) {
	running, args := statusIn(runningStatuses)
	return changed(s.pg.NewUpdate((*subscriptionModel)(nil)).
		Set("current_period_start = ?", start.UTC()).
		Set("current_period_end = ?", end.UTC()).
		Set("updated_at = ?", now()).
		Where("id = ?", subID.String()).
		Where(running, args...).
		Where("current_period_end = ?", from.UTC()).
		Where("current_period_end <= ?", at.UTC()).
		Where("current_period_end < ?", end.UTC()).
		Where("(cancel_at IS NULL OR cancel_at > current_period_end)").
		Exec(ctx))
}

func (s *Store) MarkInvoicePastDue(ctx context.Context, invID id.InvoiceID, at time.Time) (bool, error) {
	return changed(s.pg.NewUpdate((*invoiceModel)(nil)).
		Set("status = ?", string(invoice.StatusPastDue)).
		Set("updated_at = ?", now()).
		Where("id = ?", invID.String()).
		Where("status = ?", string(invoice.StatusPending)).
		Where("due_date < ?", at.UTC()).
		Exec(ctx))
}
