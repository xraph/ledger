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
    title: "Document Ingestion Pipeline",
    description:
      "Load, chunk, embed, and store in one call. Weave handles the full lifecycle from raw content to searchable vectors.",
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
        <path d="M12 2v10M8 8l4 4 4-4" />
        <path d="M3 15v4a2 2 0 002 2h14a2 2 0 002-2v-4" />
      </svg>
    ),
    code: `doc, err := engine.Ingest(ctx, "col-123",
  weave.IngestInput{
    Title:   "Product FAQ",
    Content: "Our return policy...",
    Source:  "faq.md",
  })
// doc.State=ready chunks=12`,
    filename: "ingest.go",
  },
  {
    title: "Semantic Retrieval",
    description:
      "Cosine similarity, MMR, and hybrid search. Retrieve the most relevant chunks across a collection with configurable top-K and score thresholds.",
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
        <circle cx="11" cy="11" r="8" />
        <path d="M21 21l-4.35-4.35" />
        <path d="M11 8v6M8 11h6" />
      </svg>
    ),
    code: `results, err := engine.Retrieve(ctx, "col-123",
  &weave.RetrieveInput{
    Query:    "return policy",
    TopK:     5,
    MinScore: 0.75,
  })
// [0.94] Our return policy allows...`,
    filename: "retrieve.go",
  },
  {
    title: "Multi-Tenant Isolation",
    description:
      "Every collection, document, and chunk is scoped to a tenant via context. Cross-tenant queries are structurally impossible.",
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
        <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2" />
        <circle cx="9" cy="7" r="4" />
        <path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75" />
      </svg>
    ),
    code: `ctx = weave.WithTenant(ctx, "tenant-1")
ctx = weave.WithApp(ctx, "myapp")

// All ingestions and retrievals are
// automatically scoped to tenant-1`,
    filename: "scope.go",
  },
  {
    title: "Pluggable Backends",
    description:
      "Start with in-memory for development, swap to PostgreSQL + pgvector for production. Every subsystem is a Go interface.",
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
        <ellipse cx="12" cy="5" rx="9" ry="3" />
        <path d="M21 12c0 1.66-4.03 3-9 3s-9-1.34-9-3" />
        <path d="M3 5v14c0 1.66 4.03 3 9 3s9-1.34 9-3V5" />
      </svg>
    ),
    code: `engine, _ := weave.NewEngine(
  weave.WithStore(postgres.New(pool)),
  weave.WithVectorStore(pgvector.New(pool)),
  weave.WithEmbedder(myEmbedder),
  weave.WithLogger(slog.Default()),
)`,
    filename: "main.go",
  },
  {
    title: "Extension Hooks",
    description:
      "OnIngestCompleted, OnRetrievalStarted, and 12 other lifecycle events. Wire in metrics, audit trails, or custom logic.",
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
        <path d="M20.24 12.24a6 6 0 00-8.49-8.49L5 10.5V19h8.5z" />
        <line x1="16" y1="8" x2="2" y2="22" />
        <line x1="17.5" y1="15" x2="9" y2="15" />
      </svg>
    ),
    code: `func (e *MetricsExt) OnIngestCompleted(
  ctx context.Context,
  colID string,
  docs, chunks int,
  elapsed time.Duration,
) {
  metrics.Inc("weave.chunks.created", chunks)
}`,
    filename: "extension.go",
  },
  {
    title: "Collection Management",
    description:
      "Organize documents with per-collection embedding models, chunk strategies, and metadata. Reindex any collection at any time.",
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
        <path d="M3 6h18M3 12h18M3 18h18" />
        <rect x="2" y="3" width="20" height="18" rx="2" />
      </svg>
    ),
    code: `col, _ := engine.CreateCollection(ctx,
  weave.CreateCollectionInput{
    Name:            "product-docs",
    EmbeddingModel:  "text-embedding-3-small",
    ChunkStrategy:   "recursive",
    ChunkSize:       512,
    ChunkOverlap:    50,
  })
// Reindex: engine.Reindex(ctx, col.ID)`,
    filename: "collection.go",
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
          title="Everything you need for RAG pipelines"
          description="Weave handles the hard parts — ingestion, chunking, embedding, retrieval, and multi-tenancy — so you can focus on your application."
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
                "group relative rounded-xl border border-fd-border bg-fd-card/50 backdrop-blur-sm p-6 hover:border-violet-500/20 hover:bg-fd-card/80 transition-all duration-300",
                feature.colSpan === 2 && "md:col-span-2",
              )}
            >
              {/* Header */}
              <div className="flex items-start gap-3 mb-4">
                <div className="flex items-center justify-center size-9 rounded-lg bg-violet-500/10 text-violet-600 dark:text-violet-400 shrink-0">
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
