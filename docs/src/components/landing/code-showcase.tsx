"use client";

import { motion } from "framer-motion";
import { CodeBlock } from "./code-block";
import { SectionHeader } from "./section-header";

const setupBillingCode = `package main

import (
  "context"
  "log/slog"
  "time"

  "github.com/xraph/ledger"
  "github.com/xraph/ledger/plan"
  "github.com/xraph/ledger/store/postgres"
  "github.com/xraph/ledger/types"
)

func main() {
  ctx := context.Background()

  // Initialize billing engine
  engine := ledger.New(
    postgres.New(pool),
    ledger.WithMeterConfig(100, 5*time.Second),
    ledger.WithEntitlementCacheTTL(30*time.Second),
  )

  // Create a usage-based plan
  p := &plan.Plan{
    Name: "Pro Plan",
    Slug: "pro",
    Features: []plan.Feature{
      {
        Key:   "api_calls",
        Type:  plan.FeatureMetered,
        Limit: 10000,
      },
    },
    Pricing: &plan.Pricing{
      BaseAmount: types.USD(4900), // $49.00
    },
  }

  engine.CreatePlan(ctx, p)
}`;

const usageTrackingCode = `package main

import (
  "context"
  "fmt"

  "github.com/xraph/ledger"
)

func trackUsage(
  engine *ledger.Ledger,
  ctx context.Context,
) {
  // Set tenant context
  ctx = context.WithValue(ctx, "tenant_id", "tenant_123")
  ctx = context.WithValue(ctx, "app_id", "app_456")

  // Check entitlement (<1ms with cache)
  result, _ := engine.Entitled(ctx, "api_calls")

  if result.Allowed {
    fmt.Printf("Quota: %d/%d remaining\\n",
      result.Remaining, result.Limit)

    // Meter usage (non-blocking, batched)
    engine.Meter(ctx, "api_calls", 1)

    // Process API call...
  } else {
    fmt.Printf("Quota exceeded: %s\\n", result.Reason)
    // Return 429 Too Many Requests
  }
}`;

export function CodeShowcase() {
  return (
    <section className="relative w-full py-20 sm:py-28">
      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <SectionHeader
          badge="Developer Experience"
          title="Simple API. Powerful billing."
          description="Setup plans and track usage in under 20 lines. Ledger handles metering, entitlements, and invoicing."
        />

        <div className="mt-14 grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Setup side */}
          <motion.div
            initial={{ opacity: 0, x: -20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.1 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-emerald-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Setup Billing
              </span>
            </div>
            <CodeBlock code={setupBillingCode} filename="main.go" />
          </motion.div>

          {/* Usage side */}
          <motion.div
            initial={{ opacity: 0, x: 20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.2 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-blue-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Track Usage
              </span>
            </div>
            <CodeBlock code={usageTrackingCode} filename="usage.go" />
          </motion.div>
        </div>
      </div>
    </section>
  );
}
