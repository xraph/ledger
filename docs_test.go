package ledger_test

import (
	"context"
	"log"
	"log/slog"
	"testing"
	"time"

	"github.com/xraph/ledger"
	"github.com/xraph/ledger/plan"
	"github.com/xraph/ledger/store/memory"
	"github.com/xraph/ledger/subscription"
	"github.com/xraph/ledger/types"
)

// TestDocumentationExamples verifies that all examples in the documentation compile
func TestDocumentationExamples(t *testing.T) {
	// Test Quick Start example from README
	t.Run("QuickStartExample", func(t *testing.T) {
		// Create store (memory for demo, use PostgreSQL in production)
		store := memory.New()

		// Initialize Ledger with plugins
		l := ledger.New(store,
			ledger.WithLogger(slog.Default()),
			ledger.WithMeterConfig(100, 5*time.Second),
			ledger.WithEntitlementCacheTTL(30*time.Second),
		)

		// Start the engine
		ctx := context.Background()
		if err := l.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer l.Stop()

		// Create a plan
		p := &plan.Plan{
			Name:     "Pro Plan",
			Slug:     "pro",
			Currency: "usd",
			Status:   plan.StatusActive,
			Features: []plan.Feature{
				{
					Key:       "api_calls",
					Name:      "API Calls",
					Type:      plan.FeatureMetered,
					Limit:     10000,
					Period:    plan.PeriodMonthly,
					SoftLimit: true, // Allow overage
				},
				{
					Key:   "seats",
					Name:  "Team Seats",
					Type:  plan.FeatureSeat,
					Limit: 5,
				},
			},
			Pricing: &plan.Pricing{
				BaseAmount:    types.USD(4900), // $49.00
				BillingPeriod: plan.PeriodMonthly,
				Tiers: []plan.PriceTier{
					{
						FeatureKey: "api_calls",
						Type:       plan.TierGraduated,
						UpTo:       10000,
						UnitAmount: types.Zero("usd"), // Included
					},
					{
						FeatureKey: "api_calls",
						Type:       plan.TierGraduated,
						UpTo:       -1,           // Unlimited
						UnitAmount: types.USD(1), // $0.01 per call
					},
				},
			},
		}

		if err := l.CreatePlan(ctx, p); err != nil {
			t.Fatal(err)
		}

		// Create a subscription
		sub := &subscription.Subscription{
			TenantID: "tenant_123",
			PlanID:   p.ID,
			Status:   subscription.StatusActive,
			AppID:    "app_456",
		}

		if err := l.CreateSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}

		// Set context for tenant/app isolation
		ctx = context.WithValue(ctx, "tenant_id", "tenant_123")
		ctx = context.WithValue(ctx, "app_id", "app_456")

		// Check entitlement (< 1ms with cache)
		result, err := l.Entitled(ctx, "api_calls")
		if err != nil {
			t.Fatal(err)
		}

		if result.Allowed {
			log.Printf("API calls allowed. Remaining: %d\n", result.Remaining)

			// Meter usage (non-blocking, batched)
			l.Meter(ctx, "api_calls", 100)
		} else {
			log.Printf("API calls denied: %s\n", result.Reason)
		}

		// Generate invoice
		invoice, err := l.GenerateInvoice(ctx, sub.ID)
		if err != nil {
			t.Fatal(err)
		}

		log.Printf("Invoice generated: %s\n", invoice.Total.String())
	})

	// Test Money type examples
	t.Run("MoneyExamples", func(t *testing.T) {
		// Constructors
		_ = types.USD(4900)   // $49.00
		_ = types.EUR(9900)   // €99.00
		_ = types.Zero("usd") // $0.00

		// Arithmetic
		m1 := types.USD(100)
		m2 := types.USD(200)
		_ = m1.Add(m2)     // $3.00
		_ = m1.Multiply(3) // $3.00
		_ = m1.Divide(2)   // $0.50

		// Comparison
		if m1.LessThan(m2) {
			// m1 is less than m2
		}

		// Formatting
		_ = m1.String()      // "$1.00"
		_ = m1.FormatMajor() // "1.00"
	})
}
