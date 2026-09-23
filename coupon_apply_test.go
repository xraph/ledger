package ledger_test

import (
	"context"
	"errors"
	"sync"
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
		{
			// The label lies: this coupon says "usd" but the Money it
			// actually discounts by is EUR. Money.Subtract panics on the
			// Money's Currency field, not the label, so the check must
			// look at c.Amount.Currency, not c.Currency.
			name: "amount coupon labelled usd holds euros",
			mutate: func(c *coupon.Coupon) {
				c.Type = coupon.CouponTypeAmount
				c.Amount = types.EUR(500)
				// c.Currency is left at "usd" from baseCoupon: the
				// mismatched label.
			},
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponInvalid,
		},
		{
			name: "amount coupon has no currency anywhere",
			mutate: func(c *coupon.Coupon) {
				c.Type = coupon.CouponTypeAmount
				c.Currency = ""
				c.Amount = types.Money{Amount: 500}
			},
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponInvalid,
		},
		{
			name:    "coupon belongs to a different app",
			mutate:  func(c *coupon.Coupon) { c.AppID = "app_2" },
			code:    "LAUNCH10",
			wantErr: ledger.ErrCouponNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l, s, subID := fixture(t)

			c := baseCoupon()
			tt.mutate(c)
			mustCreateCoupon(t, s, c)

			// Capture before the call: on the memory store, GetCoupon and
			// GetCouponByID return the same pointer this test created, so
			// comparing stored.TimesRedeemed against c.TimesRedeemed after
			// the call would always compare a value against itself.
			want := c.TimesRedeemed

			_, err := l.ApplyCoupon(ctx, subID, tt.code)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}

			// A rejected coupon must leave no trace. The coupon was created
			// above regardless of which row this is (including "unknown
			// code" and "different app", where the applied code or app
			// doesn't match it), so the re-read must succeed.
			stored, getErr := s.GetCouponByID(ctx, c.ID)
			if getErr != nil {
				t.Fatalf("GetCouponByID: %v", getErr)
			}
			if stored.TimesRedeemed != want {
				t.Errorf("a rejected apply changed TimesRedeemed: got %d, want %d",
					stored.TimesRedeemed, want)
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

func TestApplyCouponCurrencyIsCaseInsensitive(t *testing.T) {
	t.Run("percentage coupon labelled uppercase applies", func(t *testing.T) {
		ctx := context.Background()
		l, s, subID := fixture(t)

		c := baseCoupon()
		c.Currency = "USD"
		mustCreateCoupon(t, s, c)

		if _, err := l.ApplyCoupon(ctx, subID, "LAUNCH10"); err != nil {
			t.Fatalf("ApplyCoupon: %v", err)
		}
	})

	t.Run("amount coupon labelled and priced uppercase applies", func(t *testing.T) {
		ctx := context.Background()
		l, s, subID := fixture(t)

		c := baseCoupon()
		c.Type = coupon.CouponTypeAmount
		c.Currency = "USD"
		c.Amount = types.Money{Amount: 500, Currency: "USD"}
		mustCreateCoupon(t, s, c)

		if _, err := l.ApplyCoupon(ctx, subID, "LAUNCH10"); err != nil {
			t.Fatalf("ApplyCoupon: %v", err)
		}
	})
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
	if err := s.CreateSubscription(ctx, sub2); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}

	if _, err := l.ApplyCoupon(ctx, sub2.ID, "LAUNCH10"); err != nil {
		t.Fatalf("ApplyCoupon with MaxRedemptions 0: %v", err)
	}

	stored, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if stored.TimesRedeemed != 10000 {
		t.Errorf("got TimesRedeemed %d, want 10000", stored.TimesRedeemed)
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
		if err := s.CreatePlan(ctx, p); err != nil {
			t.Fatalf("CreatePlan: %v", err)
		}
		sub := &subscription.Subscription{
			Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
			TenantID: "tenant_1", PlanID: p.ID,
			Status: subscription.StatusActive, AppID: "app_1",
		}
		if err := s.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}
		c := mustCreateCoupon(t, s, baseCoupon())

		_, err := l.ApplyCoupon(ctx, sub.ID, "LAUNCH10")
		if !errors.Is(err, v.err) {
			t.Fatalf("got error %v, want %v", err, v.err)
		}
		if !v.called {
			t.Error("the validator was never consulted")
		}

		stored, getErr := s.GetCouponByID(ctx, c.ID)
		if getErr != nil {
			t.Fatalf("GetCouponByID: %v", getErr)
		}
		if stored.TimesRedeemed != 0 {
			t.Errorf("a refused apply changed TimesRedeemed: got %d, want 0", stored.TimesRedeemed)
		}

		applied, err := l.ListAppliedCoupons(ctx, sub.ID)
		if err != nil {
			t.Fatalf("ListAppliedCoupons: %v", err)
		}
		if len(applied) != 0 {
			t.Errorf("a refused apply recorded %d applications, want 0", len(applied))
		}
	})

	t.Run("a built-in check refuses before the validator is consulted", func(t *testing.T) {
		v := &stubValidator{err: errors.New("should never be asked")}
		s := memory.New()
		l := ledger.New(s, ledger.WithPlugin(v))

		p := &plan.Plan{
			Entity: types.NewEntity(), ID: id.NewPlanID(), Currency: "usd",
			Status: plan.StatusActive, AppID: "app_1", Slug: "pro",
		}
		if err := s.CreatePlan(ctx, p); err != nil {
			t.Fatalf("CreatePlan: %v", err)
		}
		sub := &subscription.Subscription{
			Entity: types.NewEntity(), ID: id.NewSubscriptionID(),
			TenantID: "tenant_1", PlanID: p.ID,
			Status: subscription.StatusActive, AppID: "app_1",
		}
		if err := s.CreateSubscription(ctx, sub); err != nil {
			t.Fatalf("CreateSubscription: %v", err)
		}

		past := time.Now().UTC().Add(-48 * time.Hour)
		c := baseCoupon()
		c.ValidUntil = &past
		mustCreateCoupon(t, s, c)

		_, err := l.ApplyCoupon(ctx, sub.ID, "LAUNCH10")
		if !errors.Is(err, ledger.ErrCouponExpired) {
			t.Fatalf("got error %v, want ErrCouponExpired", err)
		}
		if v.called {
			t.Error("the validator was consulted despite an earlier, built-in refusal")
		}
	})
}

// TestApplyCouponCannotExceedTheCapConcurrently is the engine-level
// counterpart to the store-level RedeemCouponConcurrentCap conformance
// test: it drives the race through Ledger.ApplyCoupon itself, the path a
// real caller uses, rather than the store's RedeemCoupon directly. A
// coupon capped at 3 redemptions, applied to 10 different subscriptions on
// the same plan by 10 goroutines at once, must let exactly 3 through and
// refuse the rest with ErrCouponExhausted - the same guarantee the review
// found broken when ApplyCoupon read TimesRedeemed before writing it.
func TestApplyCouponCannotExceedTheCapConcurrently(t *testing.T) {
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

	c := baseCoupon()
	c.MaxRedemptions = 3
	mustCreateCoupon(t, s, c)

	const n = 10
	subIDs := make([]id.SubscriptionID, n)
	for i := range subIDs {
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
			t.Fatalf("CreateSubscription %d: %v", i, err)
		}
		subIDs[i] = sub.ID
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = l.ApplyCoupon(ctx, subIDs[i], "LAUNCH10")
		}(i)
	}
	wg.Wait()

	var wins, exhausted int
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ledger.ErrCouponExhausted):
			exhausted++
		default:
			t.Errorf("got an error from a concurrent ApplyCoupon that is neither nil nor ErrCouponExhausted: %v", err)
		}
	}
	if wins != 3 {
		t.Errorf("got %d winning ApplyCoupon calls out of %d, want exactly 3 (exhausted=%d)", wins, n, exhausted)
	}
	if exhausted != n-3 {
		t.Errorf("got %d exhausted ApplyCoupon calls out of %d, want exactly %d", exhausted, n, n-3)
	}

	stored, err := s.GetCouponByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if stored.TimesRedeemed != 3 {
		t.Errorf("got TimesRedeemed %d, want exactly 3", stored.TimesRedeemed)
	}
}
