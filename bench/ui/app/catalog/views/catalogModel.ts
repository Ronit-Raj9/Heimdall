import type { CatalogEntry } from "@/lib/types/catalog";
import { Brain, Code2, Cpu, Globe, Library, type LucideIcon, MessagesSquare, Mic, Monitor, Search, ShieldCheck, SquareTerminal, Users, Wrench } from "lucide-react";

/** Capability categories shown as filter chips, in display order. */
export const CATEGORIES = ["coding", "terminal", "memory", "computer", "browser", "voice", "support", "tools", "research", "safety", "multiagent", "gpu"] as const;
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

/** Soft tile colors per category (light and dark). */
export const CATEGORY_TINT: Record<Category, string> = {
	coding: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
	terminal: "bg-zinc-500/10 text-zinc-700 dark:text-zinc-300",
	memory: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
	computer: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
	browser: "bg-cyan-500/10 text-cyan-600 dark:text-cyan-400",
	voice: "bg-pink-500/10 text-pink-600 dark:text-pink-400",
	support: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
	tools: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
	research: "bg-teal-500/10 text-teal-600 dark:text-teal-400",
	safety: "bg-rose-500/10 text-rose-600 dark:text-rose-400",
	multiagent: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
	gpu: "bg-lime-500/10 text-lime-700 dark:text-lime-400",
	general: "bg-muted text-muted-foreground",
};

/** Words in tags, tracks, importer names and ids that map to a category. */
const WORDS: Record<string, Category> = {
	coding: "coding", repository: "coding", node: "coding", swebench: "coding", design: "coding", visual: "coding",
	terminal: "terminal", skills: "terminal", stack: "terminal", harbor: "terminal",
	memory: "memory", longmemeval: "memory", locomo: "memory", memtask: "memory",
	computer_use: "computer", desktop: "computer",
	browser: "browser", web: "browser",
	voice: "voice",
	support: "support", helpdesk: "support", tau2: "support",
	tool_use: "tools", mcp: "tools", mcpmark: "tools", external: "tools", a2a: "tools",
	research: "research", qa: "research", gaia: "research", "browsecomp-plus": "research",
	safety: "safety", integrity: "safety", agentdojo: "safety", impossiblebench: "safety",
	team: "multiagent", marble: "multiagent",
	gpu: "gpu", ml_engineering: "gpu", kernelbench: "gpu",
};

function words(e: CatalogEntry): string[] {
	const idParts = e.id.replace(/^(example|import|suite)-/, "").replace(/^acme-/, "").split(/[-_]/);
	return [...(e.tags ?? []), e.track ?? "", e.importer ?? "", e.id.replace(/^import-/, ""), ...idParts].map((w) => w.toLowerCase()).filter(Boolean);
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

export const NO_FILTERS: CatalogFilters = { category: "all", source: "all", query: "" };

/** Entries matching the category chip, the source filter and a free-text query. */
export function filterEntries(entries: CatalogEntry[], f: CatalogFilters): CatalogEntry[] {
	const q = f.query.trim().toLowerCase();
	return entries.filter((e) => {
		if (f.category !== "all" && !entryCategories(e).includes(f.category)) return false;
		if (f.source === "ready" && e.needs_data) return false;
		if (f.source === "needs" && !e.needs_data) return false;
		if (!q) return true;
		return [e.title, e.id, e.summary, ...(e.tags ?? []), ...(e.benchmarks ?? [])].join(" ").toLowerCase().includes(q);
	});
}

function cap(s: string): string {
	return s ? s[0].toUpperCase() + s.slice(1) : s;
}

/** A short card name, a human tagline when the title carried one, and whether it is an example. */
export function cleanTitle(e: CatalogEntry): { name: string; tagline?: string; example: boolean } {
	const m = /^acme\s+(.*?)\s*example(?:\s+project)?\s*(?::\s*(.*))?$/i.exec(e.title.trim());
	if (m) {
		let name = m[1].trim();
		if (!name) name = e.id.replace(/^example-/, "").replace(/^acme-/, "").replace(/[-_]/g, " ");
		if (name.length <= 4 && name === name.toUpperCase()) return { name, tagline: m[2] ? cap(m[2].trim()) : undefined, example: true };
		return { name: cap(name), tagline: m[2] ? cap(m[2].trim()) : undefined, example: true };
	}
	if (e.kind === "bundled" && /^[a-z0-9_-]+$/.test(e.title)) return { name: cap(e.title.replace(/[-_]/g, " ")), example: false };
	return { name: e.title, example: e.kind === "example" };
}

/** One readable line for the card: the tagline, else the summary. */
export function cardDescription(e: CatalogEntry): string {
	return cleanTitle(e).tagline ?? e.summary;
}

const FEATURED = ["terminal-bench", "skillsbench", "livecodebench", "swe", "longmemeval", "gaia", "tau2", "browsecomp"];

export type SectionId = "featured" | "try" | "import";

/** Which section a card sits in. */
export function sectionOf(e: CatalogEntry): SectionId {
	if (e.kind === "example" || e.kind === "bundled") return "try";
	const id = e.id.replace(/^import-/, "");
	if (!e.id.startsWith("import-") || FEATURED.some((f) => id.startsWith(f) && id !== "swebench")) return "featured";
	return "import";
}

export const SECTIONS: SectionId[] = ["featured", "try", "import"];

/** Where an installed entry lives: its suite page, else the agents or suites list. */
export function installedHref(e: CatalogEntry): string {
	const suite = e.suite_id ?? e.suites?.[0];
	if (suite) return `/suites/${encodeURIComponent(suite)}`;
	return (e.agents?.length ?? 0) > 0 ? "/agents" : "/suites";
}
