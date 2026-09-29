package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/meter"
	"github.com/xraph/ledger/plugin"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
)

type quotaSpy struct{ exceeded int }

func (q *quotaSpy) Name() string { return "quota-spy" }
func (q *quotaSpy) OnQuotaExceeded(_ context.Context, _, _ string, _, _ int64) error {
	q.exceeded++
	return nil
}

var _ plugin.OnQuotaExceeded = (*quotaSpy)(nil)

func TestInspectEntitlementHasNoSideEffects(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	spy := &quotaSpy{}
	l := ledger.New(s, ledger.WithPlugin(spy))
	p := activePlan(t, l, "ent", "app_1", 0)

	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if err := s.IngestBatch(ctx, []*meter.UsageEvent{{ID: id.NewUsageEventID(), TenantID: "t1", AppID: "app_1", FeatureKey: "api_calls", Quantity: 1500, Timestamp: time.Now().UTC()}}); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}

	got, err := l.InspectEntitlement(ctx, "t1", "app_1", "api_calls")
	if err != nil {
		t.Fatalf("InspectEntitlement: %v", err)
	}
	if got.Allowed || got.Used != 1500 || got.Limit != 1000 || got.Reason != "quota exceeded" {
		t.Errorf("got %+v, want not allowed, used 1500 of 1000, quota exceeded", got)
	}
	if spy.exceeded != 0 {
		t.Errorf("OnQuotaExceeded fired %d times; inspection must not fire plugin events", spy.exceeded)
	}
	if _, err := s.GetCached(ctx, "t1", "app_1", "api_calls"); err == nil {
		t.Error("inspection must not write the entitlement cache")
	}
}

func TestInspectEntitlementWithoutASubscription(t *testing.T) {
	l := ledger.New(memory.New())
	got, err := l.InspectEntitlement(context.Background(), "nobody", "app_1", "api_calls")
	if err != nil {
		t.Fatalf("InspectEntitlement: %v", err)
	}
	if got.Allowed || got.Reason != "no active subscription" {
		t.Errorf("got %+v", got)
	}
}

func TestInspectEntitlementRefusesAnEmptyTenant(t *testing.T) {
	l := ledger.New(memory.New())
	if _, err := l.InspectEntitlement(context.Background(), "", "app_1", "api_calls"); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("empty tenant: got %v, want ErrInvalidInput", err)
	}
}

type stubFormatter struct{ format string }

func (f *stubFormatter) Name() string   { return "stub-" + f.format }
func (f *stubFormatter) Format() string { return f.format }
func (f *stubFormatter) Render(_ context.Context, _ interface{}, w interface{}) error {
	out, ok := w.(io.Writer)
	if !ok {
		return errors.New("writer is not an io.Writer")
	}
	_, err := fmt.Fprintf(out, "invoice rendered as %s", f.format)
	return err
}

var _ plugin.InvoiceFormatter = (*stubFormatter)(nil)

func TestInvoiceFormatsAndExport(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(memory.New(), ledger.WithPlugin(&stubFormatter{format: "pdf"}), ledger.WithPlugin(&stubFormatter{format: "csv"}))
	if got := l.InvoiceFormats(); len(got) != 2 || got[0] != "csv" || got[1] != "pdf" {
		t.Errorf("formats = %v, want [csv pdf]", got)
	}

	p := activePlan(t, l, "exp", "app_1", 0)
	sub := &subscription.Subscription{TenantID: "t1", PlanID: p.ID, AppID: "app_1"}
	if err := l.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	inv, err := l.GenerateInvoice(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	body, err := l.ExportInvoice(ctx, inv.ID, "pdf")
	if err != nil || string(body) != "invoice rendered as pdf" {
		t.Errorf("export pdf: %q, %v", body, err)
	}
	if _, err := l.ExportInvoice(ctx, inv.ID, "docx"); !errors.Is(err, ledger.ErrInvalidInput) {
		t.Errorf("unknown format: got %v, want ErrInvalidInput", err)
	}
}

func TestNoFormatsOrProvidersIsEmptyNotNil(t *testing.T) {
	l := ledger.New(memory.New())
	if got := l.InvoiceFormats(); got == nil || len(got) != 0 {
		t.Errorf("formats = %#v, want an empty non-nil slice", got)
	}
	if got := l.ProviderNames(); got == nil || len(got) != 0 {
		t.Errorf("providers = %#v, want an empty non-nil slice", got)
	}
}
