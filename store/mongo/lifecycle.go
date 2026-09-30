package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/subscription"
)

// dueFields maps a DueField to the document field it reads.
var dueFields = map[subscription.DueField]string{
	subscription.DueCancel:    "cancel_at",
	subscription.DueTrialEnd:  "trial_end",
	subscription.DuePeriodEnd: "current_period_end",
}

func statusStrings(in []subscription.Status) []string {
	out := make([]string, len(in))
	for i, st := range in {
		out[i] = string(st)
	}
	return out
}

// lifecycleSubscriptionIndexes and lifecycleInvoiceIndexes back the lifecycle
// clock's queries. Migrate builds them through migrationIndexes, and the
// add_lifecycle_indexes migration builds the same ones; keep the two in step.
func lifecycleSubscriptionIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "cancel_at", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "trial_end", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "current_period_end", Value: 1}}},
	}
}

func lifecycleInvoiceIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "due_date", Value: 1}}},
	}
}

func (s *Store) ListDueSubscriptions(ctx context.Context, opts subscription.DueOpts) ([]*subscription.Subscription, error) {
	field, ok := dueFields[opts.Field]
	if !ok {
		return nil, fmt.Errorf("ledger/mongo: unknown due field %q: %w", opts.Field, ledger.ErrInvalidInput)
	}
	filter := bson.M{field: bson.M{"$lte": opts.Before}}
	if len(opts.Statuses) > 0 {
		filter["status"] = bson.M{"$in": statusStrings(opts.Statuses)}
	}
	if opts.AppID != "" {
		filter["app_id"] = opts.AppID
	}
	var models []subscriptionModel
	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: field, Value: 1}, {Key: "_id", Value: 1}})
	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list due subscriptions: %w", err)
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
	filter := bson.M{
		"status":   string(invoice.StatusPending),
		"due_date": bson.M{"$lt": opts.Before},
	}
	if opts.AppID != "" {
		filter["app_id"] = opts.AppID
	}
	var models []invoiceModel
	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "due_date", Value: 1}, {Key: "_id", Value: 1}})
	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}
	if opts.Offset > 0 {
		q = q.Skip(int64(opts.Offset))
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("ledger/mongo: list overdue invoices: %w", err)
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
	res, err := s.mdb.NewUpdate((*subscriptionModel)(nil)).
		Filter(bson.M{
			"_id":       subID.String(),
			"status":    string(subscription.StatusTrialing),
			"trial_end": bson.M{"$lte": at},
		}).
		Set("status", string(subscription.StatusActive)).
		Set("updated_at", now()).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: end trial: %w", err)
	}
	return res.MatchedCount() > 0, nil
}

// EnactSubscriptionCancel copies cancel_at into canceled_at inside the update,
// with a pipeline, so the value written is the stored one and nothing is read
// first. Grove's update builder has no pipeline form, so this goes to the
// driver's collection directly and bypasses grove's operation hooks: a hook a
// host registers on the grove DB does not see this write, though it sees every
// other lifecycle transition.
func (s *Store) EnactSubscriptionCancel(ctx context.Context, subID id.SubscriptionID, at time.Time) (bool, error) {
	res, err := s.mdb.Collection(colSubscriptions).UpdateOne(ctx,
		bson.M{
			"_id":       subID.String(),
			"cancel_at": bson.M{"$lte": at},
			"status":    bson.M{"$nin": []string{string(subscription.StatusCanceled), string(subscription.StatusExpired)}},
		},
		mongo.Pipeline{{{Key: "$set", Value: bson.D{
			{Key: "status", Value: string(subscription.StatusCanceled)},
			{Key: "canceled_at", Value: "$cancel_at"},
			{Key: "updated_at", Value: now()},
		}}}},
	)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: enact cancel: %w", err)
	}
	return res.MatchedCount > 0, nil
}

func (s *Store) AdvanceSubscriptionPeriod(ctx context.Context, subID id.SubscriptionID, start, end, at time.Time) (bool, error) {
	res, err := s.mdb.NewUpdate((*subscriptionModel)(nil)).
		Filter(bson.M{
			"_id": subID.String(),
			"status": bson.M{"$in": []string{
				string(subscription.StatusActive), string(subscription.StatusTrialing), string(subscription.StatusPastDue),
			}},
			"current_period_end": bson.M{"$lte": at, "$lt": end},
			"$or": bson.A{
				bson.M{"cancel_at": nil},
				bson.M{"$expr": bson.M{"$gt": bson.A{"$cancel_at", "$current_period_end"}}},
			},
		}).
		Set("current_period_start", start.UTC()).
		Set("current_period_end", end.UTC()).
		Set("updated_at", now()).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: advance period: %w", err)
	}
	return res.MatchedCount() > 0, nil
}

func (s *Store) MarkInvoicePastDue(ctx context.Context, invID id.InvoiceID, at time.Time) (bool, error) {
	res, err := s.mdb.NewUpdate((*invoiceModel)(nil)).
		Filter(bson.M{
			"_id":      invID.String(),
			"status":   string(invoice.StatusPending),
			"due_date": bson.M{"$lt": at},
		}).
		Set("status", string(invoice.StatusPastDue)).
		Set("updated_at", now()).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("ledger/mongo: mark invoice past due: %w", err)
	}
	return res.MatchedCount() > 0, nil
}
