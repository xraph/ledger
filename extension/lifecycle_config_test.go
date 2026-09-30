package extension

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/forge"
	log "github.com/xraph/go-utils/log"

	ledger "github.com/xraph/ledger"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

func TestLifecycleConfigReachesTheEngine(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{"default", nil, time.Minute},
		{"an interval", []Option{WithConfig(Config{LifecycleInterval: 90 * time.Second})}, 90 * time.Second},
		{"disabled", []Option{WithConfig(Config{LifecycleInterval: 90 * time.Second}), WithDisableLifecycle()}, 0},
		{"a negative interval is unset, not off", []Option{WithConfig(Config{LifecycleInterval: -time.Second})}, time.Minute},
		{"a pass-through engine option wins", []Option{WithLedgerOption(ledger.WithLifecycleInterval(0))}, 0},
	}
	for _, c := range cases {
		e := New(c.opts...)
		e.config = e.mergeWithDefaults(e.config)
		eng := ledger.New(memory.New(), e.buildLedgerOpts()...)
		if got := eng.LifecycleInterval(); got != c.want {
			t.Errorf("%s: interval %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMergeKeepsAProgrammaticDisableAndFillsTheInterval(t *testing.T) {
	e := New()
	if got := e.mergeConfigurations(Config{LifecycleInterval: time.Minute}, Config{DisableLifecycle: true}); !got.DisableLifecycle {
		t.Error("a YAML file must not turn back on a clock the code turned off")
	}
	if got := e.mergeConfigurations(Config{}, Config{LifecycleInterval: 2 * time.Minute}); got.LifecycleInterval != 2*time.Minute {
		t.Errorf("interval %v, want the programmatic 2m", got.LifecycleInterval)
	}
	if got := e.mergeConfigurations(Config{}, Config{}); got.LifecycleInterval != time.Minute || got.DisableLifecycle {
		t.Errorf("got %v disabled %v, want the 1m default and on", got.LifecycleInterval, got.DisableLifecycle)
	}
}

func TestSettingsReportTheLifecycleClock(t *testing.T) {
	if got := lifecycleSetting(nil); got != "off" {
		t.Errorf("no engine: %q, want off", got)
	}
	if got := lifecycleSetting(ledger.New(memory.New())); got != "1m0s" {
		t.Errorf("default engine: %q, want 1m0s", got)
	}
	if got := lifecycleSetting(ledger.New(memory.New(), ledger.WithLifecycleInterval(0))); got != "off" {
		t.Errorf("clock off: %q, want off", got)
	}
}

// The extension starts the clock with the engine, with DisableMigrate set or
// not, and leaves it off when disable_lifecycle says so.
func TestExtensionStartsTheLifecycleWorker(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		opts        []Option
		wantCancels bool
	}{
		{"migrating", nil, true},
		{"with DisableMigrate", []Option{WithDisableMigrate()}, true},
		{"with disable_lifecycle", []Option{WithDisableLifecycle()}, false},
		{"with DisableMigrate and disable_lifecycle", []Option{WithDisableMigrate(), WithDisableLifecycle()}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s := memory.New()
			opts := append([]Option{
				WithStore(s),
				WithConfig(Config{LifecycleInterval: 5 * time.Millisecond}),
				WithLedgerOption(ledger.WithClock(func() time.Time { return now })),
			}, c.opts...)
			e := New(opts...)
			e.config = e.mergeWithDefaults(e.config)
			e.engine = ledger.New(s, e.buildLedgerOpts()...)

			cancelAt := now.AddDate(0, -1, 0)
			sub := &subscription.Subscription{
				Entity: types.Entity{CreatedAt: cancelAt, UpdatedAt: cancelAt}, ID: id.NewSubscriptionID(),
				TenantID: "t1", AppID: "app_1", Status: subscription.StatusActive,
				CurrentPeriodStart: cancelAt, CurrentPeriodEnd: now.AddDate(0, 0, 10),
				CancelAt: &cancelAt,
			}
			if err := s.CreateSubscription(ctx, sub); err != nil {
				t.Fatalf("CreateSubscription: %v", err)
			}
			if err := e.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}

			// A clock that runs is found within the two seconds; one that is
			// off is given a short window to prove it stays quiet.
			window := 300 * time.Millisecond
			if c.wantCancels {
				window = 2 * time.Second
			}
			deadline := time.Now().Add(window)
			canceled := false
			for time.Now().Before(deadline) {
				if got, err := s.GetSubscription(ctx, sub.ID); err == nil && got.Status == subscription.StatusCanceled {
					canceled = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if canceled != c.wantCancels {
				t.Errorf("the clock enacted the cancel: %v, want %v", canceled, c.wantCancels)
			}
			if err := e.Stop(ctx); err != nil {
				t.Fatalf("Stop: %v", err)
			}
		})
	}
}

// Register reads the interval and the switch from the extension's real config
// path (the app's ConfigManager, bound under extensions.ledger), so the YAML
// spelling is what this decodes, duration string and all.
func TestLifecycleConfigDecodesThroughRegister(t *testing.T) {
	cases := []struct {
		name string
		keys map[string]any
		opts []Option
		want time.Duration
	}{
		{"absent keys take the default", map[string]any{}, nil, time.Minute},
		{"an interval", map[string]any{"lifecycle_interval": "90s"}, nil, 90 * time.Second},
		{"disable_lifecycle", map[string]any{"disable_lifecycle": true}, nil, 0},
		{"disable_lifecycle beats an interval", map[string]any{"lifecycle_interval": "90s", "disable_lifecycle": true}, nil, 0},
		{"a negative interval is unset", map[string]any{"lifecycle_interval": "-5s"}, nil, time.Minute},
		{"the code's switch survives a YAML file", map[string]any{"lifecycle_interval": "90s"}, []Option{WithDisableLifecycle()}, 0},
		{"the code's interval fills a YAML file without one", map[string]any{"meter_batch_size": 50}, []Option{WithConfig(Config{LifecycleInterval: 2 * time.Minute})}, 2 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cm := forge.NewManager()
			cm.Set("extensions.ledger", c.keys)
			app := forge.New(forge.WithAppName("t"), forge.WithAppConfigManager(cm))
			e := New(append([]Option{WithStore(memory.New())}, c.opts...)...)
			if err := e.Register(app); err != nil {
				t.Fatalf("Register: %v", err)
			}
			if got := e.Engine().LifecycleInterval(); got != c.want {
				t.Errorf("engine interval %v, want %v (config %+v)", got, c.want, e.config)
			}
		})
	}
}

// failingDueStore fails the clock's first query, so every lifecycle run ends
// in an error Advance reports.
type failingDueStore struct{ *memory.Store }

func (failingDueStore) ListDueSubscriptions(context.Context, subscription.DueOpts) ([]*subscription.Subscription, error) {
	return nil, errors.New("due query failed")
}

// ledger.New defaults to a no-op logger, so without the extension handing the
// engine its own logger a failed run would leave no trace at all.
func TestLifecycleErrorsReachTheExtensionLogger(t *testing.T) {
	ctx := context.Background()
	logger := log.NewTestLogger().(*log.TestLogger)
	app := forge.New(forge.WithAppName("t"), forge.WithAppLogger(logger))
	e := New(WithStore(failingDueStore{memory.New()}), WithConfig(Config{LifecycleInterval: 5 * time.Millisecond}))
	if err := e.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for !logger.AssertHasLog("WARN", "ledger: lifecycle run left work undone") {
		if time.Now().After(deadline) {
			t.Fatalf("no lifecycle warning reached the logger in 2s; it logged %d warnings", logger.CountLogs("WARN"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
