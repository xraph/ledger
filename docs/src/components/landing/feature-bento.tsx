"use client";

import { motion } from "framer-motion";
import { cn } from "@/lib/cn";
import { CodeBlock } from "./code-block";
import { SectionHeader } from "./section-header";

interface FeatureCard {
  title: string;
  description: string;
  icon: React.ReactNode;
  code: string;
  filename: string;
  colSpan?: number;
}

const features: FeatureCard[] = [
  {
    title: "Sub-Millisecond Entitlements",
    description:
      "Check feature access with <1ms latency using intelligent caching. Built for high-frequency permission checks.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M12 6v6l4 2" />
        <circle cx="12" cy="12" r="10" />
      </svg>
    ),
    code: `// Check entitlement (<1ms with cache)
result, _ := engine.Entitled(ctx, "api_calls")

if result.Allowed {
  fmt.Printf("Remaining: %d/%d\\n",
    result.Remaining, result.Limit)
  // Process request...
}`,
    filename: "entitlement.go",
  },
  {
    title: "High-Throughput Metering",
    description:
      "Ingest 10K+ usage events per second with batched processing. Non-blocking API calls with automatic flush.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M3 12h18M3 6h18M3 18h18" />
        <circle cx="9" cy="12" r="1" />
        <circle cx="15" cy="6" r="1" />
        <circle cx="12" cy="18" r="1" />
      </svg>
    ),
    code: `// Meter usage (non-blocking, batched)
engine.Meter(ctx, "api_calls", 100)
engine.Meter(ctx, "storage_gb", 5)
engine.Meter(ctx, "seats", 1)

// Auto-batches every 100 events or 5s
// 10K+ events/second throughput`,
    filename: "metering.go",
  },
  {
    title: "Flexible Pricing Models",
    description:
      "Graduated tiers, volume pricing, per-seat, flat-rate, and hybrid models. Define complex pricing with simple structs.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M12 2v20M17 5H9.5a3.5 3.5 0 000 7h5a3.5 3.5 0 010 7H6" />
      </svg>
    ),
    code: `Pricing: &plan.Pricing{
  BaseAmount: types.USD(4900), // $49
  Tiers: []plan.PriceTier{
    {UpTo: 1000, UnitAmount: types.Zero()},
    {UpTo: 5000, UnitAmount: types.USD(10)},
    {UpTo: -1, UnitAmount: types.USD(5)},
  },
}`,
    filename: "pricing.go",
  },
  {
    title: "Subscription Lifecycle",
    description:
      "Manage trials, upgrades, downgrades, and cancellations. Automatic proration and period management.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z" />
        <path d="M22 6l-10 7L2 6" />
      </svg>
    ),
    code: `// Create subscription with trial
sub := &subscription.Subscription{
  PlanID:     planID,
  Status:     subscription.StatusTrialing,
  TrialEnd:   time.Now().AddDate(0, 0, 14),
}

// Upgrade/downgrade
engine.ChangePlan(ctx, subID, newPlanID)`,
    filename: "subscription.go",
  },
  {
    title: "Automatic Invoicing",
    description:
      "Generate invoices with line items, taxes, and discounts. Integrates with payment providers for collection.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" />
        <path d="M14 2v6h6M16 13H8M16 17H8M10 9H8" />
      </svg>
    ),
    code: `// Generate invoice for period
invoice, _ := engine.GenerateInvoice(
  ctx, subscriptionID)

// Invoice includes:
// - Base subscription: $49.00
// - API overage (5k): $50.00
// - Discount (10%): -$9.90
// Total: $89.10`,
    filename: "invoice.go",
  },
  {
    title: "Type-Safe Money",
    description:
      "Integer-only currency handling with zero floating-point errors. Multi-currency support with proper decimal handling.",
    icon: (
      <svg
        className="size-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="10" />
        <path d="M16 8l-8 8M8 8l8 8" />
      </svg>
    ),
    code: `// All amounts are integer cents
amount := types.USD(4999)  // $49.99
tax := amount.Multiply(0.08) // 8% tax
total := amount.Add(tax)

// Multi-currency support
eur := types.EUR(3999)  // €39.99
gbp := types.GBP(2999)  // £29.99

// No floating-point errors ever`,
    filename: "money.go",
    colSpan: 2,
  },
];

const containerVariants = {
  hidden: {},
  visible: {
    transition: {
      staggerChildren: 0.08,
    },
  },
};

const itemVariants = {
  hidden: { opacity: 0, y: 20 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.5, ease: "easeOut" as const },
  },
};

export function FeatureBento() {
  return (
    <section className="relative w-full py-20 sm:py-28">
      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <SectionHeader
          badge="Features"
          title="Everything you need for SaaS billing"
          description="Ledger handles the hard parts — metering, entitlements, pricing, and invoicing — so you can focus on your product."
        />

        <motion.div
          variants={containerVariants}
          initial="hidden"
          whileInView="visible"
          viewport={{ once: true, margin: "-50px" }}
          className="mt-14 grid grid-cols-1 md:grid-cols-2 gap-4"
        >
          {features.map((feature) => (
            <motion.div
              key={feature.title}
              variants={itemVariants}
              className={cn(
                "group relative rounded-xl border border-fd-border bg-fd-card/50 backdrop-blur-sm p-6 hover:border-emerald-500/20 hover:bg-fd-card/80 transition-all duration-300",
                feature.colSpan === 2 && "md:col-span-2",
              )}
            >
              {/* Header */}
              <div className="flex items-start gap-3 mb-4">
                <div className="flex items-center justify-center size-9 rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 shrink-0">
                  {feature.icon}
                </div>
                <div>
                  <h3 className="text-sm font-semibold text-fd-foreground">
                    {feature.title}
                  </h3>
                  <p className="text-xs text-fd-muted-foreground mt-1 leading-relaxed">
                    {feature.description}
                  </p>
                </div>
              </div>

              {/* Code snippet */}
              <CodeBlock
                code={feature.code}
                filename={feature.filename}
                showLineNumbers={false}
                className="text-xs"
              />
            </motion.div>
          ))}
        </motion.div>
      </div>
    </section>
  );
}
