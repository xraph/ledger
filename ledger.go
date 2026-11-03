package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	log "github.com/xraph/go-utils/log"
	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/entitlement"
	"github.com/xraph/ledger/feature"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/internal/telemetry"
	"github.com/xraph/ledger/invoice"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// Ledger is the main billing engine.
type Ledger struct {
	store   store.Store
	plugins *plugin.Registry
	logger  log.Logger

	// Background workers
	meterBuffer chan *meter.UsageEvent
	// retained counts keyed events the flush worker is holding for retry. They
	// have left meterBuffer, so MeterWithKey adds them to len(meterBuffer) when
	// it checks the buffer's capacity, which keeps a long outage from growing
	// memory without bound.
	retained atomic.Int64
	stopChan chan struct{}
	wg       sync.WaitGroup

	// importMu serialises the four provider imports from their duplicate check
	// through their store write, so one process never stores a second row for
	// a repeated import. It does not reach across replicas: with more than one,
	// two imports of the same record can still race, and the dashboard's
	// disabled button while a request is pending is what keeps that rare.
	importMu sync.Mutex

	// Configuration
	meterBatchSize      int
	meterFlushInterval  time.Duration
	retryInitial        time.Duration // first wait before a failed keyed batch is retried
	retryMax            time.Duration // the wait stops doubling here
	entitlementCacheTTL time.Duration
	lifecycleInterval   time.Duration
	clock               func() time.Time
	skipMigrate         bool

	// Telemetry. metricsSink is what WithMetrics set; New builds metrics from
	// it once the options have run.
	metricsSink gometrics.Metrics
	metrics     engineMetrics
}

// New creates a new Ledger instance.
func New(s store.Store, opts ...Option) *Ledger {
	l := &Ledger{
		store:               s,
		plugins:             plugin.NewRegistry(),
		logger:              log.NewNoopLogger(),
		meterBuffer:         make(chan *meter.UsageEvent, 10000),
		stopChan:            make(chan struct{}),
		meterBatchSize:      100,
		meterFlushInterval:  5 * time.Second,
		retryInitial:        time.Second,
		retryMax:            30 * time.Second,
		entitlementCacheTTL: 30 * time.Second,
		lifecycleInterval:   time.Minute,
		clock:               time.Now,
	}

	for _, opt := range opts {
		opt(l)
	}

	l.metrics = newEngineMetrics(l.metricsSink)
	l.plugins.WithMetrics(l.metricsSink)

	return l
}

// Option configures a Ledger instance.
type Option func(*Ledger)

// WithLogger sets the logger.
func WithLogger(logger log.Logger) Option {
	return func(l *Ledger) {
		l.logger = logger
		l.plugins.WithLogger(logger)
	}
}

// WithPlugin registers a plugin.
func WithPlugin(p plugin.Plugin) Option {
	return func(l *Ledger) {
		_ = l.plugins.Register(p) //nolint:errcheck // best-effort plugin registration during init
	}
}

// WithMeterConfig configures metering parameters.
func WithMeterConfig(batchSize int, flushInterval time.Duration) Option {
	return func(l *Ledger) {
		l.meterBatchSize = batchSize
		l.meterFlushInterval = flushInterval
	}
}

// WithEntitlementCacheTTL sets the entitlement cache TTL.
func WithEntitlementCacheTTL(ttl time.Duration) Option {
	return func(l *Ledger) {
		l.entitlementCacheTTL = ttl
	}
}

// WithLifecycleInterval sets how often the lifecycle clock runs Advance in the
// background (default: one minute). Zero or less turns the clock off, for a
// deployment that calls Advance from its own scheduler.
func WithLifecycleInterval(d time.Duration) Option {
	return func(l *Ledger) {
		l.lifecycleInterval = max(d, 0)
	}
}

// WithClock sets where the engine reads the time for its decisions. Tests use
// it. A nil clock keeps time.Now.
//
// These follow the clock: the lifecycle worker's Advance, a new
// subscription's creation time, first period and trial, the periods
// GenerateInvoice will bill, coupon validity, and an invoice's due date.
//
// These stay on the wall clock: UpdatedAt and other change stamps, the
// timestamp on a metered usage event, the moment an immediate cancel or a
// void is recorded, the store's own usage windows, and the expiry of the
// entitlement cache.
func WithClock(now func() time.Time) Option {
	return func(l *Ledger) {
		if now != nil {
			l.clock = now
		}
	}
}

// LifecycleInterval reports how often the lifecycle clock runs. Zero means it
// is off.
func (l *Ledger) LifecycleInterval() time.Duration { return l.lifecycleInterval }

// now is the engine's current time, in UTC, from the configured clock.
func (l *Ledger) now() time.Time { return l.clock().UTC() }

// stamp is now cut to the millisecond, for a time the engine stores and hands
// back to the caller: a subscription's creation and first period, a pause, a
// resume. Mongo keeps milliseconds and postgres microseconds, so a nanosecond
// value would come back from the store different from the one the caller
// holds, and a period named from the caller's copy would not match.
func (l *Ledger) stamp() time.Time { return l.now().Truncate(time.Millisecond) }

// WithoutMigrate makes Start skip the store migration and start only the
// background workers, for a deployment that migrates its schema separately.
func WithoutMigrate() Option {
	return func(l *Ledger) {
		l.skipMigrate = true
	}
}

// Store returns the underlying ledger store.
func (l *Ledger) Store() store.Store { return l.store }

// Start begins background workers.
func (l *Ledger) Start(ctx context.Context) error {
	// Migrate database, unless the deployment migrates it separately
	if !l.skipMigrate {
		if err := l.store.Migrate(ctx); err != nil {
			return err
		}
	}

	// Initialize plugins
	l.plugins.EmitInit(ctx, l)

	// Start meter flush worker. Like the lifecycle clock it outlives Start:
	// Stop ends it, not ctx, which a caller may cancel once Start returns.
	// It drops ctx's span too, so each flush is a trace of its own.
	l.wg.Add(1)
	go l.meterFlushWorker(telemetry.Detach(context.WithoutCancel(ctx)))

	// Start the lifecycle clock
	if l.lifecycleInterval > 0 {
		l.wg.Add(1)
		go l.lifecycleWorker(ctx)
	}

	l.logger.Info("ledger started",
		log.Int("batch_size", l.meterBatchSize),
		log.Duration("flush_interval", l.meterFlushInterval),
		log.Duration("cache_ttl", l.entitlementCacheTTL),
		log.Duration("lifecycle_interval", l.lifecycleInterval),
	)

	return nil
}

// Health checks the health of the Ledger by pinging its store.
func (l *Ledger) Health(ctx context.Context) error {
	return l.store.Ping(ctx)
}

// Stop shuts down the Ledger.
func (l *Ledger) Stop() error {
	close(l.stopChan)
	l.wg.Wait()

	ctx := context.Background()
	l.plugins.EmitShutdown(ctx)

	return l.store.Close()
}

// ──────────────────────────────────────────────────
// Plan Management
// ──────────────────────────────────────────────────

// CreatePlan validates and stores a new billing plan. It normalises the plan
// first (see normalisePlan), and refuses a slug the app already uses with
// ErrAlreadyExists.
func (l *Ledger) CreatePlan(ctx context.Context, p *plan.Plan) error {
	if p.ID == (id.PlanID{}) {
		p.ID = id.NewPlanID()
	}
	normalisePlan(p)
	if err := validatePlan(p); err != nil {
		return err
	}
	taken, err := l.slugTaken(ctx, p.Slug, p.AppID, p.ID)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("%w: slug %q is already used in this app", ErrAlreadyExists, p.Slug)
	}
	p.Entity = types.NewEntity()

	if err := l.store.CreatePlan(ctx, p); err != nil {
		return err
	}

	l.plugins.EmitPlanCreated(ctx, p)
	return nil
}

// GetPlan retrieves a plan by ID.
func (l *Ledger) GetPlan(ctx context.Context, planID id.PlanID) (*plan.Plan, error) {
	return l.store.GetPlan(ctx, planID)
}

// GetPlanBySlug retrieves a plan by slug.
func (l *Ledger) GetPlanBySlug(ctx context.Context, slug, appID string) (*plan.Plan, error) {
	return l.store.GetPlanBySlug(ctx, slug, appID)
}

// ──────────────────────────────────────────────────
// Feature Catalog Management
// ──────────────────────────────────────────────────

// CreateFeature creates a new catalog feature.
func (l *Ledger) CreateFeature(ctx context.Context, f *feature.Feature) error {
	if f.ID == (id.FeatureID{}) {
		f.ID = id.NewFeatureID()
	}
	f.Entity = types.NewEntity()

	if err := l.store.CreateFeature(ctx, f); err != nil {
		return err
	}

	l.plugins.EmitFeatureCreated(ctx, f)
	return nil
}

// GetFeature retrieves a catalog feature by ID.
func (l *Ledger) GetFeature(ctx context.Context, featureID id.FeatureID) (*feature.Feature, error) {
	return l.store.GetFeature(ctx, featureID)
}

// GetFeatureByKey retrieves a catalog feature by key and app scope.
func (l *Ledger) GetFeatureByKey(ctx context.Context, key, appID string) (*feature.Feature, error) {
	return l.store.GetFeatureByKey(ctx, key, appID)
}

// ListFeatures lists catalog features for an app.
func (l *Ledger) ListFeatures(ctx context.Context, appID string, opts feature.ListOpts) ([]*feature.Feature, error) {
	return l.store.ListFeatures(ctx, appID, opts)
}

// ListGlobalFeatures lists catalog features with no app scope.
func (l *Ledger) ListGlobalFeatures(ctx context.Context, opts feature.ListOpts) ([]*feature.Feature, error) {
	return l.store.ListGlobalFeatures(ctx, opts)
}

// UpdateFeature updates a catalog feature.
func (l *Ledger) UpdateFeature(ctx context.Context, f *feature.Feature) error {
	old, err := l.store.GetFeature(ctx, f.ID)
	if err != nil {
		return err
	}

	if err := l.store.UpdateFeature(ctx, f); err != nil {
		return err
	}

	l.plugins.EmitFeatureUpdated(ctx, old, f)
	return nil
}

// DeleteFeature deletes a catalog feature.
func (l *Ledger) DeleteFeature(ctx context.Context, featureID id.FeatureID) error {
	if err := l.store.DeleteFeature(ctx, featureID); err != nil {
		return err
	}

	l.plugins.EmitFeatureDeleted(ctx, featureID.String())
	return nil
}

// ArchiveFeature archives a catalog feature.
func (l *Ledger) ArchiveFeature(ctx context.Context, featureID id.FeatureID) error {
	if err := l.store.ArchiveFeature(ctx, featureID); err != nil {
		return err
	}

	l.plugins.EmitFeatureArchived(ctx, featureID.String())
	return nil
}

// ──────────────────────────────────────────────────
// Subscription Management
// ──────────────────────────────────────────────────

// CreateSubscription creates a new subscription.
//
// A subscription must name its tenant. Every store reads an empty tenant id
// as "every tenant", so one without a tenant would later be billed for other
// customers' usage. It is refused with an error wrapping ErrInvalidInput and
// nothing is written. The app id may stay empty: deployments that never set
// one still bill their base fee.
func (l *Ledger) CreateSubscription(ctx context.Context, sub *subscription.Subscription) error {
	if sub.TenantID == "" {
		return fmt.Errorf("%w: subscription has no tenant id", ErrInvalidInput)
	}

	p, err := l.subscribablePlan(ctx, sub.PlanID, sub.AppID)
	if err != nil {
		return err
	}
	if err := validateQuantity(p, sub.Quantity); err != nil {
		return err
	}

	if sub.ID == (id.SubscriptionID{}) {
		sub.ID = id.NewSubscriptionID()
	}
	// Stamped from the engine's clock, so a subscription created under
	// WithClock has its first period and its trial on that clock's dates,
	// and cut to the millisecond, so the struct the caller keeps matches the
	// row on every backend.
	created := l.stamp()
	sub.Entity = types.Entity{CreatedAt: created, UpdatedAt: created}

	// Set the first period from the plan's billing period (see
	// billingPeriod). Stored times are UTC, so every backend holds the same
	// representation. time.Now() carries the host's zone and a monotonic
	// reading, neither of which a store can round-trip.
	switch {
	case sub.CurrentPeriodStart.IsZero():
		// From CreatedAt, not a second clock reading: on a nanosecond clock a
		// start later than created_at would let ForPeriod accept the period
		// before the first, which ends just after created_at.
		sub.CurrentPeriodStart = sub.CreatedAt.UTC()
		sub.CurrentPeriodEnd = firstPeriodEnd(sub.CurrentPeriodStart, billingPeriod(p))
	case sub.CurrentPeriodEnd.IsZero():
		sub.CurrentPeriodStart = sub.CurrentPeriodStart.UTC()
		sub.CurrentPeriodEnd = firstPeriodEnd(sub.CurrentPeriodStart, billingPeriod(p))
	}

	if sub.Status == "" {
		if p.TrialDays > 0 {
			trialStart := sub.CurrentPeriodStart
			trialEnd := trialStart.AddDate(0, 0, p.TrialDays)
			sub.Status = subscription.StatusTrialing
			sub.TrialStart = &trialStart
			sub.TrialEnd = &trialEnd
		} else {
			sub.Status = subscription.StatusActive
		}
	}

	if err := l.store.CreateSubscription(ctx, sub); err != nil {
		return err
	}

	// Invalidate entitlement cache for tenant
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	l.plugins.EmitSubscriptionCreated(ctx, sub)
	return nil
}

// GetSubscription retrieves a subscription by ID.
func (l *Ledger) GetSubscription(ctx context.Context, subID id.SubscriptionID) (*subscription.Subscription, error) {
	return l.store.GetSubscription(ctx, subID)
}

// GetActiveSubscription retrieves the active subscription for a tenant.
func (l *Ledger) GetActiveSubscription(ctx context.Context, tenantID, appID string) (*subscription.Subscription, error) {
	return l.store.GetActiveSubscription(ctx, tenantID, appID)
}

// CancelSubscription cancels a subscription, now or at the end of its current
// period. A subscription that is already canceled or expired is refused, so a
// cancel on it can neither move its cancel_at nor announce the cancellation
// again. One that is still running can be canceled again while a scheduled
// cancel waits: a scheduled cancel sets cancel_at to the current period end
// once more, and an immediate one stops it now.
//
// OnSubscriptionCanceled fires only when the subscription stopped now. A
// cancel dated later fires OnSubscriptionCancelScheduled, and the lifecycle
// clock fires OnSubscriptionCanceled when it enacts it. That holds even when
// the period it ends is already over: the subscription keeps running until
// the clock's next run cancels it as of that period's end.
func (l *Ledger) CancelSubscription(ctx context.Context, subID id.SubscriptionID, immediately bool) error {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return err
	}
	switch sub.Status {
	case subscription.StatusCanceled:
		return ErrSubscriptionCanceled
	case subscription.StatusExpired:
		return ErrSubscriptionExpired
	}

	// The store picks the date: now, or for a scheduled cancel the period end
	// current when its write lands, which is later than the one read above
	// if the lifecycle clock advanced the period in between.
	cancelAt, err := l.store.CancelSubscription(ctx, subID, immediately)
	if err != nil {
		return err
	}
	// The hooks get the row as stored, which carries the store's updated_at
	// and anything written beside the cancel, such as a period the clock
	// advanced. The row read above stands in only if the read fails.
	sub = l.storedOr(ctx, sub, func(x *subscription.Subscription) {
		x.CancelAt = &cancelAt
		if immediately {
			canceledAt := cancelAt
			x.Status = subscription.StatusCanceled
			x.CanceledAt = &canceledAt
		}
	})

	// Invalidate entitlement cache
	_ = l.store.Invalidate(ctx, sub.TenantID, sub.AppID) //nolint:errcheck // best-effort cache invalidation

	// Only an immediate cancel changes the status; a scheduled one never
	// does, whatever date the store copied into cancel_at.
	if immediately {
		l.plugins.EmitSubscriptionCanceled(ctx, sub)
	} else {
		l.plugins.EmitSubscriptionCancelScheduled(ctx, sub)
	}
	return nil
}

// ──────────────────────────────────────────────────
// Usage Metering
// ──────────────────────────────────────────────────

// Meter records a usage event (non-blocking).
//
// The event is buffered and written by a background worker. If that write
// fails the event is dropped and counted. Use MeterWithKey when the event must
// survive a store outage and must not count twice. After Stop it returns an
// error wrapping ErrStoreClosed.
func (l *Ledger) Meter(ctx context.Context, featureKey string, quantity int64) error {
	return l.meter(ctx, featureKey, quantity, "")
}

// MeterWithKey is Meter for an event that carries an idempotency key. The store
// counts a key once, so sending the same key again, from a retry of your own or
// a replay, adds nothing.
//
// The key is unique across every tenant and every app, not within the tenant in
// the context. Every store dedupes on the key alone, so two tenants that send
// the same key have the second event dropped without an error. Namespace it
// yourself, for example "<tenant>/<app>/<message id>".
//
// A key must be non-empty, valid UTF-8, free of NUL bytes and at most 255 bytes
// long, or the call returns ErrInvalidInput. A key a store would always reject
// would otherwise sit in the retry queue until Stop.
//
// A keyed event whose write fails is not dropped. The worker keeps it and tries
// again with a growing wait (one second, doubling to thirty), ahead of newer
// events, until the store takes it or Stop makes a last attempt. While events
// are held they count against the buffer together with the events waiting in
// it, so a long outage ends in ErrMeterBufferFull from this method, not in
// unbounded memory.
func (l *Ledger) MeterWithKey(ctx context.Context, featureKey string, quantity int64, idempotencyKey string) error {
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return err
	}

	return l.meter(ctx, featureKey, quantity, idempotencyKey)
}

// maxIdempotencyKeyLen is the longest key MeterWithKey accepts, in bytes.
const maxIdempotencyKeyLen = 255

func validateIdempotencyKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: an idempotency key is required", ErrInvalidInput)
	case len(key) > maxIdempotencyKeyLen:
		return fmt.Errorf("%w: an idempotency key is at most %d bytes", ErrInvalidInput, maxIdempotencyKeyLen)
	case !utf8.ValidString(key):
		return fmt.Errorf("%w: an idempotency key must be valid UTF-8", ErrInvalidInput)
	case strings.ContainsRune(key, 0):
		return fmt.Errorf("%w: an idempotency key cannot contain a NUL byte", ErrInvalidInput)
	}

	return nil
}

func (l *Ledger) meter(ctx context.Context, featureKey string, quantity int64, idempotencyKey string) error {
	// After Stop nothing flushes the buffer, so an accepted event would be lost
	// without a word.
	select {
	case <-l.stopChan:
		return fmt.Errorf("%w: the ledger is stopped", ErrStoreClosed)
	default:
	}

	// Extract tenant and app from context
	tenantID := extractTenantID(ctx)
	appID := extractAppID(ctx)

	if tenantID == "" || appID == "" {
		return ErrInvalidInput
	}

	event := &meter.UsageEvent{
		ID:             id.NewUsageEventID(),
		TenantID:       tenantID,
		AppID:          appID,
		FeatureKey:     featureKey,
		Quantity:       quantity,
		Timestamp:      time.Now().UTC(),
		IdempotencyKey: idempotencyKey,
	}

	if idempotencyKey != "" && l.retained.Load()+int64(len(l.meterBuffer)) >= int64(cap(l.meterBuffer)) {
		l.metrics.meterEvents.Inc(resultBufferFull)
		return ErrMeterBufferFull
	}

	select {
	case l.meterBuffer <- event:
		return nil
	default:
		l.metrics.meterEvents.Inc(resultBufferFull)
		return ErrMeterBufferFull
	}
}

// finalFlushTimeout bounds the last write Stop makes, so a store that has
// stopped answering cannot hang shutdown.
const finalFlushTimeout = 10 * time.Second

// retryQueue holds the keyed events of failed batches, oldest first, and when
// the worker may try them again. Only the flush worker touches it.
type retryQueue struct {
	events []*meter.UsageEvent
	delay  time.Duration // the wait after the last failed attempt
	due    time.Time     // when the held events may be tried again
}

// meterFlushWorker flushes usage events to the store.
func (l *Ledger) meterFlushWorker(ctx context.Context) {
	defer l.wg.Done()

	var (
		batch = make([]*meter.UsageEvent, 0, l.meterBatchSize)
		held  retryQueue
	)
	ticker := time.NewTicker(l.meterFlushInterval)
	defer ticker.Stop()
	// retry fires when held events are due. It stays stopped while none are
	// held.
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()

	flush := func() {
		l.flushMeterBatch(ctx, &held, batch, false)
		batch = make([]*meter.UsageEvent, 0, l.meterBatchSize)
		if len(held.events) > 0 {
			retry.Reset(max(time.Until(held.due), 0))
		} else {
			retry.Stop()
		}
	}

	for {
		select {
		case <-l.stopChan:
			// Final flush: whatever is still buffered, plus anything held.
		drain:
			for {
				select {
				case event := <-l.meterBuffer:
					batch = append(batch, event)
				default:
					break drain
				}
			}
			final, cancel := context.WithTimeout(ctx, finalFlushTimeout)
			l.flushMeterBatch(final, &held, batch, true)
			cancel()

			return

		case event := <-l.meterBuffer:
			batch = append(batch, event)
			if len(batch) >= l.meterBatchSize {
				flush()
			}

		case <-ticker.C:
			if len(batch) > 0 {
				flush()
			}

		case <-retry.C:
			flush()
		}
	}
}

// flushMeterBatch writes one batch, with the held events of earlier failed
// batches ahead of it once they are due (or at once, when final). Each flush is
// a trace of its own. The write goes out in pieces of at most the batch size,
// so a long outage's backlog never lands as one oversized insert.
//
// A failed write loses the events that carry no idempotency key, which the
// meter events metric counts. Keyed events that were not written go back on
// the retry queue, in order, and are counted when they land, or as lost if the
// final attempt fails.
func (l *Ledger) flushMeterBatch(ctx context.Context, held *retryQueue, batch []*meter.UsageEvent, final bool) {
	retryHeld := len(held.events) > 0 && (final || !time.Now().Before(held.due))
	send := batch
	if retryHeld {
		send = make([]*meter.UsageEvent, 0, len(held.events)+len(batch))
		send = append(send, held.events...)
		send = append(send, batch...)
	}
	if len(send) == 0 {
		return
	}

	ctx, span := telemetry.StartRoot(ctx, "flush meter batch",
		trace.WithAttributes(attribute.Int(attrBatchSize, len(send))))
	defer span.End()

	start := time.Now()

	var err error
	sent := 0
	for sent < len(send) {
		end := min(sent+max(l.meterBatchSize, 1), len(send))
		if err = l.store.IngestBatch(ctx, send[sent:end]); err != nil {
			break
		}
		sent = end
	}

	if sent > 0 {
		l.metrics.meterEvents.Add(float64(sent), resultOK)

		elapsed := time.Since(start)
		l.plugins.EmitUsageFlushed(ctx, sent, elapsed)

		l.logger.Debug("flushed meter batch",
			log.Int("batch_size", sent),
			log.Int64("elapsed_ms", elapsed.Milliseconds()),
		)
	}
	if err == nil {
		if retryHeld {
			held.events, held.delay = nil, 0
			l.retained.Store(0)
		}

		return
	}

	telemetry.Fail(span, errorType(err))

	unsent := send[sent:]
	keyed := keyedEvents(unsent)
	lost := len(unsent) - len(keyed)
	switch {
	case final:
		// Nothing is left to retry after this.
		lost = len(unsent)
		held.events, held.delay = nil, 0
		l.retained.Store(0)
	case retryHeld:
		held.events = keyed
		held.delay = min(max(held.delay, l.retryInitial)*2, l.retryMax)
		if len(keyed) == 0 {
			held.delay = 0
		}
		held.due = time.Now().Add(held.delay)
		l.retained.Store(int64(len(keyed)))
	case len(keyed) > 0:
		// The held events were not due, so only this batch was sent. Its keyed
		// events queue behind them.
		if len(held.events) == 0 {
			held.delay = l.retryInitial
			held.due = time.Now().Add(held.delay)
		}
		held.events = append(held.events, keyed...)
		l.retained.Store(int64(len(held.events)))
	}
	if lost > 0 {
		l.metrics.meterEvents.Add(float64(lost), resultError)
	}
	l.logger.Error("failed to flush meter batch",
		log.Error(err),
		log.Int("batch_size", len(send)),
		log.Int("lost", lost),
		log.Int("held_for_retry", len(held.events)),
	)
}

// keyedEvents returns the events that carry an idempotency key, in order.
func keyedEvents(events []*meter.UsageEvent) []*meter.UsageEvent {
	var keyed []*meter.UsageEvent
	for _, e := range events {
		if e.IdempotencyKey != "" {
			keyed = append(keyed, e)
		}
	}

	return keyed
}

// ──────────────────────────────────────────────────
// Entitlements
// ──────────────────────────────────────────────────

// Entitled checks if the current tenant can use a feature.
//
// It runs on most requests, so its span is only started under a caller's
// span: a check made outside any trace does not begin one.
//
// A store that cannot answer reads as no access here: a failed subscription
// lookup comes back as "no active subscription" with a nil error. Use Check
// when you need to tell an outage from a real no.
func (l *Ledger) Entitled(ctx context.Context, featureKey string) (*entitlement.Result, error) {
	return l.checkEntitlement(ctx, featureKey, false)
}

// Check answers the same question as Entitled, from the same cache, plans and
// quotas, but it does not turn a store failure into "no access". A subscription
// lookup that fails with anything other than ErrNoActiveSubscription or
// ErrSubscriptionNotFound, and a usage aggregate that fails, return a nil
// result and an error that wraps ErrUnavailable (and the store's own error), so
// a caller can fail open or closed on purpose. A failure caused by the caller's
// own cancelled or expired context comes back as that context error, not as
// ErrUnavailable.
func (l *Ledger) Check(ctx context.Context, featureKey string) (*entitlement.Result, error) {
	return l.checkEntitlement(ctx, featureKey, true)
}

// checkEntitlement is Entitled and Check. strict says whether a store failure
// is an error or a denial.
func (l *Ledger) checkEntitlement(ctx context.Context, featureKey string, strict bool) (*entitlement.Result, error) {
	ctx, span := telemetry.StartChild(ctx, "check entitlement")
	defer span.End()
	// Attributes are built only for a span that records, so a check outside
	// any trace allocates nothing for telemetry.
	if span.IsRecording() {
		span.SetAttributes(attribute.String(attrFeatureKey, featureKey))
	}

	result, source, err := l.entitled(ctx, featureKey, strict)
	answer := resultError
	switch {
	case err != nil:
		telemetry.Fail(span, errorType(err))
	case result.Allowed:
		answer = resultAllowed
	case source == entitlementNoAccess:
		answer = resultNoAccess
	default:
		answer = resultDenied
	}
	hit := source == entitlementCached
	if span.IsRecording() {
		span.SetAttributes(attribute.String(attrEntitlement, answer), attribute.Bool(attrCacheHit, hit))
	}
	cacheLabel := cacheMiss
	if hit {
		cacheLabel = cacheHit
	}
	l.metrics.entitlementChecks.Inc(answer, cacheLabel)
	return result, err
}

// entitled answers Entitled, and says where the answer came from: the cache,
// or which kind of computed answer it was.
func (l *Ledger) entitled(ctx context.Context, featureKey string, strict bool) (*entitlement.Result, entitlementKind, error) {
	tenantID := extractTenantID(ctx)
	appID := extractAppID(ctx)

	if tenantID == "" || appID == "" {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "missing tenant or app context",
		}, entitlementNoAccess, nil
	}

	// Check cache first
	if cached, err := l.store.GetCached(ctx, tenantID, appID, featureKey); err == nil {
		return cached, entitlementCached, nil
	}

	result, kind, err := l.computeEntitlement(ctx, tenantID, appID, featureKey, strict)
	if err != nil {
		return nil, kind, err
	}

	switch kind {
	case entitlementBoolean:
		_ = l.store.SetCached(ctx, tenantID, appID, featureKey, result, l.entitlementCacheTTL) //nolint:errcheck // best-effort cache set
	case entitlementMetered:
		if result.Reason == "quota exceeded" {
			l.plugins.EmitQuotaExceeded(ctx, tenantID, featureKey, result.Used, result.Limit)
		}
		_ = l.store.SetCached(ctx, tenantID, appID, featureKey, result, l.entitlementCacheTTL) //nolint:errcheck // best-effort cache set
		l.plugins.EmitEntitlementChecked(ctx, result)
	case entitlementNoAccess, entitlementCached:
		// Nothing is cached and no event fires when there is no access to report.
	}

	return result, kind, nil
}

// unavailable wraps a store failure in ErrUnavailable for Check. A context
// error is the caller's own cancellation or deadline, not the store being down,
// so it passes through as it is and IsRetryable stays false for it.
func unavailable(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

type entitlementKind int

const (
	entitlementNoAccess entitlementKind = iota // no subscription, no plan, or feature not in plan
	entitlementBoolean
	entitlementMetered
	entitlementCached // answered from the cache; only entitled returns it
)

// computeEntitlement answers an entitlement question from the store alone. It
// reads no cache, writes no cache and fires no plugin events; callers decide.
//
// When strict is false a store failure reads as no access, as it always has.
// When strict is true a failure that is not a plain "not found" returns an
// error wrapping ErrUnavailable.
func (l *Ledger) computeEntitlement(ctx context.Context, tenantID, appID, featureKey string, strict bool) (*entitlement.Result, entitlementKind, error) {
	// Get active subscription
	sub, err := l.store.GetActiveSubscription(ctx, tenantID, appID)
	if err != nil {
		if strict && !errors.Is(err, ErrNoActiveSubscription) && !errors.Is(err, ErrSubscriptionNotFound) {
			return nil, entitlementNoAccess, unavailable(err)
		}

		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "no active subscription",
		}, entitlementNoAccess, nil
	}

	// Get plan
	p, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		if strict && !errors.Is(err, ErrPlanNotFound) {
			return nil, entitlementNoAccess, unavailable(err)
		}

		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "plan not found",
		}, entitlementNoAccess, nil
	}

	// Find feature in plan
	feat := p.FindFeature(featureKey)
	if feat == nil {
		return &entitlement.Result{
			Allowed: false,
			Feature: featureKey,
			Reason:  "feature not in plan",
		}, entitlementNoAccess, nil
	}

	// Boolean feature
	if feat.Type == plan.FeatureBoolean {
		result := &entitlement.Result{
			Allowed: feat.Limit > 0,
			Feature: featureKey,
			Limit:   feat.Limit,
		}
		return result, entitlementBoolean, nil
	}

	// Metered/seat feature
	used, err := l.store.Aggregate(ctx, tenantID, appID, featureKey, feat.Period)
	if err != nil {
		if strict {
			return nil, entitlementMetered, unavailable(err)
		}

		return nil, entitlementMetered, err
	}

	result := &entitlement.Result{
		Feature:   featureKey,
		Used:      used,
		Limit:     feat.Limit,
		Remaining: max(0, feat.Limit-used),
		SoftLimit: feat.SoftLimit,
	}

	switch {
	case feat.Limit == -1:
		result.Allowed = true
		result.Remaining = -1
	case used < feat.Limit:
		result.Allowed = true
	case feat.SoftLimit:
		result.Allowed = true
		result.Reason = "over soft limit"
	default:
		result.Allowed = false
		result.Reason = "quota exceeded"
	}

	return result, entitlementMetered, nil
}

// Remaining returns the remaining quota for a feature.
func (l *Ledger) Remaining(ctx context.Context, featureKey string) (int64, error) {
	result, err := l.Entitled(ctx, featureKey)
	if err != nil {
		return 0, err
	}
	return result.Remaining, nil
}

// ──────────────────────────────────────────────────
// Invoice Generation
// ──────────────────────────────────────────────────

// aggregateUsage totals a feature's usage for billing over window, the part of
// the billed period an invoice counts (see usageWindow). A feature naming a
// registered aggregator under metadata key "aggregator" is aggregated by the
// plugin over that window; anything else is summed from the same events
// (usageInPeriod). An unregistered name falls back to the sum rather than
// failing: a plan referring to a plugin that is not installed should still
// bill.
//
// GenerateInvoice has already refused an empty tenant id. The plugin path
// also refuses an empty app id, because it reads events through QueryUsage,
// which drops the app filter when the app id is empty. The summing path
// skips events from another app itself.
func (l *Ledger) aggregateUsage(ctx context.Context, sub *subscription.Subscription, pf plan.Feature, window subscription.Period) (int64, error) {
	name := pf.Metadata["aggregator"]
	if name == "" {
		return l.usageInPeriod(ctx, sub, pf, window)
	}

	agg := l.plugins.GetUsageAggregator(name)
	if agg == nil {
		l.logger.Warn("ledger: feature names an unregistered usage aggregator; summing its events",
			log.String("aggregator", name),
			log.String("feature", pf.Key),
		)

		return l.usageInPeriod(ctx, sub, pf, window)
	}

	if sub.AppID == "" {
		return 0, fmt.Errorf("%w: usage aggregator %q needs an app id to scope its events, and subscription %s has none",
			ErrInvalidInput, name, sub.ID)
	}

	events, err := l.store.QueryUsage(ctx, sub.TenantID, sub.AppID, meter.QueryOpts{
		FeatureKey: pf.Key,
		Start:      window.Start,
		End:        window.End,
	})
	if err != nil {
		return 0, fmt.Errorf("query usage for aggregator %q: %w", name, err)
	}

	boxed := make([]interface{}, len(events))
	for i := range events {
		boxed[i] = events[i]
	}

	total, err := agg.Aggregate(ctx, boxed)
	if err != nil {
		return 0, fmt.Errorf("usage aggregator %q: %w", name, err)
	}
	// A negative total would reduce billable usage below what the store
	// recorded, and could turn an overage into a silent zero. Refuse it the
	// way a negative tax amount or strategy result is refused.
	if total < 0 {
		return 0, fmt.Errorf("usage aggregator %q returned a negative total %d", name, total)
	}

	return total, nil
}

// priceFeature prices one feature's billable usage in currency, which must
// be the plan's already-lowercased currency as GenerateInvoice normalised it.
// featureTiers must already have passed invoice.ValidateTiers.
//
// A feature naming a registered pricing strategy under metadata key
// "pricing_strategy", or failing that a plan naming one, is priced by the
// plugin. Everything else uses the built-in tier models. The plugin is never
// asked to price usage the allowance covers, and its answer must be a
// non-negative Money in the plan's currency.
func (l *Ledger) priceFeature(p *plan.Plan, pf plan.Feature, featureTiers []plan.PriceTier, currency string, usage, included int64) (types.Money, error) {
	// The feature's own strategy wins over the plan's.
	namedBy := "feature"
	name := pf.Metadata["pricing_strategy"]
	if name == "" {
		namedBy = "plan"
		name = p.Metadata["pricing_strategy"]
	}
	if name == "" {
		return invoice.ComputeOverage(featureTiers, usage, included, currency)
	}

	strategy := l.plugins.GetPricingStrategy(name)
	if strategy == nil {
		l.logger.Warn(fmt.Sprintf("ledger: %s names an unregistered pricing strategy; using the built-in tiers", namedBy),
			log.String("strategy", name),
			log.String("feature", pf.Key),
		)

		return invoice.ComputeOverage(featureTiers, usage, included, currency)
	}

	// Defence in depth: both callers already guarantee this (the metered
	// loop skips a Limit of -1 and any usage at or under the allowance, and
	// the seat loop skips a seat count of zero against an allowance of
	// zero), so today it never fires.
	if included < 0 || usage <= included {
		return types.Zero(currency), nil
	}

	// Hand the strategy a ladder in order, as invoice.ComputeOverage sees it.
	sorted := invoice.SortTiers(featureTiers)
	boxed := make([]interface{}, len(sorted))
	for i := range sorted {
		boxed[i] = sorted[i]
	}

	raw := strategy.Compute(boxed, usage, included, currency)
	amount, ok := raw.(types.Money)
	if !ok {
		return types.Money{}, fmt.Errorf("pricing strategy %q returned %T, want types.Money", name, raw)
	}
	if amount.Currency != "" && !strings.EqualFold(amount.Currency, currency) {
		return types.Money{}, fmt.Errorf("pricing strategy %q returned %s, want %s", name, amount.Currency, currency)
	}
	if amount.IsNegative() {
		return types.Money{}, fmt.Errorf("pricing strategy %q returned a negative amount %v", name, amount)
	}

	return types.Money{Amount: amount.Amount, Currency: currency}, nil
}

// addChecked adds b to a, refusing an overflowing sum. The error names the
// stage that was being built and wraps types.ErrOverflow, so a caller can
// match it with errors.Is.
func addChecked(stage string, a, b types.Money) (types.Money, error) {
	sum, err := a.CheckedAdd(b)
	if err != nil {
		return types.Money{}, fmt.Errorf("%s: %w", stage, err)
	}

	return sum, nil
}

// GenerateInvoice generates an invoice for a subscription period.
//
// It assembles line items in a fixed order: the base fee, then metered
// usage overage per feature in plan order, then seat charges per seat
// feature in plan order, then one discount line per applied coupon, then
// one tax line. Every Money the function builds is normalised to one
// lowercased currency taken from the plan, so two values built from
// different casings of the same currency (e.g. "USD" and "usd") never trip
// Money.Add/Subtract's case-sensitive mismatch panic. Tax is computed on
// the net amount (subtotal less discounts, clamped at zero), not the
// gross subtotal, so a discounted invoice is never taxed on money the
// customer was never charged.
//
// Every sum it builds (subtotal, discount, net, tax and total) is checked.
// An amount that overflows an int64 fails generation with an error wrapping
// types.ErrOverflow that names the stage, and nothing is stored: a wrapped
// figure must never become an invoice.
//
// A subscription with an empty tenant id is refused with an error wrapping
// ErrInvalidInput before anything else is read for it. Every store reads an
// empty tenant as "every tenant", so billing one would total other
// customers' usage. An empty app id is allowed and still bills the base
// fee; only a feature totalled by a plugin aggregator refuses it (see
// aggregateUsage).
//
// Usage is always the billed period's own: the events from its start to its
// end, or to now while it is still running (see usageWindow). That holds for
// the current period and for one named with ForPeriod, so two invoices for
// consecutive periods never count the same event. A feature's reset period
// (plan.Feature.Period) plays no part in billing; it shapes quota checks
// alone.
//
// With ForPeriod it bills a period the subscription has already had instead
// of its current one. Only usage is historical: the plan, its prices, the
// seat counts and the coupons are all read as they are now, because Ledger
// keeps no history of them and ChangePlan leaves the period alone.
func (l *Ledger) GenerateInvoice(ctx context.Context, subID id.SubscriptionID, opts ...InvoiceOption) (inv *invoice.Invoice, err error) {
	start := time.Now()
	ctx, span := telemetry.Start(ctx, "generate invoice",
		trace.WithAttributes(attribute.String(attrSubscriptionID, subID.String())))

	// Pricing strategies, usage aggregators and tax calculators are plugin
	// code and can panic. finished stays false when one does, and the span
	// still ends, marked as a panic.
	finished := false
	defer func() {
		result := resultOK
		switch {
		case !finished:
			result = resultError
		case errors.Is(err, ErrAlreadyExists):
			result = resultExists
		case IsNotFound(err):
			result = resultNotFound
		case err != nil:
			result = resultError
		}
		span.SetAttributes(attribute.String(attrInvoiceResult, result))
		if inv != nil {
			span.SetAttributes(
				attribute.String(attrInvoiceID, inv.ID.String()),
				attribute.Int(attrInvoiceLineItems, len(inv.LineItems)),
			)
		}
		if finished {
			endSpan(span, err)
		} else {
			telemetry.Fail(span, "panic")
			span.End()
		}
		l.metrics.invoiceGenerate.ObserveSince(start, result)
	}()

	inv, err = l.generateInvoice(ctx, subID, opts...)
	finished = true
	return inv, err
}

// generateInvoice is GenerateInvoice without its telemetry.
func (l *Ledger) generateInvoice(ctx context.Context, subID id.SubscriptionID, opts ...InvoiceOption) (*invoice.Invoice, error) {
	sub, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}

	if sub.TenantID == "" {
		return nil, fmt.Errorf("%w: subscription %s has no tenant id", ErrInvalidInput, sub.ID)
	}

	p, err := l.store.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return nil, err
	}

	// A named past period (ForPeriod) is billed on a copy of the subscription
	// carrying that period, so the duplicate check, the invoice's dates and a
	// plugin aggregator's window all read it from one place.
	now := l.now()
	named, err := namedPeriod(sub, p, opts, now)
	if err != nil {
		return nil, err
	}
	periodKind := "current"
	if named != nil {
		periodKind = "named"
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String(attrInvoicePeriod, periodKind))
	// A resume moves the end of a paused subscription's period on by the
	// length of the pause, so that period is not finished: billing it now
	// would bill a period whose end later moves. Ended periods named with
	// ForPeriod are finished and stay billable.
	if named == nil && sub.Status == subscription.StatusPaused {
		return nil, fmt.Errorf("%w: subscription %s is paused; its period is billed when it ends", ErrInvalidInput, sub.ID)
	}
	if named != nil {
		billed := *sub
		billed.CurrentPeriodStart, billed.CurrentPeriodEnd = named.Start, named.End
		sub = &billed
	}

	existing, err := l.liveInvoiceForPeriod(ctx, sub)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: invoice %s already covers this billing period", ErrAlreadyExists, existing.ID)
	}
	window := usageWindow(subscription.Period{Start: sub.CurrentPeriodStart, End: sub.CurrentPeriodEnd}, now)

	currency := strings.ToLower(p.Currency)

	inv := &invoice.Invoice{
		Entity:         types.NewEntity(),
		ID:             id.NewInvoiceID(),
		TenantID:       sub.TenantID,
		SubscriptionID: sub.ID,
		Status:         invoice.StatusDraft,
		Currency:       currency,
		Subtotal:       types.Zero(currency),
		TaxAmount:      types.Zero(currency),
		DiscountAmount: types.Zero(currency),
		Total:          types.Zero(currency),
		PeriodStart:    sub.CurrentPeriodStart,
		PeriodEnd:      sub.CurrentPeriodEnd,
		AppID:          sub.AppID,
		LineItems:      []invoice.LineItem{},
	}

	// 1. Base subscription fee. A base price stored in a currency other
	// than the plan's is a catalogue error, not something to panic over:
	// reject it before it ever reaches Money.Add. A negative base price is a
	// catalogue error too, and is refused rather than skipped: skipping it
	// would invoice the plan as though it had no base fee at all.
	if p.Pricing != nil && p.Pricing.BaseAmount.IsNegative() {
		return nil, fmt.Errorf("%w: plan %s base price %v is negative",
			ErrInvalidPricing, p.ID, p.Pricing.BaseAmount)
	}
	if p.Pricing != nil && p.Pricing.BaseAmount.IsPositive() {
		if p.Pricing.BaseAmount.Currency != "" && !strings.EqualFold(p.Pricing.BaseAmount.Currency, currency) {
			return nil, fmt.Errorf("%w: plan %s base price is in %q, plan bills in %q",
				ErrInvalidPricing, p.ID, p.Pricing.BaseAmount.Currency, p.Currency)
		}

		base := types.Money{Amount: p.Pricing.BaseAmount.Amount, Currency: currency}
		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			Description: "Base subscription fee",
			Quantity:    1,
			UnitAmount:  base,
			Amount:      base,
			Type:        invoice.LineItemBase,
		})
		subtotal, addErr := addChecked("subtotal: adding the base fee", inv.Subtotal, base)
		if addErr != nil {
			return nil, addErr
		}
		inv.Subtotal = subtotal
	}

	// 2. Metered usage overage, priced from the plan's tiers.
	var tiers []plan.PriceTier
	if p.Pricing != nil {
		tiers = p.Pricing.Tiers
	}

	for _, pf := range p.Features {
		if pf.Type != plan.FeatureMetered {
			continue
		}

		// An unlimited allowance can never bill, so this feature is
		// skipped entirely, including ladder validation: a catalogue
		// mistake on a feature nobody can ever be charged for should not
		// fail every invoice on the plan.
		if pf.Limit < 0 {
			continue
		}

		featureTiers := tiersFor(tiers, pf.Key)
		if vErr := invoice.ValidateTiers(featureTiers, currency); vErr != nil {
			return nil, fmt.Errorf("plan %s feature %q: %w", p.ID, pf.Key, vErr)
		}

		used, aggErr := l.aggregateUsage(ctx, sub, pf, window)
		if aggErr != nil {
			return nil, fmt.Errorf("aggregate usage for feature %q: %w", pf.Key, aggErr)
		}

		billable := used - pf.Limit
		if billable <= 0 {
			continue
		}

		amount, priceErr := l.priceFeature(p, pf, featureTiers, currency, used, pf.Limit)
		if priceErr != nil {
			return nil, fmt.Errorf("price feature %q: %w", pf.Key, priceErr)
		}
		if amount.IsZero() {
			continue
		}

		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			FeatureKey:  pf.Key,
			Description: pf.Name + " overage",
			Quantity:    billable,
			UnitAmount:  types.Zero(currency),
			Amount:      amount,
			Type:        invoice.LineItemOverage,
		})
		subtotal, addErr := addChecked(fmt.Sprintf("subtotal: adding feature %q overage", pf.Key), inv.Subtotal, amount)
		if addErr != nil {
			return nil, addErr
		}
		inv.Subtotal = subtotal
	}

	// 3. Seat charges, from the quantities carried on the subscription.
	// ComputeOverage prices the full seat count against an allowance of
	// zero, so every seat is billed rather than only the seats past some
	// included count: seat features do not carry an allowance, and the
	// feature's own Limit plays no part in pricing them. For the same
	// reason a seat feature with Limit -1 is not skipped, unlike a metered
	// one.
	for _, pf := range p.Features {
		if pf.Type != plan.FeatureSeat {
			continue
		}

		featureTiers := tiersFor(tiers, pf.Key)
		if vErr := invoice.ValidateTiers(featureTiers, currency); vErr != nil {
			return nil, fmt.Errorf("plan %s feature %q: %w", p.ID, pf.Key, vErr)
		}

		seats := sub.Quantity[pf.Key]
		if seats <= 0 {
			continue
		}

		amount, priceErr := l.priceFeature(p, pf, featureTiers, currency, seats, 0)
		if priceErr != nil {
			return nil, fmt.Errorf("price feature %q: %w", pf.Key, priceErr)
		}
		if amount.IsZero() {
			continue
		}

		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			FeatureKey:  pf.Key,
			Description: pf.Name,
			Quantity:    seats,
			UnitAmount:  types.Zero(currency),
			Amount:      amount,
			Type:        invoice.LineItemSeat,
		})
		subtotal, addErr := addChecked(fmt.Sprintf("subtotal: adding feature %q seats", pf.Key), inv.Subtotal, amount)
		if addErr != nil {
			return nil, addErr
		}
		inv.Subtotal = subtotal
	}

	// 4. Coupon discounts. Percentage coupons compute against the subtotal
	// as it stood before any discount, so two stacked percentages do not
	// compound. Amount coupons subtract flat. Each coupon is validated again
	// below (range, sign, type and currency against the plan) because it may
	// have been attached by a path other than ApplyCoupon, or the plan may
	// have changed since it was applied. That per-coupon currency check is
	// what keeps Money.Add from panicking here.
	applied, couponErr := l.store.ListAppliedCoupons(ctx, sub.ID)
	if couponErr != nil {
		return nil, fmt.Errorf("list applied coupons: %w", couponErr)
	}

	discountBase := inv.Subtotal
	for _, c := range applied {
		// A coupon reaching here did not necessarily pass through
		// ApplyCoupon's own checks: it may have been recorded by another
		// path (a direct store write, a migration, a different caller
		// entirely). Re-validate its shape before it ever discounts
		// anything, rather than trusting that every application row was
		// produced by the one guarded entry point.
		var amount types.Money
		switch c.Type {
		case coupon.CouponTypePercentage:
			if c.Percentage < 0 || c.Percentage > 100 {
				return nil, fmt.Errorf("%w: coupon %q has percentage %d out of range 0..100",
					ErrCouponInvalid, c.Code, c.Percentage)
			}
			pct, pctErr := discountBase.CheckedPercent(c.Percentage)
			if pctErr != nil {
				return nil, fmt.Errorf("discount: coupon %q at %d%%: %w", c.Code, c.Percentage, pctErr)
			}
			amount = pct
		case coupon.CouponTypeAmount:
			if c.Amount.Amount < 0 {
				return nil, fmt.Errorf("%w: coupon %q has a negative amount %v",
					ErrCouponInvalid, c.Code, c.Amount)
			}

			// The effective currency is whatever Subtract will actually
			// operate on: the Money's own currency first, falling back to
			// the coupon's label only when the Money carries none. A plan
			// whose currency drifted after this coupon was applied (a
			// $5.00 coupon left attached to a plan since switched to JPY)
			// must not silently become a five-unit discount in the new
			// currency.
			effectiveCurrency := strings.ToLower(c.Amount.Currency)
			if effectiveCurrency == "" {
				effectiveCurrency = strings.ToLower(c.Currency)
			}
			if effectiveCurrency != currency {
				return nil, fmt.Errorf("%w: coupon %q is in %q, plan bills in %q",
					ErrCouponInvalid, c.Code, effectiveCurrency, currency)
			}

			amount = types.Money{Amount: c.Amount.Amount, Currency: currency}
		default:
			return nil, fmt.Errorf("%w: coupon %q has unsupported type %q",
				ErrCouponInvalid, c.Code, c.Type)
		}
		if amount.IsZero() {
			continue
		}

		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			Description: "Discount " + c.Code,
			Quantity:    1,
			UnitAmount:  amount.Negate(),
			Amount:      amount.Negate(),
			Type:        invoice.LineItemDiscount,
		})
		discount, addErr := addChecked(fmt.Sprintf("discount: adding coupon %q", c.Code), inv.DiscountAmount, amount)
		if addErr != nil {
			return nil, addErr
		}
		inv.DiscountAmount = discount
	}

	// The net amount is what tax is actually charged on, and what the
	// final total is built from. It is clamped at zero here, before tax:
	// Ledger has no refund path, so a discount larger than the bill must
	// produce a free invoice, never a negative one.
	net, netErr := inv.Subtotal.CheckedSubtract(inv.DiscountAmount)
	if netErr != nil {
		return nil, fmt.Errorf("net amount: %w", netErr)
	}
	if net.IsNegative() {
		net = types.Zero(currency)
	}

	// 5. Tax, from whichever plugins provide it, computed on the net
	// amount. The interface returns interface{}, so a plugin that hands
	// back the wrong type fails generation rather than silently
	// contributing nothing.
	for _, tc := range l.plugins.GetTaxCalculators() {
		raw, taxErr := tc.CalculateTax(ctx, net, sub.TenantID)
		if taxErr != nil {
			return nil, fmt.Errorf("tax calculator %q: %w", tc.Name(), taxErr)
		}
		if raw == nil {
			continue
		}

		amount, ok := raw.(types.Money)
		if !ok {
			return nil, fmt.Errorf("tax calculator %q returned %T, want types.Money", tc.Name(), raw)
		}
		if amount.Currency != "" && !strings.EqualFold(amount.Currency, currency) {
			return nil, fmt.Errorf("tax calculator %q returned %s, want %s",
				tc.Name(), amount.Currency, currency)
		}
		// A negative tax amount would reduce the bill, not tax it, and
		// could drive Total below the net amount that was already clamped
		// at zero above. Refuse it the same way a wrong type is refused: a
		// zero or nil result stays legal and simply adds no tax line.
		if amount.IsNegative() {
			return nil, fmt.Errorf("tax calculator %q returned a negative tax amount %v", tc.Name(), amount)
		}

		taxTotal, addErr := addChecked(fmt.Sprintf("tax: adding calculator %q", tc.Name()),
			inv.TaxAmount, types.Money{Amount: amount.Amount, Currency: currency})
		if addErr != nil {
			return nil, addErr
		}
		inv.TaxAmount = taxTotal
	}

	if inv.TaxAmount.IsPositive() {
		inv.LineItems = append(inv.LineItems, invoice.LineItem{
			ID:          id.NewLineItemID(),
			InvoiceID:   inv.ID,
			Description: "Tax",
			Quantity:    1,
			UnitAmount:  inv.TaxAmount,
			Amount:      inv.TaxAmount,
			Type:        invoice.LineItemTax,
		})
	}

	// Total is the already-clamped net amount plus tax.
	total, totalErr := addChecked("invoice total: adding tax to the net amount", net, inv.TaxAmount)
	if totalErr != nil {
		return nil, totalErr
	}
	inv.Total = total

	// Save invoice
	if err := l.store.CreateInvoice(ctx, inv); err != nil {
		return nil, err
	}

	l.plugins.EmitInvoiceGenerated(ctx, inv)
	return inv, nil
}

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

// tiersFor returns the tiers belonging to one feature key.
func tiersFor(tiers []plan.PriceTier, featureKey string) []plan.PriceTier {
	out := make([]plan.PriceTier, 0, len(tiers))
	for _, t := range tiers {
		if t.FeatureKey == featureKey {
			out = append(out, t)
		}
	}

	return out
}

// ──────────────────────────────────────────────────
// Provider Sync & Payment Methods
// ──────────────────────────────────────────────────

// HasProviders returns true if any payment provider plugins are registered.
func (l *Ledger) HasProviders() bool {
	return l.plugins.HasPaymentProviders()
}

// getProvider resolves a payment provider by name. If name is empty, returns
// the first registered provider. Returns ErrProviderNotConfigured when no
// providers are registered at all.
func (l *Ledger) getProvider(name string) (provider.Provider, error) {
	if !l.plugins.HasPaymentProviders() {
		return nil, ErrProviderNotConfigured
	}

	if name != "" {
		pp := l.plugins.GetPaymentProvider(name)
		if pp == nil {
			return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
		}
		return pp.Provider(), nil
	}

	// Default to first registered provider
	providers := l.plugins.GetPaymentProviders()
	if len(providers) == 0 {
		return nil, ErrProviderNotConfigured
	}
	return providers[0].Provider(), nil
}

// providerFor resolves a provider as getProvider does, and names it on the
// span in ctx.
func (l *Ledger) providerFor(ctx context.Context, name string) (provider.Provider, error) {
	prov, err := l.getProvider(name)
	if err == nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String(attrProviderName, prov.Name()))
	}
	return prov, err
}

// callProvider makes one call into a payment provider plugin and records how
// long it took. operation is one of the documented operation label values.
func callProvider[T any](l *Ledger, prov provider.Provider, operation string, call func() (T, error)) (T, error) {
	start := time.Now()
	v, err := call()
	l.metrics.providerCalls.ObserveSince(start, prov.Name(), operation, resultOf(err))
	return v, err
}

// ListPaymentMethods lists payment methods from the provider for a tenant.
// Payment methods are always fetched live and never stored locally.
func (l *Ledger) ListPaymentMethods(ctx context.Context, tenantID string) (methods []provider.PaymentMethod, err error) {
	ctx, span := telemetry.Start(ctx, "list payment methods")
	defer func() { endSpan(span, err) }()

	prov, err := l.providerFor(ctx, "")
	if err != nil {
		// No providers configured — return empty list (dev mode)
		if errors.Is(err, ErrProviderNotConfigured) {
			return nil, nil
		}
		return nil, err
	}

	return callProvider(l, prov, "list_payment_methods", func() ([]provider.PaymentMethod, error) {
		return prov.ListPaymentMethods(ctx, tenantID)
	})
}

// SyncPlanToProvider pushes a local plan to the payment provider.
func (l *Ledger) SyncPlanToProvider(ctx context.Context, planID id.PlanID) (res *provider.SyncResult, err error) {
	ctx, span := telemetry.Start(ctx, "sync plan",
		trace.WithAttributes(attribute.String(attrPlanID, planID.String())))
	defer func() { endSpan(span, err) }()

	p, err := l.store.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}

	prov, err := l.providerFor(ctx, p.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := callProvider(l, prov, "sync_plan", func() (string, error) {
		return prov.SyncPlan(ctx, p)
	})
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "plan",
		EntityID:     planID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	// Update local entity with provider tracking
	if syncErr == nil {
		p.ProviderID = providerID
		p.ProviderName = prov.Name()
		_ = l.store.UpdatePlan(ctx, p) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncFeatureToProvider pushes a local feature to the payment provider.
func (l *Ledger) SyncFeatureToProvider(ctx context.Context, featureID id.FeatureID) (res *provider.SyncResult, err error) {
	ctx, span := telemetry.Start(ctx, "sync feature",
		trace.WithAttributes(attribute.String(attrFeatureID, featureID.String())))
	defer func() { endSpan(span, err) }()

	f, err := l.store.GetFeature(ctx, featureID)
	if err != nil {
		return nil, err
	}

	prov, err := l.providerFor(ctx, f.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := callProvider(l, prov, "sync_feature", func() (string, error) {
		return prov.SyncFeature(ctx, f)
	})
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "feature",
		EntityID:     featureID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		f.ProviderID = providerID
		f.ProviderName = prov.Name()
		_ = l.store.UpdateFeature(ctx, f) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncSubscriptionToProvider pushes a local subscription to the payment provider.
func (l *Ledger) SyncSubscriptionToProvider(ctx context.Context, subID id.SubscriptionID) (res *provider.SyncResult, err error) {
	ctx, span := telemetry.Start(ctx, "sync subscription",
		trace.WithAttributes(attribute.String(attrSubscriptionID, subID.String())))
	defer func() { endSpan(span, err) }()

	s, err := l.store.GetSubscription(ctx, subID)
	if err != nil {
		return nil, err
	}

	prov, err := l.providerFor(ctx, s.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := callProvider(l, prov, "sync_subscription", func() (string, error) {
		return prov.SyncSubscription(ctx, s)
	})
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "subscription",
		EntityID:     subID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		s.ProviderID = providerID
		s.ProviderName = prov.Name()
		// Only the provider columns: the sync took a network round trip, and
		// the clock or an operator may have written the row meanwhile.
		_ = l.store.SetSubscriptionProvider(ctx, subID, providerID, s.ProviderName) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// SyncInvoiceToProvider pushes a local invoice to the payment provider.
func (l *Ledger) SyncInvoiceToProvider(ctx context.Context, invID id.InvoiceID) (res *provider.SyncResult, err error) {
	ctx, span := telemetry.Start(ctx, "sync invoice",
		trace.WithAttributes(attribute.String(attrInvoiceID, invID.String())))
	defer func() { endSpan(span, err) }()

	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return nil, err
	}

	prov, err := l.providerFor(ctx, inv.ProviderName)
	if err != nil {
		return nil, err
	}

	providerID, syncErr := callProvider(l, prov, "sync_invoice", func() (string, error) {
		return prov.SyncInvoice(ctx, inv)
	})
	result := &provider.SyncResult{
		ProviderName: prov.Name(),
		ProviderID:   providerID,
		EntityType:   "invoice",
		EntityID:     invID.String(),
		Direction:    "push",
		Success:      syncErr == nil,
	}
	if syncErr != nil {
		result.Error = syncErr.Error()
	}

	if syncErr == nil {
		inv.ProviderID = providerID
		inv.ProviderName = prov.Name()
		// Only the provider columns: the sync took a network round trip, and
		// the clock may have marked the invoice past due meanwhile, or an
		// operator paid or voided it.
		_ = l.store.SetInvoiceProvider(ctx, invID, providerID, inv.ProviderName) //nolint:errcheck // best-effort update
	}

	l.plugins.EmitProviderSync(ctx, prov.Name(), syncErr == nil, syncErr)
	return result, syncErr
}

// ──────────────────────────────────────────────────
// Webhook Reconciliation
// ──────────────────────────────────────────────────

// HandleWebhook routes an incoming webhook payload to the correct provider.
func (l *Ledger) HandleWebhook(ctx context.Context, providerName string, payload []byte) (res *provider.WebhookResult, err error) {
	ctx, span := telemetry.Start(ctx, "handle webhook")
	defer func() { endSpan(span, err) }()

	prov, err := l.providerFor(ctx, providerName)
	if err != nil {
		return nil, err
	}

	l.plugins.EmitWebhookReceived(ctx, prov.Name(), payload)

	result, err := callProvider(l, prov, "handle_webhook", func() (*provider.WebhookResult, error) {
		return prov.HandleWebhook(ctx, payload)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderWebhook, err)
	}
	if result != nil {
		span.SetAttributes(
			attribute.String(attrWebhookEvent, result.EventType),
			attribute.Bool(attrWebhookHandled, result.Handled),
		)
	}

	return result, nil
}

// ──────────────────────────────────────────────────
// Invoice Lifecycle
// ──────────────────────────────────────────────────

// FinalizeInvoice transitions an invoice from draft to pending.
func (l *Ledger) FinalizeInvoice(ctx context.Context, invID id.InvoiceID) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status != invoice.StatusDraft {
		return ErrInvoiceFinalized
	}

	inv.Status = invoice.StatusPending
	now := l.now()
	dueDate := now.AddDate(0, 0, 30) // 30-day payment terms
	inv.DueDate = &dueDate

	if err := l.store.UpdateInvoice(ctx, inv); err != nil {
		return err
	}

	l.plugins.EmitInvoiceFinalized(ctx, inv)
	return nil
}

// MarkInvoicePaid marks an invoice as paid.
func (l *Ledger) MarkInvoicePaid(ctx context.Context, invID id.InvoiceID, paidAt time.Time, paymentRef string) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status == invoice.StatusPaid {
		return ErrInvoicePaid
	}
	if inv.Status == invoice.StatusVoided {
		return ErrInvoiceVoided
	}

	if err := l.store.MarkInvoicePaid(ctx, invID, paidAt, paymentRef); err != nil {
		return err
	}

	inv.Status = invoice.StatusPaid
	inv.PaidAt = &paidAt
	inv.PaymentRef = paymentRef
	l.plugins.EmitInvoicePaid(ctx, inv)
	return nil
}

// MarkInvoiceVoided marks an invoice as voided with a reason.
func (l *Ledger) MarkInvoiceVoided(ctx context.Context, invID id.InvoiceID, reason string) error {
	inv, err := l.store.GetInvoice(ctx, invID)
	if err != nil {
		return err
	}

	if inv.Status == invoice.StatusPaid {
		return ErrInvoicePaid
	}
	if inv.Status == invoice.StatusVoided {
		return ErrInvoiceVoided
	}

	if err := l.store.MarkInvoiceVoided(ctx, invID, reason); err != nil {
		return err
	}

	now := time.Now().UTC()
	inv.Status = invoice.StatusVoided
	inv.VoidedAt = &now
	inv.VoidReason = reason
	l.plugins.EmitInvoiceVoided(ctx, inv, reason)
	return nil
}
