import { suiteDisplayCategory, suiteDisplayDescription, suiteDisplayName, suiteSections } from "@/components/suites/suiteDisplay";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import {
	type AgentCompatibility,
	type BenchmarkCompatibility,
	type EvaluationPack,
	useGetAgentCompatibilityQuery,
	useGetEvaluationPacksQuery,
} from "@/lib/store/apis/evaluationsApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronRight } from "lucide-react";
import { useEffect } from "react";
import { useEvaluateDraft } from "./evaluateDraft";

// Lane A12: wizard step 3, "Benchmarks". Pick a pack or any set of single benchmarks. Benchmarks the
// chosen agent cannot run are greyed out with the reason from /agents/{id}/compatibility.

/**
 * The slice of the wizard draft this step reads and writes. Field names follow EvaluationSpec
 * (audit/AGENTFIRST_CONTRACT.md section 3); the full draft lives with the wizard (lane A11).
 */
export interface EvaluateDraftBenchmarks {
	agent: string;
	benchmarks: string[];
	pack?: string;
}

export interface Step3BenchmarksProps<D extends EvaluateDraftBenchmarks = EvaluateDraftBenchmarks> {
	draft: D;
	onChange: (patch: Partial<EvaluateDraftBenchmarks>) => void;
}

type SuiteRow = Pick<SuiteSummary, "id" | "description" | "admitted_count" | "builtin" | "source" | "track" | "environment">;

/** Capability groups in display order (contract section 4). */
export const CAPABILITY_ORDER = ["terminal", "coding", "support", "research", "memory", "safety", "team", "voice", "gpu", "other"] as const;
export type BenchmarkCapability = (typeof CAPABILITY_ORDER)[number];

const CAPABILITY_LABEL: Record<BenchmarkCapability, string> = {
	terminal: "Terminal",
	coding: "Coding",
	support: "Support and tools",
	research: "Research",
	memory: "Memory",
	safety: "Safety",
	team: "Team of agents",
	voice: "Voice",
	gpu: "GPU",
	other: "Other",
};

const PACK_DESCRIPTION: Record<string, string> = {
	coding: "Fix real issues in real code bases.",
	terminal: "Hard jobs in a real Linux shell.",
	support: "Help customers with tools while following policy.",
	memory: "Remember facts across long conversations.",
	research: "Find hard facts by searching.",
	voice: "Answer spoken questions.",
	safety: "Do the job while resisting attacks and cheating.",
	"all-compatible": "Every benchmark this agent can run.",
};

export function isGpuBenchmark(id: string): boolean {
	return id.startsWith("kernelbench");
}

const TRACK_CAPABILITY: Record<string, BenchmarkCapability> = {
	terminal: "terminal",
	skills: "terminal",
	coding: "coding",
	artifact: "coding",
	tool_use: "support",
	mcp: "support",
	support: "support",
	qa: "research",
	browser: "research",
	computer_use: "research",
	memory: "memory",
	safety: "safety",
};

/** Which capability group a benchmark reads under: GPU first, then curated category, id, track. */
export function benchmarkCapability(suite: Pick<SuiteRow, "id" | "track">): BenchmarkCapability {
	const { id } = suite;
	if (isGpuBenchmark(id)) return "gpu";
	const curated = suiteDisplayCategory(id);
	if (curated && (CAPABILITY_ORDER as readonly string[]).includes(curated)) return curated as BenchmarkCapability;
	if (id.startsWith("hd-harbor")) return "terminal";
	if (id.startsWith("hd-swe-ib")) return "safety";
	if (id.startsWith("hd-swe")) return "coding";
	if (id.startsWith("agentdojo")) return "safety";
	if (id.startsWith("browsecomp") || id === "gaia") return "research";
	if (id.startsWith("longmemeval") || id === "locomo") return "memory";
	if (id === "tau2" || id === "mcpmark") return "support";
	if (id.startsWith("marble")) return "team";
	if (id.startsWith("voice")) return "voice";
	return (suite.track && TRACK_CAPABILITY[suite.track]) || "other";
}

/** The key a benchmark id is matched on: no "@version" pin, lower case. */
export function benchmarkKey(id: string): string {
	return id.split("@")[0].trim().toLowerCase();
}

type RawCompatRow = Partial<BenchmarkCompatibility> & { benchmark?: string; suite?: string; suite_id?: string };

/**
 * GET /agents/{id}/compatibility rows keyed by benchmarkKey. Tolerates the row shapes servers have
 * answered with (id, benchmark, suite or suite_id; ok missing; null lists), so a row the agent cannot
 * run is never read as unknown, which would leave it selectable.
 */
export function compatibilityMap(compatibility: AgentCompatibility | undefined): Map<string, BenchmarkCompatibility> {
	const out = new Map<string, BenchmarkCompatibility>();
	const rows = (compatibility?.benchmarks ?? []) as RawCompatRow[];
	for (const r of Array.isArray(rows) ? rows : []) {
		const id = r.id ?? r.benchmark ?? r.suite ?? r.suite_id;
		if (!id) continue;
		const missing = Array.isArray(r.missing) ? r.missing : [];
		const reason = r.reason ?? "";
		const ok = typeof r.ok === "boolean" ? r.ok : missing.length === 0 && !reason;
		out.set(benchmarkKey(id), { id, ok, requires: Array.isArray(r.requires) ? r.requires : [], missing, reason, ...(r.note ? { note: r.note } : {}) });
	}
	return out;
}

/** The compatibility row of benchmark id, matched on benchmarkKey. */
export function compatFor(compat: Map<string, BenchmarkCompatibility>, id: string): BenchmarkCompatibility | undefined {
	return compat.get(benchmarkKey(id));
}

/** Why a benchmark is greyed out, or "" when the agent can run it (unknown compatibility = runnable). */
export function incompatibleReason(c: BenchmarkCompatibility | undefined): string {
	if (!c || c.ok) return "";
	if (c.reason) return c.reason;
	return c.missing?.length ? `Needs ${c.missing.join(", ")}` : "This agent cannot run it";
}

export interface BenchmarkGroup {
	capability: BenchmarkCapability;
	label: string;
	suites: SuiteRow[];
}

/** Built-in and org benchmarks grouped by capability (groups in CAPABILITY_ORDER, rows in display order). */
export function groupBenchmarks(suites: readonly SuiteRow[]): { groups: BenchmarkGroup[]; practice: SuiteRow[] } {
	const sections = suiteSections(suites);
	const byCap = new Map<BenchmarkCapability, SuiteRow[]>();
	for (const s of [...sections.builtin, ...sections.org]) {
		const cap = benchmarkCapability(s);
		byCap.set(cap, [...(byCap.get(cap) ?? []), s]);
	}
	const groups = CAPABILITY_ORDER.filter((c) => byCap.has(c)).map((c) => ({
		capability: c,
		label: CAPABILITY_LABEL[c],
		suites: byCap.get(c)!,
	}));
	return { groups, practice: sections.practice };
}

/** selected without the benchmarks compatibility says the agent cannot run. */
export function keepRunnable(selected: readonly string[], compatibility: AgentCompatibility | undefined): string[] {
	const compat = compatibilityMap(compatibility);
	return selected.filter((id) => !incompatibleReason(compatFor(compat, id)));
}

/** Pack members present in the suite list and runnable by the agent. */
export function packSelection(pack: EvaluationPack, suites: readonly SuiteRow[], compat: Map<string, BenchmarkCompatibility>): string[] {
	const present = new Set(suites.map((s) => s.id));
	return pack.benchmarks.filter((id) => present.has(id) && !incompatibleReason(compatFor(compat, id)));
}

function plural(n: number, word: string): string {
	return `${n} ${word}${n === 1 ? "" : "s"}`;
}

export interface Step3BenchmarksViewProps {
	suites: readonly SuiteRow[];
	packs: readonly EvaluationPack[];
	compatibility?: AgentCompatibility;
	selected: readonly string[];
	pack?: string;
	onChange: (patch: Partial<EvaluateDraftBenchmarks>) => void;
	loading?: boolean;
	/** The agent's compatibility is still loading: rows wait instead of reading as runnable. */
	checking?: boolean;
}

/** Pure view: packs, grouped multi-select and the selected count. Data comes in through props. */
export function Step3BenchmarksView({ suites, packs, compatibility, selected, pack, onChange, loading, checking }: Step3BenchmarksViewProps) {
	const compat = compatibilityMap(compatibility);
	const ready = new Map(suites.map((s) => [s.id, s.admitted_count ?? 0]));
	const chosen = new Set(selected);
	const { groups, practice } = groupBenchmarks(suites);

	const toggle = (id: string) => {
		const next = chosen.has(id) ? selected.filter((b) => b !== id) : [...selected, id];
		onChange({ benchmarks: next, pack: undefined });
	};
	const choosePack = (p: EvaluationPack) => {
		if (pack === p.id) onChange({ pack: undefined, benchmarks: [] });
		else onChange({ pack: p.id, benchmarks: packSelection(p, suites, compat) });
	};

	const readyTasks = selected.reduce((n, id) => n + (ready.get(id) ?? 0), 0);
	const skipped = selected.filter((id) => incompatibleReason(compatFor(compat, id))).length;

	const row = (s: SuiteRow) => {
		const c = compatFor(compat, s.id);
		const reason = incompatibleReason(c);
		const disabled = Boolean(reason) || Boolean(checking);
		const on = chosen.has(s.id) && !disabled;
		const n = s.admitted_count ?? 0;
		const note = reason || (checking ? "Checking whether your agent can run it..." : "") || (isGpuBenchmark(s.id) ? "Needs a GPU worker" : "") || (c?.ok ? (c.note ?? "") : "");
		return (
			<li key={s.id}>
				<label
					data-testid={`step3-benchmark-${s.id}`}
					data-disabled={disabled || undefined}
					title={s.id}
					className={cn(
						"flex items-start gap-3 rounded-md px-2 py-1.5 text-sm",
						disabled ? "cursor-not-allowed opacity-50" : "hover:bg-muted/60 cursor-pointer",
					)}
				>
					<input
						type="checkbox"
						className="accent-primary mt-0.5 size-3.5"
						checked={on}
						disabled={disabled}
						onChange={() => toggle(s.id)}
					/>
					<span className="min-w-0 flex-1">
						<span className="flex items-baseline justify-between gap-3">
							<span className="truncate font-medium">{suiteDisplayName(s.id)}</span>
							<span className={cn("shrink-0 text-xs tabular-nums", n ? "text-muted-foreground" : "text-amber-600 dark:text-amber-400")}>
								{n ? `${plural(n, "ready task")}` : "No ready tasks"}
							</span>
						</span>
						<span className="text-muted-foreground block truncate text-xs" title={note || undefined}>
							{note ? note : suiteDisplayDescription(s)}
						</span>
					</span>
				</label>
			</li>
		);
	};

	return (
		<div className="flex flex-col gap-6" data-testid="step3-benchmarks">
			<section>
				<h3 className="mb-2 text-sm font-medium">Start from a pack</h3>
				{packs.length === 0 ? (
					<p className="text-muted-foreground text-sm">{loading ? "Loading packs..." : "No packs available."}</p>
				) : (
					<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
						{packs.map((p) => {
							const members = p.benchmarks.filter((id) => ready.has(id));
							const runnable = packSelection(p, suites, compat);
							const tasks = runnable.reduce((n, id) => n + (ready.get(id) ?? 0), 0);
							const active = pack === p.id;
							return (
								<button
									key={p.id}
									type="button"
									aria-pressed={active}
									data-testid={`step3-pack-${p.id}`}
									disabled={runnable.length === 0 || Boolean(checking)}
									onClick={() => choosePack(p)}
									className={cn(
										"rounded-md border px-3 py-2.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50",
										active ? "border-primary bg-primary/5" : "hover:bg-muted/50",
									)}
								>
									<span className="block text-sm font-medium">{p.name}</span>
									<span className="text-muted-foreground block truncate text-xs">
										{PACK_DESCRIPTION[p.id] ?? `${CAPABILITY_LABEL[p.capability as BenchmarkCapability] ?? p.capability} benchmarks.`}
									</span>
									<span className="text-muted-foreground mt-1.5 block text-xs tabular-nums">
										{plural(members.length, "benchmark")}
										{runnable.length < members.length ? ` (${runnable.length} runnable)` : ""} · {plural(tasks, "ready task")}
									</span>
								</button>
							);
						})}
					</div>
				)}
			</section>

			<section>
				<h3 className="mb-2 text-sm font-medium">Or pick benchmarks</h3>
				{groups.length === 0 && practice.length === 0 ? (
					<p className="text-muted-foreground text-sm">{loading ? "Loading benchmarks..." : "No benchmarks yet."}</p>
				) : (
					<div className="flex flex-col gap-4">
						{groups.map((g) => (
							<div key={g.capability} data-testid={`step3-group-${g.capability}`}>
								<div className="text-muted-foreground mb-1 px-2 text-xs font-medium tracking-wide uppercase">{g.label}</div>
								<ul className="flex flex-col">{g.suites.map(row)}</ul>
							</div>
						))}
						{practice.length > 0 && (
							<details data-testid="step3-practice" className="group/practice">
								<summary className="text-muted-foreground hover:text-foreground inline-flex cursor-pointer list-none items-center gap-1 px-2 text-xs font-medium select-none [&::-webkit-details-marker]:hidden">
									<ChevronRight className="size-3.5 transition-transform group-open/practice:rotate-90" aria-hidden="true" />
									Practice examples ({practice.length})
								</summary>
								<ul className="mt-1 flex flex-col">{practice.map(row)}</ul>
							</details>
						)}
					</div>
				)}
			</section>

			<div className="text-muted-foreground border-t pt-3 text-sm" data-testid="step3-summary" aria-live="polite">
				{selected.length === 0 ? (
					"No benchmarks selected"
				) : (
					<>
						<span className="text-foreground font-medium">{plural(selected.length, "benchmark")}</span> selected ·{" "}
						{plural(readyTasks, "ready task")}
						{skipped > 0 && ` · ${skipped} this agent cannot run`}
					</>
				)}
			</div>
		</div>
	);
}

/** Wizard step 3 on the shared wizard draft (useEvaluateDraft from ./evaluateDraft). */
export function Step3Benchmarks() {
	const { draft, update } = useEvaluateDraft();
	const slice: EvaluateDraftBenchmarks = { agent: draft.agent ?? "", benchmarks: draft.benchmarks, pack: draft.pack ?? undefined };
	const onChange = (patch: Partial<EvaluateDraftBenchmarks>) =>
		update({
			...(patch.benchmarks ? { benchmarks: patch.benchmarks } : {}),
			...("pack" in patch ? { pack: patch.pack ?? null } : {}),
		});
	return <Step3BenchmarksWith draft={slice} onChange={onChange} />;
}

export default Step3Benchmarks;

/** Step 3 with an explicit draft slice: loads suites, packs and the agent's compatibility, then renders the view. */
export function Step3BenchmarksWith<D extends EvaluateDraftBenchmarks>({ draft, onChange }: Step3BenchmarksProps<D>) {
	const suites = useGetSuitesQuery();
	const packs = useGetEvaluationPacksQuery();
	const compat = useGetAgentCompatibilityQuery(draft.agent, { skip: !draft.agent });
	// currentData, not data: data keeps the previous agent's answer while the new one loads.
	const compatibility = compat.currentData;
	const checking = Boolean(draft.agent) && !compatibility && !compat.isError;
	// Benchmarks picked before the answer came (or for another agent) that this agent cannot run
	// leave the selection, so the launch never carries them.
	const runnable = keepRunnable(draft.benchmarks, compatibility);
	useEffect(() => {
		if (runnable.length !== draft.benchmarks.length) onChange({ benchmarks: runnable });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [compatibility, draft.benchmarks.join(",")]);
	return (
		<Step3BenchmarksView
			suites={suites.data?.suites ?? []}
			packs={packs.data ?? []}
			compatibility={compatibility}
			selected={draft.benchmarks}
			pack={draft.pack}
			onChange={onChange}
			loading={suites.isLoading || packs.isLoading}
			checking={checking}
		/>
	);
}