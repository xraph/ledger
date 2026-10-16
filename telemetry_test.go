package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/internal/telemetrytest"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/provider"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

// leak stands in for anything a span must never carry: a tenant that is a
// person, a provider's message, a webhook body. Tests feed it through and
// check it reached no span.
const leak = "alice@example.com"

const scope = "github.com/xraph/ledger"

func testTracer() trace.Tracer { return otel.Tracer("ledger-test") }

// assertChildOf fails unless s is an internal span of ledger's scope whose
// parent is parent.
func assertChildOf(t *testing.T, s sdktrace.ReadOnlySpan, parent trace.SpanContext) {
	t.Helper()
	if s.Parent().SpanID() != parent.SpanID() || s.SpanContext().TraceID() != parent.TraceID() {
		t.Errorf("%q is not a child of the span it ran under", s.Name())
	}
	if s.SpanKind() != trace.SpanKindInternal {
		t.Errorf("%q kind %v, want internal", s.Name(), s.SpanKind())
	}
	if s.InstrumentationScope().Name != scope {
		t.Errorf("%q scope %q, want %q", s.Name(), s.InstrumentationScope().Name, scope)
	}
}

// assertRoot fails unless s begins a trace, one that is not other's.
func assertRoot(t *testing.T, s sdktrace.ReadOnlySpan, other trace.SpanContext) {
	t.Helper()
	if s.Parent().IsValid() {
		t.Errorf("%q has a parent; a background unit of work must begin its own trace", s.Name())
	}
	if s.SpanContext().TraceID() == other.TraceID() {
		t.Errorf("%q joined the trace of the context its worker was started with", s.Name())
	}
}

// assertFailed fails unless s is marked failed with typ, and its only event
// is an exception naming typ and nothing else.
func assertFailed(t *testing.T, s sdktrace.ReadOnlySpan, typ string) {
	t.Helper()
	if s.Status().Code != codes.Error || s.Status().Description != typ {
		t.Errorf("%q status %v %q, want error %q", s.Name(), s.Status().Code, s.Status().Description, typ)
	}
	events := s.Events()
	if len(events) != 1 || events[0].Name != "exception" || len(events[0].Attributes) != 1 ||
		events[0].Attributes[0].Key != "exception.type" || events[0].Attributes[0].Value.AsString() != typ {
		t.Errorf("%q events %+v, want one exception event carrying only exception.type %q", s.Name(), events, typ)
	}
}

func assertOK(t *testing.T, s sdktrace.ReadOnlySpan) {
	t.Helper()
	if s.Status().Code == codes.Error || len(s.Events()) != 0 {
		t.Errorf("%q marked failed: %v %q, events %v", s.Name(), s.Status().Code, s.Status().Description, s.Events())
	}
}

func assertInt(t *testing.T, s sdktrace.ReadOnlySpan, key string, want int64) {
	t.Helper()
	if v, ok := telemetrytest.Attr(s, key); !ok || v.AsInt64() != want {
		t.Errorf("%q %s = %v (set %v), want %d", s.Name(), key, v.String(), ok, want)
	}
}

func assertString(t *testing.T, s sdktrace.ReadOnlySpan, key, want string) {
	t.Helper()
	if v, ok := telemetrytest.Attr(s, key); !ok || v.AsString() != want {
		t.Errorf("%q %s = %q (set %v), want %q", s.Name(), key, v.String(), ok, want)
	}
}

func assertBool(t *testing.T, s sdktrace.ReadOnlySpan, key string, want bool) {
	t.Helper()
	if v, ok := telemetrytest.Attr(s, key); !ok || v.AsBool() != want {
		t.Errorf("%q %s = %v (set %v), want %v", s.Name(), key, v.String(), ok, want)
	}
}

func assertValue(t *testing.T, sink *telemetrytest.Sink, series string, want float64) {
	t.Helper()
	if got := sink.Value(series); got != want {
		t.Errorf("%s = %v, want %v (series: %v)", series, got, want, sink.Series())
	}
}

func assertObservations(t *testing.T, sink *telemetrytest.Sink, series string, want uint64) {
	t.Helper()
	if got := sink.Observations(series); got != want {
		t.Errorf("%s observed %d times, want %d (series: %v)", series, got, want, sink.Series())
	}
}

func TestAdvanceTracesARunThatMovesSomething(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	s := memory.New()
	l := ledger.New(s, ledger.WithMetrics(sink))
	p := activePlan(t, l, "pro", "app_1", 0)
	// A cancel inside the January period: the period step has nothing to
	// write, the cancel step has one.
	seedSub(t, s, p, func(x *subscription.Subscription) {
		x.TenantID = leak
		x.CancelAt = ptr(at(2026, 1, 20))
	})

	ctx, parent := testTracer().Start(context.Background(), "nightly job")
	report, err := l.Advance(ctx, at(2026, 3, 1))
	parent.End()
	if err != nil || len(report.CancelsEnacted) != 1 {
		t.Fatalf("Advance: %+v, %v", report, err)
	}

	run := telemetrytest.Only(t, rec, "run lifecycle")
	assertChildOf(t, run, parent.SpanContext())
	assertOK(t, run)
	assertInt(t, run, "ledger.lifecycle.cancels_enacted", 1)
	assertInt(t, run, "ledger.lifecycle.periods_advanced", 0)
	assertInt(t, run, "ledger.lifecycle.trials_ended", 0)
	assertInt(t, run, "ledger.lifecycle.invoices_past_due", 0)

	step := telemetrytest.Only(t, rec, "enact cancels")
	assertChildOf(t, step, run.SpanContext())
	assertInt(t, step, "ledger.lifecycle.changed", 1)
	assertInt(t, step, "ledger.lifecycle.failed", 0)
	for _, idle := range []string{"advance periods", "end trials", "mark invoices past due"} {
		if n := len(telemetrytest.Named(rec, idle)); n != 0 {
			t.Errorf("%d %q spans for a step with nothing to write", n, idle)
		}
	}
	telemetrytest.AssertNoValue(t, rec, leak)

	assertValue(t, sink, `ledger_lifecycle_transitions_total{result="ok",step="enact_cancels"}`, 1)
	assertObservations(t, sink, `ledger_lifecycle_run_duration_seconds{result="ok"}`, 1)
	sink.AssertOneLabelSet(t)
}

func TestAdvanceThatMovesNothingStartsNoSpan(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	s := memory.New()
	l := ledger.New(s, ledger.WithMetrics(sink))
	// A one-off plan's subscription stays in the due set after its period
	// ends, and every run lists it again. Listing it is not work.
	once := planBilled(t, l, "once", plan.PeriodNone)
	seedSub(t, s, once, func(*subscription.Subscription) {})

	ctx, parent := testTracer().Start(context.Background(), "nightly job")
	if _, err := l.Advance(ctx, at(2026, 3, 1)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	parent.End()

	if names := telemetrytest.Names(rec); len(names) != 1 || names[0] != "nightly job" {
		t.Errorf("spans %v, want only the caller's", names)
	}
	assertObservations(t, sink, `ledger_lifecycle_run_duration_seconds{result="ok"}`, 1)
	for _, series := range sink.Series() {
		if series != `ledger_lifecycle_run_duration_seconds{result="ok"}` {
			t.Errorf("an idle run recorded %s", series)
		}
	}
}

// failingLists fails every subscription list in words that quote a tenant.
type failingLists struct{ *memory.Store }

func (failingLists) ListDueSubscriptions(context.Context, subscription.DueOpts) ([]*subscription.Subscription, error) {
	return nil, fmt.Errorf("due query for tenant %s failed", leak)
}

func TestAFailedRunIsTracedByTypeNotMessage(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	l := ledger.New(failingLists{memory.New()}, ledger.WithMetrics(sink))

	ctx, parent := testTracer().Start(context.Background(), "nightly job")
	if _, err := l.Advance(ctx, at(2026, 3, 1)); err == nil {
		t.Fatal("Advance succeeded over a failing store")
	}
	parent.End()

	run := telemetrytest.Only(t, rec, "run lifecycle")
	assertFailed(t, run, "*errors.errorString")
	for _, name := range []string{"advance periods", "enact cancels", "end trials"} {
		step := telemetrytest.Only(t, rec, name)
		assertChildOf(t, step, run.SpanContext())
		assertFailed(t, step, "*errors.errorString")
	}
	telemetrytest.AssertNoValue(t, rec, leak)
	assertObservations(t, sink, `ledger_lifecycle_run_duration_seconds{result="error"}`, 1)
}

// cancelSignal tells the test when the clock has enacted a cancel.
type cancelSignal struct{ fired chan struct{} }

func (c *cancelSignal) Name() string { return "cancel-signal" }

func (c *cancelSignal) OnSubscriptionCanceled(context.Context, interface{}) error {
	select {
	case c.fired <- struct{}{}:
	default:
	}
	return nil
}

func TestTheClockTracesEachRunAsItsOwnTrace(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	s := memory.New()
	signal := &cancelSignal{fired: make(chan struct{}, 1)}
	l := ledger.New(s,
		ledger.WithPlugin(signal),
		ledger.WithLifecycleInterval(5*time.Millisecond),
		ledger.WithClock(func() time.Time { return at(2026, 3, 1) }),
	)
	p := activePlan(t, l, "pro", "app_1", 0)
	seedSub(t, s, p, func(x *subscription.Subscription) { x.CancelAt = ptr(at(2026, 1, 20)) })

	// Forge hands Start a context carrying its boot span.
	bootCtx, boot := testTracer().Start(context.Background(), "start ledger")
	if err := l.Start(bootCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	boot.End()
	select {
	case <-signal.fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the clock did not enact the cancel")
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Only the run that moved something is traced; the idle ticks around it
	// are not.
	run := telemetrytest.Only(t, rec, "run lifecycle")
	assertRoot(t, run, boot.SpanContext())
	if run.Status().Code == codes.Error {
		t.Errorf("run marked failed: %q", run.Status().Description)
	}
	assertChildOf(t, telemetrytest.Only(t, rec, "enact cancels"), run.SpanContext())
}

// signalingFlush tells the test when a flush has stored its batch.
type signalingFlush struct{ flushed chan int }

func (f *signalingFlush) Name() string { return "flush-signal" }

func (f *signalingFlush) OnUsageFlushed(_ context.Context, n int, _ time.Duration) error {
	f.flushed <- n
	return nil
}

func meterContext() context.Context {
	// Meter reads these string keys today.
	ctx := context.WithValue(context.Background(), "tenant_id", leak)
	// Meter reads these string keys today.
	return context.WithValue(ctx, "app_id", "app_1")
}

func TestEachMeterFlushIsATraceOfItsOwn(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	signal := &signalingFlush{flushed: make(chan int, 1)}
	l := ledger.New(memory.New(),
		ledger.WithMetrics(sink),
		ledger.WithPlugin(signal),
		ledger.WithLifecycleInterval(0),
		ledger.WithMeterConfig(3, time.Hour),
	)
	bootCtx, boot := testTracer().Start(context.Background(), "start ledger")
	if err := l.Start(bootCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	boot.End()

	for range 3 {
		if err := l.Meter(meterContext(), "api_calls", 1); err != nil {
			t.Fatalf("Meter: %v", err)
		}
	}
	select {
	case <-signal.flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("the batch was not flushed")
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	flush := telemetrytest.Only(t, rec, "flush meter batch")
	assertRoot(t, flush, boot.SpanContext())
	assertOK(t, flush)
	assertInt(t, flush, "ledger.meter.batch_size", 3)
	telemetrytest.AssertNoValue(t, rec, leak)
	assertValue(t, sink, `ledger_meter_events_total{result="ok"}`, 3)
}

// failingIngest refuses every batch in words that quote a tenant, and says
// when it has.
type failingIngest struct {
	*memory.Store
	refused chan struct{}
}

func (f failingIngest) IngestBatch(context.Context, []*meter.UsageEvent) error {
	defer func() { f.refused <- struct{}{} }()
	return fmt.Errorf("insert for %s: %w", leak, errors.ErrUnsupported)
}

func TestAFailedFlushCountsTheEventsItLost(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	st := failingIngest{Store: memory.New(), refused: make(chan struct{}, 1)}
	l := ledger.New(st, ledger.WithMetrics(sink), ledger.WithLifecycleInterval(0), ledger.WithMeterConfig(2, time.Hour))
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for range 2 {
		if err := l.Meter(meterContext(), "api_calls", 1); err != nil {
			t.Fatalf("Meter: %v", err)
		}
	}
	select {
	case <-st.refused:
	case <-time.After(5 * time.Second):
		t.Fatal("the batch was never written")
	}
	if err := l.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The innermost error is the one named.
	assertFailed(t, telemetrytest.Only(t, rec, "flush meter batch"), "*errors.errorString")
	telemetrytest.AssertNoValue(t, rec, leak)
	assertValue(t, sink, `ledger_meter_events_total{result="error"}`, 2)
}

func TestAFullMeterBufferIsCounted(t *testing.T) {
	sink := telemetrytest.NewSink()
	// Not started, so nothing drains the buffer.
	l := ledger.New(memory.New(), ledger.WithMetrics(sink))
	for i := 0; ; i++ {
		err := l.Meter(meterContext(), "api_calls", 1)
		if errors.Is(err, ledger.ErrMeterBufferFull) {
			break
		}
		if err != nil || i > 1_000_000 {
			t.Fatalf("Meter after %d events: %v", i, err)
		}
	}
	assertValue(t, sink, `ledger_meter_events_total{result="buffer_full"}`, 1)
}

func TestEntitledIsTracedOnlyUnderACallersSpan(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	l := ledger.New(memory.New(), ledger.WithMetrics(sink))
	p := activePlan(t, l, "pro", "app_1", 0)
	if err := l.CreateSubscription(context.Background(), &subscription.Subscription{TenantID: leak, PlanID: p.ID, AppID: "app_1"}); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	ctx := meterContext()

	// Outside any trace: an answer, a count, no span.
	if got, err := l.Entitled(ctx, "api_calls"); err != nil || !got.Allowed {
		t.Fatalf("Entitled: %+v, %v", got, err)
	}
	if names := telemetrytest.Names(rec); len(names) != 0 {
		t.Fatalf("a check outside any trace started spans: %v", names)
	}

	reqCtx, req := testTracer().Start(ctx, "GET /reports")
	if got, err := l.Entitled(reqCtx, "api_calls"); err != nil || !got.Allowed {
		t.Fatalf("Entitled (cached): %+v, %v", got, err)
	}
	if got, err := l.Entitled(reqCtx, "exports"); err != nil || got.Allowed {
		t.Fatalf("Entitled (not in plan): %+v, %v", got, err)
	}
	req.End()

	checks := telemetrytest.Named(rec, "check entitlement")
	if len(checks) != 2 {
		t.Fatalf("want two check spans, got %v", telemetrytest.Names(rec))
	}
	for _, c := range checks {
		assertChildOf(t, c, req.SpanContext())
		assertOK(t, c)
	}
	assertString(t, checks[0], "ledger.feature.key", "api_calls")
	assertString(t, checks[0], "ledger.entitlement.result", "allowed")
	assertBool(t, checks[0], "ledger.entitlement.cache_hit", true)
	assertString(t, checks[1], "ledger.feature.key", "exports")
	assertString(t, checks[1], "ledger.entitlement.result", "no_access")
	assertBool(t, checks[1], "ledger.entitlement.cache_hit", false)
	telemetrytest.AssertNoValue(t, rec, leak)

	// No tenant in the context is no access too.
	if got, err := l.Entitled(context.Background(), "api_calls"); err != nil || got.Allowed {
		t.Fatalf("Entitled (no tenant): %+v, %v", got, err)
	}
	assertValue(t, sink, `ledger_entitlement_checks_total{cache="miss",result="allowed"}`, 1)
	assertValue(t, sink, `ledger_entitlement_checks_total{cache="hit",result="allowed"}`, 1)
	assertValue(t, sink, `ledger_entitlement_checks_total{cache="miss",result="no_access"}`, 2)
	sink.AssertOneLabelSet(t)
}

func TestGenerateInvoiceIsTracedAndTimed(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	s := memory.New()
	l := ledger.New(s, ledger.WithMetrics(sink))
	p := activePlan(t, l, "pro", "app_1", 0)
	ctx := context.Background()
	sub := &subscription.Subscription{TenantID: leak, PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	// A subscription with no tenant is refused before anything is billed.
	orphan := &subscription.Subscription{
		ID: id.NewSubscriptionID(), PlanID: p.ID, AppID: "app_1", Status: subscription.StatusActive,
		CurrentPeriodStart: time.Now().UTC().Add(-time.Hour), CurrentPeriodEnd: time.Now().UTC().Add(time.Hour),
	}
	if err := s.CreateSubscription(ctx, orphan); err != nil {
		t.Fatalf("seed: %v", err)
	}

	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	if _, err := l.GenerateInvoice(ctx, sub.ID); !errors.Is(err, ledger.ErrAlreadyExists) {
		t.Fatalf("second GenerateInvoice: %v, want ErrAlreadyExists", err)
	}
	if _, err := l.GenerateInvoice(ctx, id.NewSubscriptionID()); !ledger.IsNotFound(err) {
		t.Fatalf("GenerateInvoice for no subscription: %v, want not found", err)
	}
	if _, err := l.GenerateInvoice(ctx, orphan.ID); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Fatalf("GenerateInvoice without a tenant: %v, want ErrInvalidInput", err)
	}

	spans := telemetrytest.Named(rec, "generate invoice")
	if len(spans) != 4 {
		t.Fatalf("want four generate spans, got %v", telemetrytest.Names(rec))
	}
	ok, exists, missing, refused := spans[0], spans[1], spans[2], spans[3]
	assertOK(t, ok)
	assertString(t, ok, "ledger.subscription.id", sub.ID.String())
	assertString(t, ok, "ledger.invoice.id", inv.ID.String())
	assertInt(t, ok, "ledger.invoice.line_items", int64(len(inv.LineItems)))
	assertString(t, ok, "ledger.invoice.period", "current")
	assertString(t, ok, "ledger.invoice.result", "ok")
	// An invoice that already exists, or a subscription that doesn't, is an
	// answer, not a failure.
	assertOK(t, exists)
	assertString(t, exists, "ledger.invoice.result", "exists")
	assertOK(t, missing)
	assertString(t, missing, "ledger.invoice.result", "not_found")
	assertFailed(t, refused, "ledger.ErrInvalidInput")
	assertString(t, refused, "ledger.invoice.result", "error")
	for _, sp := range spans {
		if _, set := telemetrytest.Attr(sp, "ledger.invoice.total"); set {
			t.Error("an amount reached a span")
		}
	}
	telemetrytest.AssertNoValue(t, rec, leak)

	for _, result := range []string{"ok", "exists", "not_found", "error"} {
		assertObservations(t, sink, `ledger_invoice_generate_duration_seconds{result="`+result+`"}`, 1)
	}
}

// tracedProvider is a payment provider that declines in words quoting the
// customer, and answers webhooks and payment-method lists.
type tracedProvider struct{ providerAPI }

func (p *tracedProvider) Name() string                { return "fake" }
func (p *tracedProvider) Provider() provider.Provider { return p }

func (p *tracedProvider) SyncPlan(context.Context, *plan.Plan) (string, error) {
	return "", fmt.Errorf("card of %s declined", leak)
}

func (p *tracedProvider) ImportPlan(_ context.Context, pid string) (*plan.Plan, error) {
	return nil, fmt.Errorf("no plan %s for %s", pid, leak)
}

func (p *tracedProvider) HandleWebhook(context.Context, []byte) (*provider.WebhookResult, error) {
	return &provider.WebhookResult{EventType: "invoice.paid", Handled: true}, nil
}

func (p *tracedProvider) ListPaymentMethods(context.Context, string) ([]provider.PaymentMethod, error) {
	return []provider.PaymentMethod{{Type: "card", Last4: "4242"}}, nil
}

func TestProviderCallsAreTracedAndTimed(t *testing.T) {
	rec := telemetrytest.RecordSpans(t)
	sink := telemetrytest.NewSink()
	l := ledger.New(memory.New(), ledger.WithPlugin(&tracedProvider{}), ledger.WithMetrics(sink))
	p := activePlan(t, l, "pro", "app_1", 0)
	ctx, parent := testTracer().Start(context.Background(), "POST /billing")

	if _, err := l.SyncPlanToProvider(ctx, p.ID); err == nil {
		t.Fatal("SyncPlanToProvider succeeded over a declining provider")
	}
	if _, err := l.ImportPlanFromProvider(ctx, "fake", "prod_1"); !errors.Is(err, ledger.ErrProviderSync) {
		t.Fatalf("ImportPlanFromProvider: %v, want ErrProviderSync", err)
	}
	if res, err := l.HandleWebhook(ctx, "fake", []byte(`{"customer":"`+leak+`"}`)); err != nil || !res.Handled {
		t.Fatalf("HandleWebhook: %+v, %v", res, err)
	}
	if _, err := l.ListPaymentMethods(ctx, leak); err != nil {
		t.Fatalf("ListPaymentMethods: %v", err)
	}
	parent.End()

	sync := telemetrytest.Only(t, rec, "sync plan")
	assertChildOf(t, sync, parent.SpanContext())
	assertString(t, sync, "ledger.plan.id", p.ID.String())
	assertString(t, sync, "ledger.provider.name", "fake")
	assertFailed(t, sync, "*errors.errorString")

	imported := telemetrytest.Only(t, rec, "import plan")
	assertString(t, imported, "ledger.provider.name", "fake")
	assertFailed(t, imported, "ledger.ErrProviderSync")

	hook := telemetrytest.Only(t, rec, "handle webhook")
	assertOK(t, hook)
	assertString(t, hook, "ledger.provider.name", "fake")
	assertString(t, hook, "ledger.webhook.event_type", "invoice.paid")
	assertBool(t, hook, "ledger.webhook.handled", true)

	assertOK(t, telemetrytest.Only(t, rec, "list payment methods"))
	telemetrytest.AssertNoValue(t, rec, leak)

	for series, want := range map[string]uint64{
		`ledger_provider_call_duration_seconds{operation="sync_plan",provider="fake",result="error"}`:         1,
		`ledger_provider_call_duration_seconds{operation="import_plan",provider="fake",result="error"}`:       1,
		`ledger_provider_call_duration_seconds{operation="handle_webhook",provider="fake",result="ok"}`:       1,
		`ledger_provider_call_duration_seconds{operation="list_payment_methods",provider="fake",result="ok"}`: 1,
	} {
		assertObservations(t, sink, series, want)
	}
	sink.AssertOneLabelSet(t)
}

func TestNoMetricsWithoutASink(t *testing.T) {
	// Every path that records must be safe with no sink: the default.
	l := ledger.New(memory.New(), ledger.WithPlugin(&tracedProvider{}))
	p := activePlan(t, l, "pro", "app_1", 0)
	if _, err := l.SyncPlanToProvider(context.Background(), p.ID); err == nil {
		t.Fatal("SyncPlanToProvider succeeded over a declining provider")
	}
	if _, err := l.Entitled(meterContext(), "api_calls"); err != nil {
		t.Fatalf("Entitled: %v", err)
	}
	if _, err := l.Advance(context.Background(), at(2026, 3, 1)); err != nil {
		t.Fatalf("Advance: %v", err)
	}
}
