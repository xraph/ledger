"use client";

import { motion } from "framer-motion";
import { CodeBlock } from "./code-block";
import { SectionHeader } from "./section-header";

const createTransactionCode = `package main

import (
  "context"
  "log/slog"
  "time"

  "github.com/xraph/ledger"
  "github.com/xraph/ledger/store/postgres"
  "github.com/shopspring/decimal"
)

func main() {
  ctx := context.Background()

  engine, _ := ledger.NewEngine(
    ledger.WithStore(postgres.New(pool)),
    ledger.WithValidator(ledger.DoubleEntry),
    ledger.WithLogger(slog.Default()),
  )

  ctx = ledger.WithTenant(ctx, "tenant-1")
  ctx = ledger.WithApp(ctx, "accounting")

  // Create a double-entry transaction
  txn, _ := engine.CreateTransaction(ctx,
    ledger.Transaction{
      Date:        time.Now(),
      Description: "Customer payment",
      Entries: []ledger.Entry{
        {Account: "cash", Debit: decimal.NewFromFloat(100)},
        {Account: "revenue", Credit: decimal.NewFromFloat(100)},
      },
    })
  // txn.ID=txn_01j8x... Status=posted
}`;

const reportCode = `package main

import (
  "context"
  "fmt"

  "github.com/xraph/ledger"
)

func generateReport(
  engine *ledger.Engine,
  ctx context.Context,
) {
  ctx = ledger.WithTenant(ctx, "tenant-1")

  // Generate balance sheet report
  report, _ := engine.GenerateReport(ctx,
    &ledger.ReportInput{
      Type:     ledger.BalanceSheet,
      Date:     time.Now(),
      Accounts: []string{"assets", "liabilities", "equity"},
    })

  for _, line := range report.Lines {
    fmt.Printf("%s: %s\\n",
      line.Account, line.Balance.String())
  }
  // assets.cash: 10,500.00
  // liabilities.payable: 3,200.00
}`;

export function CodeShowcase() {
  return (
    <section className="relative w-full py-20 sm:py-28">
      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <SectionHeader
          badge="Developer Experience"
          title="Simple API. Powerful accounting."
          description="Create transactions and generate financial reports in under 20 lines. Ledger handles the rest."
        />

        <div className="mt-14 grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Transaction side */}
          <motion.div
            initial={{ opacity: 0, x: -20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.1 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-emerald-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Transactions
              </span>
            </div>
            <CodeBlock code={createTransactionCode} filename="main.go" />
          </motion.div>

          {/* Reporting side */}
          <motion.div
            initial={{ opacity: 0, x: 20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.2 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-blue-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Reporting
              </span>
            </div>
            <CodeBlock code={reportCode} filename="report.go" />
          </motion.div>
        </div>
      </div>
    </section>
  );
}
