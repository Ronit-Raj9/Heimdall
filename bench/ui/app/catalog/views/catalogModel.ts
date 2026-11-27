import type { CatalogEntry } from "@/lib/types/catalog";
import { displayFor } from "./catalogDisplay";
import {
  Brain,
  Code2,
  Cpu,
  Globe,
  Library,
  type LucideIcon,
  MessagesSquare,
  Mic,
  Monitor,
  Search,
  ShieldCheck,
  SquareTerminal,
  Users,
  Wrench,
} from "lucide-react";

/** Capability categories shown as filter chips, in display order. */
export const CATEGORIES = [
  "coding",
  "terminal",
  "memory",
  "computer",
  "browser",
  "voice",
  "support",
  "tools",
  "research",
  "safety",
  "multiagent",
  "gpu",
] as const;
export type Category = (typeof CATEGORIES)[number] | "general";

export const CATEGORY_ICON: Record<Category, LucideIcon> = {
  coding: Code2,
  terminal: SquareTerminal,
  memory: Brain,
  computer: Monitor,
  browser: Globe,
  voice: Mic,
  support: MessagesSquare,
  tools: Wrench,
  research: Search,
  safety: ShieldCheck,
  multiagent: Users,
  gpu: Cpu,
  general: Library,
};

/** Words in tags, tracks, importer names and ids that map to a category. */
const WORDS: Record<string, Category> = {
  coding: "coding",
  repository: "coding",
  node: "coding",
  swebench: "coding",
  design: "coding",
  visual: "coding",
  terminal: "terminal",
  skills: "terminal",
  stack: "terminal",
  harbor: "terminal",
  memory: "memory",
  longmemeval: "memory",
  locomo: "memory",
  memtask: "memory",
  computer_use: "computer",
  desktop: "computer",
  browser: "browser",
  web: "browser",
  voice: "voice",
  support: "support",
  helpdesk: "support",
  tau2: "support",
  tool_use: "tools",
  mcp: "tools",
  mcpmark: "tools",
  external: "tools",
  a2a: "tools",
  research: "research",
  qa: "research",
  gaia: "research",
  "browsecomp-plus": "research",
  safety: "safety",
  integrity: "safety",
  agentdojo: "safety",
  impossiblebench: "safety",
  team: "multiagent",
  marble: "multiagent",
  gpu: "gpu",
  ml_engineering: "gpu",
  kernelbench: "gpu",
};

function words(e: CatalogEntry): string[] {
  const idParts = e.id
    .replace(/^(example|import|suite)-/, "")
    .replace(/^acme-/, "")
    .split(/[-_]/);
  return [
    ...(e.tags ?? []),
    e.track ?? "",
    e.importer ?? "",
    e.id.replace(/^import-/, ""),
    ...idParts,
  ]
    .map((w) => w.toLowerCase())
    .filter(Boolean);
}

/** Every category an entry belongs to; the first is its primary one. */
export function entryCategories(e: CatalogEntry): Category[] {
  const out: Category[] = [];
  for (const w of words(e)) {
    const c = WORDS[w];
    if (c && !out.includes(c)) out.push(c);
  }
  return out.length ? out : ["general"];
}

export type SourceFilter = "all" | "ready" | "needs";

export interface CatalogFilters {
  category: Category | "all";
  source: SourceFilter;
  query: string;
}

export const NO_FILTERS: CatalogFilters = {
  category: "all",
  source: "all",
  query: "",
};

/** Entries matching the category chip, the source filter and a free-text query. */
export function filterEntries(
  entries: CatalogEntry[],
  f: CatalogFilters,
): CatalogEntry[] {
  const q = f.query.trim().toLowerCase();
  return entries.filter((e) => {
    if (f.category !== "all" && !entryCategories(e).includes(f.category))
      return false;
    if (f.source === "ready" && e.needs_data) return false;
    if (f.source === "needs" && !e.needs_data) return false;
    if (!q) return true;
    const d = displayFor(e);
    return [
      d.title,
      d.description,
      e.title,
      e.id,
      e.summary,
      ...(e.tags ?? []),
      ...(e.benchmarks ?? []),
    ]
      .join(" ")
      .toLowerCase()
      .includes(q);
  });
}

function cap(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

/** A short card name, a human tagline when the title carried one, and whether it is an example. */
export function cleanTitle(e: CatalogEntry): {
  name: string;
  tagline?: string;
  example: boolean;
} {
  const m = /^acme\s+(.*?)\s*example(?:\s+project)?\s*(?::\s*(.*))?$/i.exec(
    e.title.trim(),
  );
  if (m) {
    let name = m[1].trim();
    if (!name)
      name = e.id
        .replace(/^example-/, "")
        .replace(/^acme-/, "")
        .replace(/[-_]/g, " ");
    const tagline = m[2] ? cap(m[2].trim()) : undefined;
    // Plain card name: "Memory test (example)". Short all-caps names (MCP, GPU) keep their case.
    const base =
      name.length <= 4 && name === name.toUpperCase() ? name : cap(name);
    return { name: `${base} test (example)`, tagline, example: true };
  }
  if (e.kind === "bundled" && /^[a-z0-9_-]+$/.test(e.title))
    return { name: cap(e.title.replace(/[-_]/g, " ")), example: false };
  return { name: e.title, example: e.kind === "example" };
}

/** One readable line for the card (curated, see catalogDisplay). */
export function cardDescription(e: CatalogEntry): string {
  return displayFor(e).description;
}

const FEATURED = [
  "terminal-bench",
  "skillsbench",
  "livecodebench",
  "swe",
  "longmemeval",
  "gaia",
  "tau2",
  "browsecomp",
];

export type SectionId = "featured" | "try" | "import";

/** Which section a card sits in. */
export function sectionOf(e: CatalogEntry): SectionId {
  if (e.kind === "example" || e.kind === "bundled") return "try";
  const id = e.id.replace(/^import-/, "");
  if (
    !e.id.startsWith("import-") ||
    FEATURED.some((f) => id.startsWith(f) && id !== "swebench")
  )
    return "featured";
  return "import";
}

export const SECTIONS: SectionId[] = ["featured", "try", "import"];

/** Suites that only Heimdall itself uses (the improver task) or test fixtures: never offered in the catalog (N6-1). */
const INTERNAL_SUITES = new Set(["heimdall-improver"]);

/** The suite an entry adds, when it adds one. */
export function entrySuite(e: CatalogEntry): string | undefined {
  return e.suite_id ?? (e.id.startsWith("suite-") ? e.id.slice("suite-".length) : undefined);
}

/** An internal entry: a server flag when present, else the internal suite list and zz-test fixtures. */
export function isInternalEntry(e: CatalogEntry): boolean {
  if ((e as CatalogEntry & { internal?: boolean }).internal === true) return true;
  const suite = entrySuite(e);
  return !!suite && (INTERNAL_SUITES.has(suite) || suite.startsWith("zz-test-"));
}

/**
 * The entries the catalog lists: internal ones left out, and one entry per suite (M6-64). The
 * server lists headline benchmarks first, so when two entries add the same suite the first stays.
 */
export function catalogEntries(entries: readonly CatalogEntry[]): CatalogEntry[] {
  const out: CatalogEntry[] = [];
  const bySuite = new Map<string, number>();
  for (const e of entries) {
    if (isInternalEntry(e)) continue;
    const suite = entrySuite(e);
    if (!suite) {
      out.push(e);
      continue;
    }
    if (bySuite.has(suite)) continue;
    bySuite.set(suite, out.length);
    out.push(e);
  }
  return out;
}

/** Where an installed entry lives: its suite page, else the agents or suites list. */
export function installedHref(e: CatalogEntry): string {
  const suite = e.suite_id ?? e.suites?.[0];
  if (suite) return `/suites/${encodeURIComponent(suite)}`;
  return (e.agents?.length ?? 0) > 0 ? "/agents" : "/suites";
}
