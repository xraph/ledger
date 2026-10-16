package extension

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xraph/forge"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xraph/ledger/store/memory"
)

func scrape(t *testing.T, app forge.App) string {
	t.Helper()
	p, ok := app.Metrics().(interface{ PrometheusHandler() http.Handler })
	if !ok {
		t.Fatalf("app metrics %T have no Prometheus handler", app.Metrics())
	}
	rr := httptest.NewRecorder()
	p.PrometheusHandler().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/_/metrics", nil))
	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	return string(body)
}

func TestTheEngineRecordsOnTheAppsMetricsAndTracer(t *testing.T) {
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	app := forge.New(forge.WithAppName("ledger-telemetry-test"), forge.WithAppLogger(forge.NewNoopLogger()))
	rec := tracetest.NewSpanRecorder()
	if err := forge.RegisterSpanProcessor(app, rec); err != nil {
		t.Fatalf("RegisterSpanProcessor: %v", err)
	}
	e := New(WithStore(memory.New()), WithDisableLifecycle())
	if err := e.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, span := forge.StartSpan(context.Background(), "GET /reports")
	// No tenant in the context: no access, answered without the cache.
	if got, err := e.Engine().Entitled(ctx, "api_calls"); err != nil || got.Allowed {
		t.Fatalf("Entitled: %+v, %v", got, err)
	}
	span.End()

	var check bool
	for _, s := range rec.Ended() {
		if s.Name() == "check entitlement" && s.InstrumentationScope().Name == "github.com/xraph/ledger" &&
			s.Parent().SpanID() == span.SpanContext().SpanID() {
			check = true
		}
	}
	if !check {
		t.Error("the engine's span did not reach forge's tracer provider under the request span")
	}

	// A ledger metric is not semconv, so the scrape carries forge's prefix.
	body := scrape(t, app)
	want := `forge_ledger_entitlement_checks_total{cache="miss",result="no_access"} 1`
	if !strings.Contains(body, want) {
		var got []string
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "ledger_") {
				got = append(got, line)
			}
		}
		t.Fatalf("scrape lacks %s; ledger lines:\n%s", want, strings.Join(got, "\n"))
	}
}
