"use client";

import { motion } from "framer-motion";
import { CodeBlock } from "./code-block";
import { SectionHeader } from "./section-header";

const ingestCode = `package main

import (
  "context"
  "log/slog"

  "github.com/xraph/weave"
  "github.com/xraph/weave/store/postgres"
  "github.com/xraph/weave/vectorstore/pgvector"
)

func main() {
  ctx := context.Background()

  engine, _ := weave.NewEngine(
    weave.WithStore(postgres.New(pool)),
    weave.WithVectorStore(pgvector.New(pool)),
    weave.WithEmbedder(myEmbedder),
    weave.WithLogger(slog.Default()),
  )

  ctx = weave.WithTenant(ctx, "tenant-1")
  ctx = weave.WithApp(ctx, "myapp")

  // Ingest a document — chunk, embed, store
  doc, _ := engine.Ingest(ctx, "col-123",
    weave.IngestInput{
      Title:   "Product FAQ",
      Content: "Our return policy...",
      Source:  "faq.md",
    })
  // doc.State=ready chunks=12
}`;

const retrieveCode = `package main

import (
  "context"
  "fmt"

  "github.com/xraph/weave"
)

func queryContext(
  engine *weave.Engine,
  ctx context.Context,
) {
  ctx = weave.WithTenant(ctx, "tenant-1")

  // Semantic retrieval with score threshold
  results, _ := engine.Retrieve(ctx, "col-123",
    &weave.RetrieveInput{
      Query:    "What is the return policy?",
      TopK:     5,
      MinScore: 0.75,
    })

  for _, r := range results {
    fmt.Printf("[%.2f] %s\\n",
      r.Score, r.Content[:80])
  }
  // [0.94] Our return policy allows...
  // [0.87] Items must be returned...
}`;

export function CodeShowcase() {
  return (
    <section className="relative w-full py-20 sm:py-28">
      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <SectionHeader
          badge="Developer Experience"
          title="Simple API. Powerful retrieval."
          description="Ingest a document and retrieve semantically similar chunks in under 20 lines. Weave handles the rest."
        />

        <div className="mt-14 grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Ingestion side */}
          <motion.div
            initial={{ opacity: 0, x: -20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.1 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-violet-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Ingestion
              </span>
            </div>
            <CodeBlock code={ingestCode} filename="main.go" />
          </motion.div>

          {/* Retrieval side */}
          <motion.div
            initial={{ opacity: 0, x: 20 }}
            whileInView={{ opacity: 1, x: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: 0.2 }}
          >
            <div className="mb-3 flex items-center gap-2">
              <div className="size-2 rounded-full bg-green-500" />
              <span className="text-xs font-medium text-fd-muted-foreground uppercase tracking-wider">
                Retrieval
              </span>
            </div>
            <CodeBlock code={retrieveCode} filename="retrieve.go" />
          </motion.div>
        </div>
      </div>
    </section>
  );
}
