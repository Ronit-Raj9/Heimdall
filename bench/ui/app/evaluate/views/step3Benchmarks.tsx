import { BenchmarkChecklist } from "@/components/evaluations/benchmarkChecklist";
import { RedTeamPanel } from "@/components/redteam/redTeamPanel";
import { hw11 } from "@/components/runs/debug/hw11I18n";
import { Button } from "@/components/ui/button";
import { suiteDisplayCategory, suiteSections } from "@/components/suites/suiteDisplay";
import { getBenchErrorMessage, useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import { LoadError } from "@/components/runs/screenStates";
import "@/app/evaluations_/$evalId/views/w4m2I18n";
import {
	type AgentCompatibility,
	type BenchmarkCompatibility,
	type EvaluationPack,
	useGetAgentCompatibilityQuery,
	useGetEvaluationPacksQuery,
} from "@/lib/store/apis/evaluationsApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { entriesByBenchmark, referenceHint } from "@/components/reference/reference.utils";
import { type ReferenceEntry, useGetReferenceBoardQuery } from "@/lib/store/apis/referenceApi";
import "@/lib/types/referenceI18n";
import "./fw06I18n";
import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";
import { useEvaluateDraft } from "./evaluateDraft";
import { OwnTasksCard } from "./ownTasks";
import { fw05 } from "./fw05I18n";

// Lane A12: wizard step 3, "Benchmarks". Pick a pack or any set of single benchmarks. Benchmarks the
// chosen agent cannot run are greyed out with the reason from /agents/{id}/compatibility. In an A/B
// evaluation every setup's agent is checked: a benchmark is runnable only when all of them can run it.

/**
 * The slice of the wizard draft this step reads and writes. Field names follow EvaluationSpec
 * (audit/AGENTFIRST_CONTRACT.md section 3); the full draft lives with the wizard (lane A11).
 */
export interface EvaluateDraftBenchmarks {
	agent: string;
	benchmarks: string[];
	pack?: string;
	/** The other setups of an A/B evaluation (arms after A), with their agent. */
	arms?: readonly { label: string; agent: string }[];
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
	// Only the shell benchmarks among the imported Harbor packs read as Terminal by id. BFCL is function
	// calling (tools); its import track says "terminal" only because that is the Harbor importer's default.
	const bare = benchmarkKey(id);
	if (bare === "hd-harbor-tb2" || bare === "hd-harbor-skills") return "terminal";
	if (bare.startsWith("hd-harbor-bfcl")) return "support";
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
		out.set(benchmarkKey(id), {
			id,
			ok,
			requires: Array.isArray(r.requires) ? r.requires : [],
			missing,
			reason,
			...(r.note ? { note: r.note } : {}),
		});
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

/** Whether a benchmark has at least one ready (admitted) task: only those are offered for an evaluation. */
export function hasReadyTasks(s: Pick<SuiteRow, "admitted_count">): boolean {
	return (s.admitted_count ?? 0) > 0;
}

/**
 * Built-in and org benchmarks grouped by capability (groups in CAPABILITY_ORDER, rows in display
 * order). A real evaluation runs real benchmarks only: the practice examples (Acme, coding-core) and
 * benchmarks with no ready task are left out; the Benchmarks page lists the latter as "Not ready yet".
 */
export function groupBenchmarks(suites: readonly SuiteRow[]): { groups: BenchmarkGroup[] } {
	const sections = suiteSections(suites.filter(hasReadyTasks));
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
	return { groups };
}

/** One other setup of an A/B evaluation and its agent's compatibility answer (undefined while loading). */
export interface ArmCompatibility {
	label: string;
	agent: string;
	compatibility?: AgentCompatibility;
}

/** The setups after A whose agent differs from A's, once per agent (the first label wins). */
export function otherArmAgents(
	primary: string,
	arms: readonly { label: string; agent: string }[] | undefined,
): { label: string; agent: string }[] {
	const seen = new Set<string>([primary]);
	const out: { label: string; agent: string }[] = [];
	for (const a of arms ?? []) {
		if (!a.agent || seen.has(a.agent)) continue;
		seen.add(a.agent);
		out.push({ label: a.label, agent: a.agent });
	}
	return out;
}

/**
 * The primary agent's compatibility with every other setup's answer folded in: a benchmark the
 * primary agent can run but another setup's agent cannot becomes not runnable, with
 * describe(agent, setup, reason) as its reason. Undefined while the primary answer is unknown.
 */
export function mergeArmCompatibility(
	primary: AgentCompatibility | undefined,
	others: readonly ArmCompatibility[],
	describe: (agent: string, setup: string, reason: string) => string,
): AgentCompatibility | undefined {
	if (!primary) return undefined;
	const base = compatibilityMap(primary);
	const rows = new Map<string, BenchmarkCompatibility>(base);
	for (const o of others) {
		if (!o.compatibility) continue;
		for (const [key, c] of compatibilityMap(o.compatibility)) {
			const own = rows.get(key);
			if (own && !own.ok) continue; // the primary agent's own reason comes first
			if (!incompatibleReason(c)) continue;
			const why = c.reason || (c.missing.length ? `Needs ${c.missing.join(", ")}` : "");
			rows.set(key, {
				id: own?.id ?? c.id,
				ok: false,
				requires: own?.requires ?? c.requires,
				missing: c.missing,
				reason: describe(o.agent, o.label, why),
			});
		}
	}
	return { ...primary, benchmarks: [...rows.values()] };
}

/** selected without the benchmarks compatibility says the agent cannot run. */
export function keepRunnable(selected: readonly string[], compatibility: AgentCompatibility | undefined): string[] {
	const compat = compatibilityMap(compatibility);
	return selected.filter((id) => !incompatibleReason(compatFor(compat, id)));
}

/** Pack members present in the suite list and runnable by the agent. */
export function packSelection(pack: EvaluationPack, suites: readonly SuiteRow[], compat: Map<string, BenchmarkCompatibility>): string[] {
	const present = new Set(suites.filter(hasReadyTasks).map((s) => s.id));
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
	/** Published reference scores (gap G4-03), every benchmark; absent or empty shows no hint. */
	reference?: readonly ReferenceEntry[];
	/** The "Your own tasks" card, shown first (the container passes it; the pure view stays data-free). */
	ownTasks?: ReactNode;
	/** The note naming the picks this agent cannot run, shown above the summary. */
	leftOut?: ReactNode;
}

/** Pure view: packs, grouped multi-select and the selected count. Data comes in through props. */
export function Step3BenchmarksView({
	suites,
	packs,
	compatibility,
	selected,
	pack,
	onChange,
	loading,
	checking,
	reference,
	ownTasks,
	leftOut,
}: Step3BenchmarksViewProps) {
	const { t: tr } = useTranslation("reference");
	const compat = compatibilityMap(compatibility);
	const known = entriesByBenchmark(reference ?? []);
	const ready = new Map(suites.map((s) => [s.id, s.admitted_count ?? 0]));
	const chosen = new Set(selected);

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

	// "Agents like codex score about 42% here": only from published reference runs, never a guess.
	const hintLine = (id: string) => {
		const h = referenceHint(known.get(benchmarkKey(id)));
		if (!h) return null;
		return (
			<span className="text-primary/90 block truncate text-xs" data-testid={`step3-reference-${id}`}>
				{h.kind === "one"
					? tr("hint.one", { agent: h.agent, score: h.score })
					: tr("hint.many", { agent: h.agent, low: h.low, high: h.high })}
			</span>
		);
	};

	return (
		<div className="flex flex-col gap-6" data-testid="step3-benchmarks">
			{ownTasks}
			<section>
				<h3 className="mb-2 text-sm font-medium">Start from a pack</h3>
				{packs.length === 0 ? (
					<p className="text-muted-foreground text-sm">{loading ? "Loading packs..." : "No packs available."}</p>
				) : (
					<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
						{packs.map((p) => {
							const members = p.benchmarks.filter((id) => (ready.get(id) ?? 0) > 0);
							const runnable = packSelection(p, suites, compat);
							const tasks = runnable.reduce((n, id) => n + (ready.get(id) ?? 0), 0);
							const active = pack === p.id;
							// Say whose counts the card shows: every member, the ones the agent can run, or none and why.
							const counts = { benchmarks: plural(members.length, "benchmark"), tasks: plural(tasks, "ready task") };
							const firstReason = members.map((id) => incompatibleReason(compatFor(compat, id))).find(Boolean) ?? "";
							const why = firstReason.replace(/^agent .* cannot run benchmark [^:]*:\s*/i, "").replace(/^./, (c) => c.toLowerCase());
							const countText =
								runnable.length >= members.length
									? fw05("pack.all", counts)
									: runnable.length === 0
										? fw05("pack.none", { benchmarks: counts.benchmarks, reason: why })
										: fw05("pack.some", { ...counts, runnable: String(runnable.length) });
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
									<span className="text-muted-foreground mt-1.5 block text-xs tabular-nums" data-testid={`step3-pack-${p.id}-counts`}>
										{countText}
									</span>
								</button>
							);
						})}
					</div>
				)}
			</section>

			<section>
				<h3 className="mb-2 text-sm font-medium">Or pick benchmarks</h3>
				<BenchmarkChecklist
					suites={suites}
					compatibility={compatibility}
					checking={checking}
					selected={selected}
					onToggle={toggle}
					loading={loading}
					testIdPrefix="step3"
					extra={hintLine}
				/>
			</section>

			{leftOut}
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
	const slice: EvaluateDraftBenchmarks = {
		agent: draft.agent ?? "",
		benchmarks: draft.benchmarks,
		pack: draft.pack ?? undefined,
		arms: draft.arms.slice(1),
	};
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
	const reference = useGetReferenceBoardQuery(); // a failure only hides the hints
	const { t } = useTranslation("fw06");
	const others = otherArmAgents(draft.agent, draft.arms);
	const arms = useArmCompatibility(others);
	// currentData, not data: data keeps the previous agent's answer while the new one loads.
	const compatibility = mergeArmCompatibility(compat.currentData, arms.answers, (agent, setup, reason) =>
		reason ? t("step3.otherArmCannot", { agent, setup, reason }) : t("step3.otherArmCannotGeneric", { agent, setup }),
	);
	// Fix wave 3, N187: a setup whose check failed is never read as runnable. Step 3 stays blocked
	// (nothing selected, rows waiting) until a retry answers.
	const failed = failedSetupChecks({ label: "A", agent: draft.agent, failed: Boolean(draft.agent) && compat.isError }, arms.failed);
	const checking = Boolean(draft.agent) && ((!compat.currentData && !compat.isError) || arms.checking || failed.length > 0);
	// Benchmarks picked before the answer came (or for another agent) that this agent cannot run
	// leave the selection, so the launch never carries them.
	const runnable = failed.length > 0 ? [] : keepRunnable(draft.benchmarks, compatibility);
	const answered = compatibility?.benchmarks.map((b) => `${b.id}:${b.ok}`).join(",");
	const [leftOut, setLeftOut] = useState<LeftOutItem[]>([]);
	useEffect(() => setLeftOut([]), [draft.agent]);
	useEffect(() => {
		if (failed.length === 0 && runnable.length !== draft.benchmarks.length) {
			const map = compatibilityMap(compatibility);
			const dropped = draft.benchmarks
				.filter((id) => !runnable.includes(id))
				.map((id) => ({ id, reason: incompatibleReason(compatFor(map, id)) }));
			setLeftOut((prev) => [...prev.filter((p) => !dropped.some((d) => d.id === p.id)), ...dropped]);
		}
		if (runnable.length !== draft.benchmarks.length) onChange({ benchmarks: runnable, ...(failed.length > 0 ? { pack: undefined } : {}) });
		else if (failed.length > 0 && draft.pack) onChange({ pack: undefined });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [answered, draft.benchmarks.join(","), failed.length, draft.pack]);
	const retry = () => {
		if (compat.isError) void compat.refetch();
		arms.retry();
	};
	const lists = listsState(suites, packs);
	const retryLists = () => {
		void suites.refetch();
		void packs.refetch();
	};
	return (
		<div className="flex flex-col gap-6">
			{failed.length > 0 && <SetupCheckFailed failed={failed} onRetry={retry} reason={setupCheckReason(compat.error)} />}
			{lists === "error" ? (
				<LoadError title={w4m2("listsFailed")} error={suites.error ?? packs.error} onRetry={retryLists} testId="step3-lists-error" />
			) : (
				<Step3BenchmarksView
					suites={suites.data?.suites ?? []}
					packs={packs.data ?? []}
					compatibility={compatibility}
					selected={draft.benchmarks}
					pack={draft.pack}
					onChange={onChange}
					loading={suites.isLoading || packs.isLoading}
					checking={checking}
					reference={reference.data?.entries}
					leftOut={<LeftOutNote items={leftOut} />}
					ownTasks={
						<OwnTasksCard
							onAdd={(id) =>
								onChange({ benchmarks: draft.benchmarks.includes(id) ? draft.benchmarks : [...draft.benchmarks, id], pack: undefined })
							}
						/>
					}
				/>
			)}
			{others.map((o) => (
				<ArmCompatProbe key={o.agent} agent={o.agent} onAnswer={arms.report} retry={arms.retries} />
			))}
			{/* Z04: write the org's red-team benchmark and add it here. */}
			<RedTeamPanel
				selected={draft.benchmarks}
				onAdd={(id) => onChange({ benchmarks: [...new Set([...draft.benchmarks, id])], pack: undefined })}
			/>
		</div>
	);
}
interface ProbeAnswer {
	compatibility?: AgentCompatibility;
	failed: boolean;
}

/**
 * Compatibility of every other setup's agent. Hooks cannot be called a variable number of times, so
 * each agent is queried by an ArmCompatProbe the caller renders (one per agent), which reports here.
 * checking is true until every agent has an answer or failed (a failed answer reads as runnable,
 * like the primary agent's).
 */
export function useArmCompatibility(others: readonly { label: string; agent: string }[]) {
	const [byAgent, setByAgent] = useState<Record<string, ProbeAnswer>>({});
	const [retries, setRetries] = useState(0);
	const report = (agent: string, answer: ProbeAnswer) =>
		setByAgent((prev) =>
			prev[agent]?.compatibility === answer.compatibility && prev[agent]?.failed === answer.failed ? prev : { ...prev, [agent]: answer },
		);
	const answers: ArmCompatibility[] = others.map((o) => ({ ...o, compatibility: byAgent[o.agent]?.compatibility }));
	const checking = others.some((o) => !byAgent[o.agent]?.compatibility && !byAgent[o.agent]?.failed);
	// Fix wave 3, N187: setups whose check failed, and a retry that asks each probe again.
	const failed = others.map((o) => ({ ...o, failed: Boolean(byAgent[o.agent]?.failed && !byAgent[o.agent]?.compatibility) }));
	const retry = () => setRetries((n) => n + 1);
	return { answers, checking, report, failed, retries, retry };
}

/** Queries one agent's compatibility and reports the answer; renders nothing. retry asks again when it grows. */
function ArmCompatProbe({
	agent,
	onAnswer,
	retry = 0,
}: {
	agent: string;
	onAnswer: (agent: string, answer: ProbeAnswer) => void;
	retry?: number;
}) {
	const q = useGetAgentCompatibilityQuery(agent);
	useEffect(() => {
		onAnswer(agent, { compatibility: q.currentData, failed: q.isError });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [agent, q.currentData, q.isError]);
	useEffect(() => {
		if (retry > 0 && q.isError) void q.refetch();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [retry]);
	return null;
}

/** One setup and whether its compatibility check failed. */
export interface SetupCheck {
	label: string;
	agent: string;
	failed: boolean;
}

/** The setups whose compatibility check failed (fix wave 3, N187), setup A first; empty when all answered. */
export function failedSetupChecks(primary: SetupCheck, others: readonly SetupCheck[]): { label: string; agent: string }[] {
	return [primary, ...others].filter((s) => s.failed && s.agent).map(({ label, agent }) => ({ label, agent }));
}

/** Step 3 is blocked while a setup could not be checked: one line per setup and a Retry button. */
export function SetupCheckFailed({
	failed,
	onRetry,
	reason,
}: {
	failed: readonly { label: string; agent: string }[];
	onRetry: () => void;
	/** The server's answer when it says why (too many requests, no connection). */
	reason?: string;
}) {
	return (
		<div
			className="border-chart-error/40 bg-chart-error/5 flex flex-col gap-2 rounded-md border px-3 py-2.5 text-sm"
			role="alert"
			data-testid="step3-setup-check-failed"
		>
			{failed.map((f) => (
				<p key={`${f.label}-${f.agent}`}>{hw11("step3.setupCheckFailed", { label: f.label, agent: f.agent })}</p>
			))}
			{reason && (
				<p className="text-muted-foreground text-xs" data-testid="step3-setup-check-reason">
					{reason}
				</p>
			)}
			<Button type="button" variant="outline" size="sm" className="w-fit" onClick={onRetry} data-testid="step3-setup-check-retry">
				{hw11("step3.retry")}
			</Button>
		</div>
	);
}
const w4m2 = (key: string, opts?: Record<string, unknown>): string => String(i18n.t(key as never, { ns: "w4m2", ...opts } as never));

/** A benchmark taken out of the selection because this agent cannot run it. */
export interface LeftOutItem {
	id: string;
	reason: string;
}

/** Says which picks were taken out of the selection and why; nothing when none were. */
export function LeftOutNote({ items }: { items: readonly LeftOutItem[] }) {
	if (items.length === 0) return null;
	return (
		<div className="text-muted-foreground flex flex-col gap-1 text-sm" role="status" data-testid="step3-left-out">
			<p>{w4m2("leftOut", { count: items.length })}</p>
			{items.map((it) => (
				<p key={it.id} className="text-xs" data-testid={`step3-left-out-${it.id}`}>
					{w4m2("leftOutItem", { benchmark: it.id, reason: it.reason })}
				</p>
			))}
		</div>
	);
}

/** Whether the benchmark and pack lists can be shown: an errored query is an error, never an empty list. */
export function listsState(
	suites: { isError?: boolean; isLoading?: boolean },
	packs: { isError?: boolean; isLoading?: boolean },
): "error" | "ready" | "loading" {
	if (suites.isError || packs.isError) return "error";
	if (suites.isLoading || packs.isLoading) return "loading";
	return "ready";
}

/** The server's reason for a failed setup check, shown only for too many requests or no connection. */
function setupCheckReason(error: unknown): string | undefined {
	const status = (error as { status?: unknown } | undefined)?.status;
	if (status === 429 || status === "FETCH_ERROR" || status === "TIMEOUT_ERROR") return getBenchErrorMessage(error);
	return undefined;
}