// Pure helpers of the run trace view: step kind classes, filters, search, token and cost per
// step, the input/output split of a row, and a JSON tokenizer for highlighting. Tested without a page.
import type { BenchEvent, StepRecord } from "@/lib/types/bench";

export type TraceKind =
  "model" | "tool" | "command" | "file" | "verifier" | "other";
export const TRACE_FILTERS = [
  "all",
  "model",
  "tools",
  "commands",
  "errors",
  "mismatches",
] as const;
export type TraceFilter = (typeof TRACE_FILTERS)[number];

/** The display class of a step: what icon and label it gets. */
export function traceKind(
  step: Pick<StepRecord, "kind" | "event_type" | "tool_name">,
): TraceKind {
  switch (step.kind) {
    case "model_turn":
    case "model_attempt":
      return "model";
    case "tool_request":
    case "tool_execution":
      return "tool";
    case "command":
      return "command";
    case "file_change":
      return "file";
  }
  if (step.event_type === "tool_call" || step.event_type === "tool_result")
    return "tool";
  if (/verif|grade|test/i.test(step.tool_name ?? "")) return "verifier";
  return "other";
}

/**
 * True when a step is an error: flagged by its record or a non-zero exit. A witness conflict alone is
 * a mismatch between two records of the run (isMismatchStep), not an error of the agent (N240).
 */
export function isErrorStep(step: StepRecord): boolean {
  return (
    Boolean(step.is_error) ||
    (step.exit_code !== undefined &&
      step.exit_code !== null &&
      step.exit_code !== 0)
  );
}

/** True when two records of the run disagree about a step (a witness conflict). */
export function isMismatchStep(step: StepRecord): boolean {
  return Boolean(step.conflict);
}

/** Steps that pass the filter chip and the search text (case-insensitive over summary, tool, path, kind). */
export function filterTrace(
  steps: readonly StepRecord[],
  filter: TraceFilter,
  query: string,
): StepRecord[] {
  const q = query.trim().toLowerCase();
  return steps.filter((step) => {
    const kind = traceKind(step);
    if (filter === "model" && kind !== "model") return false;
    if (filter === "tools" && kind !== "tool") return false;
    if (filter === "commands" && kind !== "command") return false;
    if (filter === "errors" && !isErrorStep(step)) return false;
    if (filter === "mismatches" && !isMismatchStep(step)) return false;
    if (!q) return true;
    return [
      step.summary,
      step.tool_name,
      step.path,
      step.kind,
      step.event_type,
    ].some((s) => (s ?? "").toLowerCase().includes(q));
  });
}

/** Count of steps per filter chip. */
export function filterCounts(
  steps: readonly StepRecord[],
): Record<TraceFilter, number> {
  const out: Record<TraceFilter, number> = {
    all: steps.length,
    model: 0,
    tools: 0,
    commands: 0,
    errors: 0,
    mismatches: 0,
  };
  for (const step of steps) {
    const kind = traceKind(step);
    if (kind === "model") out.model++;
    if (kind === "tool") out.tools++;
    if (kind === "command") out.commands++;
    if (isErrorStep(step)) out.errors++;
    if (isMismatchStep(step)) out.mismatches++;
  }
  return out;
}

export interface StepUsage {
  tokens: number;
  usd: number | null;
}

/** Tokens and cost of each gateway request, by request id, summed over its events. */
export function usageByRequest(
  events: readonly BenchEvent[] | undefined,
): Map<string, StepUsage> {
  const out = new Map<string, StepUsage>();
  for (const event of events ?? []) {
    if (!event.request_id) continue;
    const tokens = event.tokens
      ? event.tokens.fresh +
        event.tokens.cache_read +
        event.tokens.cache_write +
        event.tokens.out
      : 0;
    const usd =
      event.usd && typeof event.usd.v === "number" ? event.usd.v : null;
    if (!tokens && usd === null) continue;
    const prev = out.get(event.request_id) ?? { tokens: 0, usd: null };
    out.set(event.request_id, {
      tokens: prev.tokens + tokens,
      usd: usd === null ? prev.usd : (prev.usd ?? 0) + usd,
    });
  }
  return out;
}

const INPUT_KEYS = [
  "kind",
  "tool_name",
  "tool_call_id",
  "request_id",
  "path",
  "op",
  "pid",
  "ppid",
  "event_type",
  "channel",
  "summary",
] as const;
const OUTPUT_KEYS = [
  "exit_code",
  "is_error",
  "execution",
  "proof",
  "proof_class",
  "witnessed_by",
  "witness_of",
  "corroboration",
  "conflict",
  "duration_ms",
] as const;

function pick(
  step: StepRecord,
  keys: readonly string[],
): Record<string, unknown> {
  const rec = step as unknown as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const key of keys)
    if (rec[key] !== undefined && rec[key] !== null && rec[key] !== "")
      out[key] = rec[key];
  return out;
}

/** The input half (what was asked) and output half (what happened) of a step record. */
export function stepIO(step: StepRecord): {
  input: Record<string, unknown>;
  output: Record<string, unknown>;
} {
  return { input: pick(step, INPUT_KEYS), output: pick(step, OUTPUT_KEYS) };
}

export type JsonTokenKind = "key" | "string" | "number" | "literal" | "punct";
export interface JsonToken {
  kind: JsonTokenKind;
  text: string;
}

const JSON_TOKEN =
  /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],:])|(\s+)/g;

/** Tokens of pretty JSON for highlighting. Rendered as React text only. */
export function tokenizeJson(value: unknown): JsonToken[] {
  const text = JSON.stringify(value, null, 2) ?? "";
  const out: JsonToken[] = [];
  let last = 0;
  for (const m of text.matchAll(JSON_TOKEN)) {
    if (m.index > last)
      out.push({ kind: "punct", text: text.slice(last, m.index) });
    if (m[1] !== undefined) {
      out.push({ kind: m[2] ? "key" : "string", text: m[1] });
      if (m[2]) out.push({ kind: "punct", text: m[2] });
    } else if (m[3] !== undefined) out.push({ kind: "number", text: m[3] });
    else if (m[4] !== undefined) out.push({ kind: "literal", text: m[4] });
    else out.push({ kind: "punct", text: m[0] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ kind: "punct", text: text.slice(last) });
  return out;
}

/** A duration in ms as "820 ms", "4.2 s" or "3m 05s". */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${String(s).padStart(2, "0")}s`;
}

/** The run's trace download url for one format. */
export function runTraceUrl(
  base: string,
  runId: string,
  format: "json" | "otlp" | "jsonl",
): string {
  return `${base}/runs/${encodeURIComponent(runId)}/trace?format=${format}`;
}

/**
 * The selected step after the page's ?step= value changes from `prev` to `next` while the trace is open
 * ("Show that step" on another tab): a newly asked-for step is selected; an unchanged or removed value
 * keeps the current selection. Pure.
 */
export function stepParamSelection(prev: number | undefined, next: number | undefined, current: number | null): number | null {
	return next !== undefined && next !== prev ? next : current;
}
