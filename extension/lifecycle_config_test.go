package extension

import (
	"context"
	"testing"
	"time"

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

			deadline := time.Now().Add(300 * time.Millisecond)
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
