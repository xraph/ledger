package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/coupon"
	"github.com/xraph/ledger/id"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// fixture builds a ledger over a memory store holding one active plan and
// one active subscription, and returns both plus the subscription's ID.
func fixture(t *testing.T) (*ledger.Ledger, *memory.Store, id.SubscriptionID) {
	t.Helper()
	ctx := context.Background()

	s := memory.New()
	l := ledger.New(s)

	p := &plan.Plan{
		Entity:   types.NewEntity(),
		ID:       id.NewPlanID(),
		Name:     "Pro",
		Slug:     "pro",
		Currency: "usd",
		Status:   plan.StatusActive,
		AppID:    "app_1",
		Pricing: &plan.Pricing{
			ID:            id.NewPriceID(),
			BaseAmount:    types.USD(4900),
			BillingPeriod: plan.PeriodMonthly,
		},
	}
	if err := s.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	sub := &subscription.Subscription{
		Entity:             types.NewEntity(),
		ID:                 id.NewSubscriptionID(),
		TenantID:           "tenant_1",
		PlanID:             p.ID,
		Status:             subscription.StatusActive,
		CurrentPeriodStart: time.Now().UTC().Add(-24 * time.Hour),
		CurrentPeriodEnd:   time.Now().UTC().Add(24 * time.Hour),
		AppID:              "app_1",
	}
	if err := s.CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	return l, s, sub.ID
}

func mustCreateCoupon(t *testing.T, s *memory.Store, c *coupon.Coupon) *coupon.Coupon {
	t.Helper()
	if err := s.CreateCoupon(context.Background(), c); err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	return c
}

func baseCoupon() *coupon.Coupon {
	return &coupon.Coupon{
		Entity:     types.NewEntity(),
		ID:         id.NewCouponID(),
		Code:       "LAUNCH10",
		Name:       "Launch discount",
		Type:       coupon.CouponTypePercentage,
		Percentage: 10,
		Currency:   "usd",
		AppID:      "app_1",
	}
}

func TestApplyCouponHappyPath(t *testing.T) {
	ctx := context.Background()
	l, s, subID := fixture(t)
	c := mustCreateCoupon(t, s, baseCoupon())

	got, err := l.ApplyCoupon(ctx, subID, "LAUNCH10")
	if err != nil {
		t.Fatalf("ApplyCoupon: %v", err)
	}
	if got.ID.String() != c.ID.String() {
		t.Errorf("got coupon %s, want %s", got.ID, c.ID)
	}

	stored, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if stored.TimesRedeemed != 1 {
		t.Errorf("got TimesRedeemed %d, want 1", stored.TimesRedeemed)
	}

	applied, err := l.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons, want 1", len(applied))
	}
}

func TestApplyCouponRejections(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	past := time.Now().UTC().Add(-48 * time.Hour)

	tests := []struct {
		name    string
		mutate  func(*coupon.Coupon)
		code    string
		wantErr error
	}{
		{
			name:    "unknown code",
			mutate:  func(*coupon.Coupon) {},
			code:    "NOPE",
			wantErr: ledger.ErrCouponNotFound,
		},
		{
			name:    "not yet valid",
			mutate:  func(c *coupon.Coupon) { c.ValidFrom = &future },
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponNotStarted,
		},
		{
			name:    "expired",
			mutate:  func(c *coupon.Coupon) { c.ValidUntil = &past },
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponExpired,
		},
		{
			name: "redemptions exhausted",
			mutate: func(c *coupon.Coupon) {
				c.MaxRedemptions = 5
				c.TimesRedeemed = 5
			},
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponExhausted,
		},
		{
			// Review Focus 1. Money.Subtract panics on a currency
			// mismatch, so this must be refused here and never reach
			// invoice generation.
			name:    "currency does not match the plan",
			mutate:  func(c *coupon.Coupon) { c.Currency = "eur" },
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l, s, subID := fixture(t)

			c := baseCoupon()
			tt.mutate(c)
			mustCreateCoupon(t, s, c)

			_, err := l.ApplyCoupon(ctx, subID, tt.code)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}

			// A rejected coupon must leave no trace.
			stored, getErr := s.GetCouponByID(ctx, c.ID)
			if getErr == nil && stored.TimesRedeemed != c.TimesRedeemed {
				t.Errorf("a rejected apply changed TimesRedeemed: got %d, want %d",
					stored.TimesRedeemed, c.TimesRedeemed)
			}

			applied, listErr := l.ListAppliedCoupons(ctx, subID)
			if listErr != nil {
				t.Fatalf("ListAppliedCoupons: %v", listErr)
			}
			if len(applied) != 0 {
				t.Errorf("a rejected apply recorded %d applications, want 0", len(applied))
			}
		})
	}
}

func TestApplyCouponTwiceIsRejected(t *testing.T) {
	ctx := context.Background()
	l, s, subID := fixture(t)
	mustCreateCoupon(t, s, baseCoupon())

	if _, err := l.ApplyCoupon(ctx, subID, "LAUNCH10"); err != nil {
		t.Fatalf("first ApplyCoupon: %v", err)
	}

	_, err := l.ApplyCoupon(ctx, subID, "LAUNCH10")
	if !errors.Is(err, ledger.ErrCouponAlreadyApplied) {
		t.Fatalf("second ApplyCoupon: got %v, want ErrCouponAlreadyApplied", err)
	}

	applied, err := l.ListAppliedCoupons(ctx, subID)
	if err != nil {
		t.Fatalf("ListAppliedCoupons: %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("got %d applied coupons, want 1", len(applied))
	}
}

func TestApplyCouponUnlimitedRedemptions(t *testing.T) {
	ctx := context.Background()
	l, s, _ := fixture(t)

	c := baseCoupon()
	c.MaxRedemptions = 0 // unlimited
	c.TimesRedeemed = 9999
	mustCreateCoupon(t, s, c)

	// A second subscription, because the same one cannot take it twice.
	sub2 := &subscription.Subscription{
		Entity:   types.NewEntity(),
		ID:       id.NewSubscriptionID(),
		TenantID: "tenant_2",
		PlanID:   id.Nil,
		Status:   subscription.StatusActive,
		AppID:    "app_1",
	}
	_ = s.CreateSubscription(ctx, sub2)

	if _, err := l.ApplyCoupon(ctx, sub2.ID, "LAUNCH10"); err != nil {
		t.Fatalf("ApplyCoupon with MaxRedemptions 0: %v", err)
	}
}

// stubValidator refuses everything, and records that it was asked.
type stubValidator struct {
	called bool
	err    error
}

func (s *stubValidator) Name() string { return "stub-validator" }
func (s *stubValidator) ValidateCoupon(_ context.Context, _ interface{}, _ interface{}) error {
	s.called = true
	return s.err
}

func TestApplyCouponConsultsPluginValidators(t *testing.T) {
	ctx := context.Background()

	t.Run("a refusing validator blocks the apply", func(t *testing.T) {
		v := &stubValidator{err: errors.New("tenant is on the deny list")}
		s := memory.New()
		l := ledger.New(s, ledger.WithPlugin(v))

		p := &plan.Plan{
			Entity: types.NewEntity(), ID: id.NewPlanID(), Currency: "usd",
			Status: plan.StatusActive, AppID: "app_1", Slug: "pro",
		}
		_ = s.CreatePlan(ctx, p)
		sub := &subscription.Subscription{
			Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
			TenantID: "tenant_1", PlanID: p.ID,
			Status: subscription.StatusActive, AppID: "app_1",
		}
		_ = s.CreateSubscription(ctx, sub)
		mustCreateCoupon(t, s, baseCoupon())

		_, err := l.ApplyCoupon(ctx, sub.ID, "LAUNCH10")
		if err == nil {
			t.Fatal("got nil error, want the validator's refusal")
		}
		if !v.called {
			t.Error("the validator was never consulted")
		}

		applied, _ := l.ListAppliedCoupons(ctx, sub.ID)
		if len(applied) != 0 {
			t.Errorf("a refused apply recorded %d applications, want 0", len(applied))
		}
	})
}
