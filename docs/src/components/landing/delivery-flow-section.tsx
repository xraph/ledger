"use client";

import { AnimatePresence, motion } from "framer-motion";
import { useEffect, useState } from "react";
import { cn } from "@/lib/cn";
import { SectionHeader } from "./section-header";

// ─── Cycling Ingestion Action ─────────────────────────────────
const pipelineActions = ["doc.ingest", "query.retrieve", "col.reindex"];

function CyclingPipelineAction() {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    const interval = setInterval(() => {
      setIndex((prev) => (prev + 1) % pipelineActions.length);
    }, 3500);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="relative h-5 overflow-hidden">
      <AnimatePresence mode="wait">
        <motion.span
          key={pipelineActions[index]}
          initial={{ y: 12, opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          exit={{ y: -12, opacity: 0 }}
          transition={{ duration: 0.3 }}
          className="absolute inset-0 text-violet-500 dark:text-violet-400 font-mono text-xs font-medium"
        >
          {pipelineActions[index]}
        </motion.span>
      </AnimatePresence>
    </div>
  );
}

// ─── Pipeline Stage ──────────────────────────────────────────
interface StageProps {
  label: string;
  sublabel?: React.ReactNode;
  color: string;
  borderColor: string;
  bgColor: string;
  pulse?: boolean;
  delay: number;
}

function Stage({
  label,
  sublabel,
  color,
  borderColor,
  bgColor,
  pulse,
  delay,
}: StageProps) {
  return (
    <motion.div
      initial={{ opacity: 0, scale: 0.85 }}
      whileInView={{ opacity: 1, scale: 1 }}
      viewport={{ once: true }}
      transition={{ duration: 0.4, delay }}
      className={cn(
        "relative flex flex-col items-center gap-1 rounded-xl border px-4 py-3 min-w-[90px]",
        borderColor,
        bgColor,
      )}
    >
      {pulse && (
        <motion.div
          className={cn("absolute inset-0 rounded-xl border", borderColor)}
          animate={{ opacity: [0.4, 0], scale: [1, 1.12] }}
          transition={{ duration: 2, repeat: Infinity, ease: "easeOut" }}
        />
      )}
      <span className={cn("text-xs font-semibold font-mono", color)}>
        {label}
      </span>
      {sublabel && (
        <span className="text-[10px] text-fd-muted-foreground">{sublabel}</span>
      )}
    </motion.div>
  );
}

// ─── Animated Connection ─────────────────────────────────────
function Connection({
  color = "violet",
  delay = 0,
  horizontal = true,
}: {
  color?: "violet" | "purple" | "green" | "red";
  delay?: number;
  horizontal?: boolean;
}) {
  const colorMap = {
    violet: { line: "bg-violet-500/30", particle: "bg-violet-400" },
    purple: { line: "bg-purple-500/30", particle: "bg-purple-400" },
    green: { line: "bg-green-500/30", particle: "bg-green-400" },
    red: { line: "bg-red-500/30", particle: "bg-red-400" },
  };

  const c = colorMap[color];

  if (!horizontal) {
    return (
      <div className="relative flex flex-col items-center h-6 w-px">
        <div
          className={cn("absolute inset-0 w-[1.5px] rounded-full", c.line)}
        />
        <motion.div
          className={cn("absolute size-1.5 rounded-full", c.particle)}
          animate={{ y: [-2, 22], opacity: [0, 1, 1, 0] }}
          transition={{
            duration: 1.2,
            repeat: Infinity,
            ease: "linear",
            delay,
          }}
        />
      </div>
    );
  }

  return (
    <div className="relative flex items-center h-px w-8 md:w-12 shrink-0">
      <div
        className={cn(
          "absolute inset-0 h-[1.5px] rounded-full my-auto",
          c.line,
        )}
      />
      <motion.div
        className={cn("absolute size-1.5 rounded-full", c.particle)}
        animate={{ x: [-2, 40], opacity: [0, 1, 1, 0] }}
        transition={{ duration: 1.4, repeat: Infinity, ease: "linear", delay }}
      />
      {/* Arrow */}
      <div
        className="absolute right-0 border-l-[4px] border-y-[2.5px] border-y-transparent border-l-current opacity-30"
        style={{
          color:
            color === "violet"
              ? "#8b5cf6"
              : color === "purple"
                ? "#a855f7"
                : color === "green"
                  ? "#22c55e"
                  : "#ef4444",
        }}
      />
    </div>
  );
}

// ─── Event Row ───────────────────────────────────────────────
function EventRow({
  action,
  status,
  statusLabel,
  lineColor,
  delay,
}: {
  action: string;
  status: "success" | "processing" | "indexed";
  statusLabel: string;
  lineColor: "green" | "violet" | "purple" | "red";
  delay: number;
}) {
  const statusColors = {
    success:
      "text-green-600 dark:text-green-400 bg-green-500/10 border-green-500/20",
    processing:
      "text-violet-600 dark:text-violet-400 bg-violet-500/10 border-violet-500/20",
    indexed:
      "text-green-600 dark:text-green-400 bg-green-500/10 border-green-500/20",
  };

  return (
    <motion.div
      initial={{ opacity: 0, x: -8 }}
      whileInView={{ opacity: 1, x: 0 }}
      viewport={{ once: true }}
      transition={{ duration: 0.4, delay }}
      className="flex items-center gap-0"
    >
      <Connection color={lineColor} delay={delay * 2} />
      <div className="rounded-lg border border-fd-border bg-fd-card/60 px-3 py-1.5 font-mono text-[10px] text-fd-muted-foreground min-w-[110px] text-center">
        {action}
      </div>
      <Connection color={lineColor} delay={delay * 2 + 0.5} />
      <div
        className={cn(
          "rounded-md border px-2 py-1 font-mono text-[10px] font-medium whitespace-nowrap",
          statusColors[status],
        )}
      >
        {statusLabel}
      </div>
    </motion.div>
  );
}

// ─── RAG Pipeline Diagram ─────────────────────────────────────
function RAGPipelineDiagram() {
  const [phase, setPhase] = useState(0);

  useEffect(() => {
    const interval = setInterval(() => {
      setPhase((prev) => (prev + 1) % 3);
    }, 4000);
    return () => clearInterval(interval);
  }, []);

  return (
    <motion.div
      initial={{ opacity: 0 }}
      whileInView={{ opacity: 1 }}
      viewport={{ once: true }}
      transition={{ duration: 0.6 }}
      className="relative"
    >
      {/* Background glow */}
      <div className="absolute inset-0 -m-6 bg-gradient-to-br from-violet-500/5 via-transparent to-indigo-500/5 rounded-3xl blur-xl" />

      <div className="relative p-3 sm:p-6 rounded-2xl border border-fd-border/50 bg-fd-card/30 backdrop-blur-sm">
        <div className="flex flex-col items-center gap-4">
          {/* Pipeline stages */}
          <div className="flex items-center gap-0 flex-wrap justify-center">
            <Stage
              label="Ingest()"
              sublabel={<CyclingPipelineAction />}
              color="text-violet-600 dark:text-violet-400"
              borderColor="border-violet-500/30"
              bgColor="bg-violet-500/5"
              delay={0.1}
            />
            <Connection color="violet" delay={0} />
            <Stage
              label="Chunker"
              sublabel="recursive"
              color="text-purple-600 dark:text-purple-400"
              borderColor="border-purple-500/30"
              bgColor="bg-purple-500/5"
              delay={0.2}
            />
            <Connection color="violet" delay={0.5} />
            <Stage
              label="Embedder"
              sublabel="text-emb-3"
              color="text-violet-600 dark:text-violet-400"
              borderColor="border-violet-500/30"
              bgColor="bg-violet-500/8"
              pulse
              delay={0.3}
            />
          </div>

          {/* Vertical connection to events */}
          <Connection color="violet" horizontal={false} delay={1} />

          {/* Event rows with outcomes */}
          <div className="flex flex-col items-start gap-2.5">
            <EventRow
              action="doc.loaded"
              status="success"
              statusLabel="✓ Parsed"
              lineColor="green"
              delay={0.5}
            />
            <EventRow
              action="chunks.created"
              status={phase === 1 ? "indexed" : "processing"}
              statusLabel={phase === 1 ? "✓ 12 chunks" : "⟳ Chunking"}
              lineColor={phase === 1 ? "green" : "violet"}
              delay={0.6}
            />
            <EventRow
              action="vec.stored"
              status="indexed"
              statusLabel="✓ Indexed"
              lineColor="green"
              delay={0.7}
            />
          </div>

          {/* Legend */}
          <div className="flex items-center gap-4 mt-4 text-[10px] text-fd-muted-foreground">
            <div className="flex items-center gap-1.5">
              <div className="size-2 rounded-full bg-green-500" />
              <span>Ready</span>
            </div>
            <div className="flex items-center gap-1.5">
              <div className="size-2 rounded-full bg-violet-500" />
              <span>Processing</span>
            </div>
            <div className="flex items-center gap-1.5">
              <div className="size-2 rounded-full bg-purple-400" />
              <span>Chunking</span>
            </div>
            <div className="flex items-center gap-1.5">
              <div className="size-2 rounded-full bg-red-500" />
              <span>Failed</span>
            </div>
          </div>
        </div>
      </div>
    </motion.div>
  );
}

// ─── Feature Bullet ──────────────────────────────────────────
function FeatureBullet({
  title,
  description,
  delay,
}: {
  title: string;
  description: string;
  delay: number;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, x: -10 }}
      whileInView={{ opacity: 1, x: 0 }}
      viewport={{ once: true }}
      transition={{ duration: 0.4, delay }}
      className="flex items-start gap-3"
    >
      <div className="mt-1 flex items-center justify-center size-5 rounded-md bg-violet-500/10 shrink-0">
        <svg
          className="size-3 text-violet-500"
          viewBox="0 0 12 12"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M2 6l3 3 5-5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </div>
      <div>
        <h4 className="text-sm font-semibold text-fd-foreground">{title}</h4>
        <p className="text-xs text-fd-muted-foreground mt-0.5 leading-relaxed">
          {description}
        </p>
      </div>
    </motion.div>
  );
}

// ─── RAG Ingestion Pipeline Section ──────────────────────────
export function DeliveryFlowSection() {
  return (
    <section className="relative w-full py-20 sm:py-28 overflow-hidden">
      {/* Background */}
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-violet-500/[0.02] to-transparent" />

      <div className="container max-w-(--fd-layout-width) mx-auto px-4 sm:px-6">
        <div className="grid gap-12 lg:grid-cols-2 lg:gap-16 items-center">
          {/* Left: Text content */}
          <div className="flex flex-col">
            <SectionHeader
              badge="RAG Ingestion Pipeline"
              title="From document to searchable context."
              description="Weave orchestrates the entire ingestion lifecycle — tenant scoping, text chunking, embedding generation, and vector storage."
              align="left"
            />

            <div className="mt-8 space-y-5">
              <FeatureBullet
                title="Automatic Tenant Scoping"
                description="Every ingestion and retrieval is stamped with TenantID and AppID from context. Collection and chunk isolation is enforced at the store layer — no tenant can access another's data."
                delay={0.2}
              />
              <FeatureBullet
                title="Configurable Chunk Strategies"
                description="Recursive or fixed-size chunking with configurable token size and overlap. Per-collection strategy overrides let you tune chunking for different document types."
                delay={0.3}
              />
              <FeatureBullet
                title="Lifecycle Extension Hooks"
                description="OnIngestCompleted, OnRetrievalFailed, and 12 other lifecycle events. Wire in metrics, audit trails, or custom processing logic without modifying engine code."
                delay={0.4}
              />
            </div>

            <motion.div
              initial={{ opacity: 0 }}
              whileInView={{ opacity: 1 }}
              viewport={{ once: true }}
              transition={{ delay: 0.5 }}
              className="mt-8"
            >
              <a
                href="/docs/architecture"
                className="inline-flex items-center gap-1 text-sm font-medium text-violet-600 dark:text-violet-400 hover:text-violet-500 transition-colors"
              >
                Learn about the architecture
                <svg
                  className="size-3.5"
                  viewBox="0 0 16 16"
                  fill="none"
                  aria-hidden="true"
                >
                  <path
                    d="M6 4l4 4-4 4"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </a>
            </motion.div>
          </div>

          {/* Right: Pipeline diagram */}
          <div className="relative">
            <RAGPipelineDiagram />
          </div>
        </div>
      </div>
    </section>
  );
}
