import type {
	EvaluationArm,
	Report,
	ReportArmMetrics,
	ReportBenchmark,
	ReportCapability,
	ReportReliabilityCard,
	ReportProcess,
} from "@/lib/store/apis/evaluationsApi";
import { formatUsd } from "@/lib/money";
import { formatDuration } from "@/lib/utils";
import reportCardEn from "@/locales/en/reportCard.json";

/** Fills {{name}} slots of an English report card text (locales/en/reportCard.json); pure, so headline() stays testable. */
export function fillText(text: string, values: Record<string, string | number>): string {
	return text.replace(/\{\{(\w+)\}\}/g, (_, k: string) => (k in values ? String(values[k]) : `{{${k}}}`));
}

const HEADLINE = reportCardEn.headline;

/** Under this many counted runs the headline gives a count, not a percent (owner decision M3-2). */
export const MIN_RUNS_TO_RATE = 10;

/** Wilson 95% score interval for successes out of n; [0, 1] when n is 0. */
export function wilson(successes: number, n: number): { low: number; high: number } {
	if (!(n > 0)) return { low: 0, high: 1 };
	const z = 1.96;
	const p = Math.min(1, Math.max(0, successes / n));
	const z2 = z * z;
	const denom = 1 + z2 / n;
	const centre = (p + z2 / (2 * n)) / denom;
	const half = (z * Math.sqrt((p * (1 - p)) / n + z2 / (4 * n * n))) / denom;
	return { low: Math.max(0, centre - half), high: Math.min(1, centre + half) };
}

/**
 * The 95% range to show for a score: the server range, or the Wilson range when the server range has no width
 * (ci_low equal to ci_high) on fewer than 30 runs, so a small sample never reads "100%-100%".
 */
export function scoreRange(m: { score: number; ci_low: number; ci_high: number; runs: number }): { low: number; high: number } {
	if (m.runs > 0 && m.runs < 30 && m.ci_low === m.ci_high) return wilson(Math.round(m.score * m.runs), m.runs);
	return { low: m.ci_low, high: m.ci_high };
}

/** Capability rows, always shown in this order. Others found in the report follow. */
export const CAPABILITY_ORDER = ["terminal", "coding", "memory", "support", "research", "voice", "safety"] as const;

const CAPABILITY_NAMES: Record<string, string> = {
	terminal: "Terminal",
	coding: "Coding",
	memory: "Memory",
	support: "Support and tool use",
	research: "Research",
	voice: "Voice",
	safety: "Safety",
	team: "Teamwork",
	gpu: "GPU",
};

const FAULT_NAMES: Record<string, string> = {
	clean: "No fault",
	"model-down": "Model goes down",
	"model-brownout": "Model partly down",
	"model-slow": "Slow model",
	"model-flaky": "Flaky model",
	"broken-tool": "Broken tool",
	"tool-rate-limit": "Tool rate limit",
	"tool-slow": "Slow tool",
	"tool-flaky": "Flaky tool",
	"tool-partial": "Partial tool results",
	"tool-auth-expired": "Tool login expired",
	"tool-removed": "Tool removed",
	"tool-input-drift": "Tool inputs changed",
	"tool-duplicate-effect": "Tool runs twice",
	"misleading-instruction": "Misleading instruction",
	"compound-brownout-rate-limit": "Model partly down plus tool rate limit",
};

export function capabilityName(id: string): string {
	return CAPABILITY_NAMES[id] ?? id.charAt(0).toUpperCase() + id.slice(1).replace(/[_-]/g, " ");
}

export function faultName(id: string): string {
	return FAULT_NAMES[id] ?? id.replace(/[_-]/g, " ");
}

export function isAB(report: Report): boolean {
	return report.mode !== "score" && report.arms.length >= 2;
}

function onlyLabel<T>(arms: Record<string, T> | undefined, label: string): Record<string, T> {
	return arms && label in arms ? { [label]: arms[label] } : {};
}

/**
 * The report as the page shows it. Mode "score" runs a hidden repeat of the one arm (the planner
 * needs two arms); only the arm asked for is shown, with no A/B verdict.
 */
export function visibleReport(report: Report): Report {
	if (report.mode !== "score") return report;
	const arm = report.arms[0];
	const label = arm?.label ?? "A";
	return {
		...report,
		arms: arm ? [arm] : [],
		capabilities: report.capabilities.map((c) => ({
			...c,
			arms: onlyLabel(c.arms, label),
		})),
		benchmarks: report.benchmarks.map(({ verdict: _verdict, ...b }) => ({
			...b,
			arms: onlyLabel(b.arms, label),
			reliability_card: b.reliability_card && onlyLabel(b.reliability_card, label),
		})),
		reliability: report.reliability.map((r) => ({
			...r,
			arms: onlyLabel(r.arms, label),
		})),
		resilience: report.resilience?.map((r) => ({
			...r,
			arms: onlyLabel(r.arms, label),
		})),
		safety: report.safety && {
			...report.safety,
			attack_resisted: onlyLabel(report.safety.attack_resisted, label),
			cheat_rate: onlyLabel(report.safety.cheat_rate, label),
		},
		alignment: report.alignment && {
			...report.alignment,
			categories: report.alignment.categories.map((c) => ({ ...c, arms: onlyLabel(c.arms, label) })),
			impossible_flagged: report.alignment.impossible_flagged && onlyLabel(report.alignment.impossible_flagged, label),
		},
		overall: undefined,
		learning: report.learning?.filter((l) => l.arm === label),
		persistence: report.persistence?.filter((p) => p.arm === label),
		voice: report.voice?.filter((v) => v.arm === label),
		voice_calls: report.voice_calls?.filter((v) => v.arm === label),
	};
}

/** "12% (5-25%)" for a safety rate; "Not tested" when the arm has none. */
export function alignmentText(rate: { rate: number; ci_low: number; ci_high: number; runs?: number } | undefined): string {
	if (!rate) return "Not tested";
	const r = scoreRange({ score: rate.rate, ci_low: rate.ci_low, ci_high: rate.ci_high, runs: rate.runs ?? 0 });
	return `${pct(rate.rate)} (${Math.round(r.low * 100)}-${pct(r.high)})`;
}

/** "codex v2 on gpt-6.2-sol", "codex v3 (latest at launch) on gpt-6.2-sol". */
export function armDescription(arm: EvaluationArm): string {
	const version = arm.version ? ` v${arm.version}${arm.latest_at_launch ? " (latest at launch)" : ""}` : "";
	return `${arm.agent}${version} on ${arm.model}`;
}

/** A cost column: the sum when every run's cost is known, otherwise "Not known" with how many runs lack a price and why. Never $0 for an unpriced model. */
export interface CostDisplay {
	text: string;
	known: boolean;
	/** "3 of 8 runs unpriced", empty when known. */
	detail: string;
	/** The full reason, for a tooltip or a detail line. */
	reason: string;
}

export function costDisplay(m: Pick<ReportArmMetrics, "runs" | "cost_usd" | "cost_runs" | "cost_note"> | undefined): CostDisplay {
	if (!m || !m.runs) return { text: "-", known: true, detail: "", reason: "" };
	if (typeof m.cost_usd === "number" && Number.isFinite(m.cost_usd)) return { text: usd(m.cost_usd), known: true, detail: "", reason: "" };
	const priced = m.cost_runs ?? 0;
	const unpriced = Math.max(0, m.runs - priced);
	const detail = `${unpriced} of ${m.runs} ${m.runs === 1 ? "run" : "runs"} unpriced`;
	const why = m.cost_note ? `: ${m.cost_note}` : "";
	return { text: "Not known", known: false, detail, reason: `Cost not known, ${detail}${why}` };
}

/**
 * The names verdict text uses for two arms, as the server's summaries do: the label when it is more
 * than an arm letter, else the agent with its version, plus the model when that is all that differs.
 */
export function armNames(a: EvaluationArm, b: EvaluationArm): [string, string] {
	const name = (x: EvaluationArm, withModel: boolean) => {
		if (x.label.length > 1) return x.label;
		let n = x.agent || x.label;
		if (x.version) n += ` v${x.version}`;
		if (withModel && x.model) n += ` on ${x.model}`;
		return n;
	};
	let na = name(a, false);
	let nb = name(b, false);
	if (na === nb) [na, nb] = [name(a, true), name(b, true)];
	if (na === nb) [na, nb] = [`${na} (${a.label})`, `${nb} (${b.label})`];
	return [na, nb];
}

/** The display name of arm label in an A/B report; the label itself when it is not an arm. */
export function armName(report: Report, label: string): string {
	const [a, b] = report.arms;
	if (!a || !b) return label;
	const [na, nb] = armNames(a, b);
	return label === a.label ? na : label === b.label ? nb : label;
}

export function pct(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	return `${Math.round(x * 100)}%`;
}

/** Signed percentage points, "+8 pts" / "-21 pts" / "0 pts". */
export function points(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	const p = Math.round(x * 100);
	return `${p > 0 ? "+" : ""}${p} pts`;
}

/**
 * A fault score's change from a normal (clean) run of the same tasks, the one sign rule every page uses:
 * negative means the fault hurt, positive that the agent did better under it, and a change that rounds
 * to 0 points reads "no change".
 */
export function faultChange(fs: { drop: number } | undefined | null): { text: string; hurt: boolean; helped: boolean } {
	if (!fs || !Number.isFinite(fs.drop)) return { text: "-", hurt: false, helped: false };
	const p = Math.round(fs.drop * 100);
	return { text: p === 0 ? "no change" : points(fs.drop), hurt: p < 0, helped: p > 0 };
}

export function usd(x: number | undefined | null): string {
	return formatUsd(x);
}

export function compactNumber(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	if (Math.abs(x) >= 1_000_000) return `${(x / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
	if (Math.abs(x) >= 1_000) return `${(x / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
	return String(Math.round(x));
}

export function duration(seconds: number | undefined | null): string {
	return formatDuration(seconds);
}

/**
 * Runs-weighted mean score of one arm across benchmarks (falls back to capabilities). Undefined when no row has runs:
 * a row without runs has no score, so it never reads as 0% (fix wave 3, N182 and N215).
 */
export function overallScore(report: Report, label: string): number | undefined {
	const rows: Array<ReportArmMetrics | undefined> = (report.benchmarks.length > 0 ? report.benchmarks : report.capabilities).map(
		(r) => r.arms[label],
	);
	let runs = 0;
	let sum = 0;
	for (const m of rows) {
		if (!m || !Number.isFinite(m.score) || !(m.runs > 0)) continue;
		runs += m.runs;
		sum += m.score * m.runs;
	}
	return runs > 0 ? sum / runs : undefined;
}

/** Counted runs over every benchmark row and arm. */
function countedRuns(report: Report): number {
	return report.benchmarks.reduce((acc, r) => acc + Object.values(r.arms).reduce((n, m) => n + (m?.runs ?? 0), 0), 0);
}

/** "1 run", "24 runs". */
function runsText(n: number): string {
	return `${n.toLocaleString("en-US")} ${n === 1 ? "run" : "runs"}`;
}

/** Names a safety check for a sentence ("the model check (...)"); the card passes the hw08 locale text. */
export type CheckName = (gate: string) => string;

const defaultCheckName: CheckName = (gate) => `the ${gate.replace(/_/g, " ")} check`;

/** The safety checks that threw runs out, by gate id, over every benchmark and arm. */
export function thrownOutChecks(report: Report): string[] {
	const ids = new Set<string>();
	for (const b of report.benchmarks) {
		for (const t of Object.values(b.thrown_out ?? {})) {
			for (const [id, n] of Object.entries(t?.checks ?? {})) if (n > 0) ids.add(id);
		}
	}
	return [...ids].sort();
}

/** The benchmarks with thrown-out runs. */
export function thrownOutBenchmarks(report: Report): ReportBenchmark[] {
	return report.benchmarks.filter((b) => Object.values(b.thrown_out ?? {}).some((t) => (t?.runs ?? 0) > 0));
}

function joinWords(parts: string[]): string {
	if (parts.length <= 1) return parts[0] ?? "";
	return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

export interface ComparisonResult {
	/** Arm label of the winner, or "tie". */
	winner: string;
	/** Second arm minus first arm. */
	delta: number;
	significant: boolean;
}

/** Second arm vs first arm: a winner only when the confidence intervals do not overlap. */
export function compareArms(arms: Record<string, ReportArmMetrics>, a: string, b: string): ComparisonResult | undefined {
	const ma = arms[a];
	const mb = arms[b];
	if (!ma || !mb) return undefined;
	const delta = mb.score - ma.score;
	const significant = mb.ci_low > ma.ci_high || ma.ci_low > mb.ci_high;
	return {
		winner: significant ? (delta > 0 ? b : a) : "tie",
		delta,
		significant,
	};
}

/** Benchmark winner: the server verdict when present, else the CI comparison. */
export function benchmarkWinner(row: ReportBenchmark, a: string, b: string): ComparisonResult | undefined {
	if (row.verdict)
		return {
			// Only an arm label names a winner; "unclear" or any other word is no clear winner.
			winner: row.verdict.winner === a || row.verdict.winner === b ? row.verdict.winner : "tie",
			delta: row.verdict.delta,
			significant: row.verdict.significant,
		};
	return compareArms(row.arms, a, b);
}

export interface Headline {
	title: string;
	detail: string;
}

/**
 * The card's headline (fix wave 3, N182 and N215): a running evaluation says how many runs are done; when a safety check threw
 * out every run there is no score; when it threw out some, the detail says so. Otherwise the score or the A/B winner.
 */
/** What the page knows besides the report: the budget that stopped the evaluation (M3-4). */
export interface HeadlineContext {
	budgetStop?: { budget: string; done: number; planned: number };
}

/** Clean and fault run counts of a one-arm report with fault rows (M3-3); undefined when it ran no fault. */
export function faultSplit(
	report: Report,
	label: string,
): { cleanSolved: number; cleanRuns: number; fault: string; faultSolved: number; faultRuns: number; delta: number } | undefined {
	const rows = report.reliability.filter((r) => r.fault !== "clean" && (r.arms[label]?.runs ?? 0) > 0);
	if (rows.length === 0) return undefined;
	let cleanRuns = 0;
	let cleanSolved = 0;
	for (const b of report.benchmarks) {
		const m = b.arms[label];
		if (!m || !(m.runs > 0)) continue;
		cleanRuns += m.runs;
		cleanSolved += Math.round(m.score * m.runs);
	}
	const row = rows[0];
	const fm = row.arms[label];
	const faultRuns = rows.reduce((n, r) => n + (r.arms[label]?.runs ?? 0), 0);
	const faultSolved = rows.reduce((n, r) => n + Math.round((r.arms[label]?.score ?? 0) * (r.arms[label]?.runs ?? 0)), 0);
	return { cleanSolved, cleanRuns, fault: faultName(row.fault).toLowerCase(), faultSolved, faultRuns, delta: fm.drop };
}

export function headline(report: Report, checkName: CheckName = defaultCheckName, ctx: HeadlineContext = {}): Headline {
	const total = report.benchmarks.length;
	const runs = countedRuns(report);
	const benchWord = total === 1 ? "benchmark" : "benchmarks";
	const thrown = report.thrown_out_runs ?? 0;
	if (report.status === "queued" || report.status === "running") {
		const planned = report.planned_runs ?? 0;
		const done = report.done_runs ?? runs;
		return {
			title: planned > 0 ? `Still running: ${done} of ${planned} runs done` : `Still running: ${runsText(done)} done`,
			detail: `${total} ${benchWord}. The score is final when every run has finished.`,
		};
	}
	if (thrown > 0 && runs === 0) {
		const names = thrownOutBenchmarks(report).map((b) => b.benchmark);
		const checks = thrownOutChecks(report).map(checkName);
		return {
			title: "No score: every run was thrown out by a safety check",
			detail: `${runsText(thrown)} on ${joinWords(names)} failed ${checks.length ? joinWords(checks) : "a safety check"}. This is a problem with the test setup, not with your agent. Open the comparison to see the check.`,
		};
	}
	const thrownNote =
		thrown > 0
			? ` ${runsText(thrown)} ${thrown === 1 ? "was" : "were"} thrown out by a safety check and ${thrown === 1 ? "is" : "are"} not in the score.`
			: "";
	const withNote = (h: Headline): Headline => ({ ...h, detail: h.detail + thrownNote });
	if (ctx.budgetStop) {
		const { budget, done, planned } = ctx.budgetStop;
		return withNote({ title: fillText(HEADLINE.budget, { budget, done, planned }), detail: fillText(HEADLINE.budgetDetail, { done }) });
	}
	if (!isAB(report)) {
		const arm = report.arms[0];
		const score = arm ? overallScore(report, arm.label) : undefined;
		const split = arm ? faultSplit(report, arm.label) : undefined;
		if (split && score !== undefined) {
			return withNote({
				title: fillText(HEADLINE.fault, { ...split, delta: points(split.delta) }),
				detail: fillText(HEADLINE.faultRuns, split),
			});
		}
		if (score !== undefined && runs < MIN_RUNS_TO_RATE) {
			const solved = Math.round(score * runs);
			const r = wilson(solved, runs);
			return withNote({
				title: fillText(HEADLINE.small, { solved, runs }),
				detail: fillText(HEADLINE.smallDetail, { low: pct(r.low), high: pct(r.high) }),
			});
		}
		return withNote({
			title:
				score === undefined
					? "No results yet"
					: report.mode === "score"
						? fillText(HEADLINE.score, { pct: pct(score) })
						: fillText(HEADLINE.solves, { pct: pct(score) }),
			detail: `${arm ? armDescription(arm) + ", " : ""}${total} ${benchWord}, ${runs.toLocaleString("en-US")} runs${thrownNote ? "." : ""}`,
		});
	}
	const [a, b] = report.arms;
	const wins: Record<string, number> = { [a.label]: 0, [b.label]: 0, tie: 0 };
	for (const row of report.benchmarks) {
		const w = benchmarkWinner(row, a.label, b.label);
		const key = w && w.winner in wins ? w.winner : "tie";
		wins[key]++;
	}
	const counted = report.overall?.wins ?? wins;
	let winner = report.overall
		? report.overall.winner
		: wins[b.label] > wins[a.label]
			? b.label
			: wins[a.label] > wins[b.label]
				? a.label
				: "tie";
	// A server winner that is not one of the two arms ("unclear", empty or unknown) reads as a tie.
	if (winner !== a.label && winner !== b.label) winner = "tie";
	const loser = winner === a.label ? b.label : a.label;
	const name = armName(report, winner);
	const title =
		winner === "tie"
			? `No clear winner across ${total} ${benchWord}`
			: `${name} is better on ${counted[winner] ?? 0} of ${total} ${benchWord}`;
	const verdict =
		report.overall?.summary ||
		(winner === "tie"
			? "Neither agent is clearly better overall."
			: `Overall ${name} wins; worse on ${counted[loser] ?? 0}, tied on ${counted.tie ?? 0}.`);
	return withNote({ title, detail: verdict });
}

export interface CapabilityRow {
	capability: string;
	data?: ReportCapability;
	/** True when some arm has runs on it in this evaluation. */
	tested: boolean;
}

/**
 * Capability rows: the tested ones first, then the rest (shown under "Not tested in this evaluation"), each group in the fixed
 * order with any extra capability after. A report with no runs at all has no rows: its headline says why (N185).
 */
export function capabilityRows(report: Report): CapabilityRow[] {
	const byId = new Map(report.capabilities.map((c) => [c.capability, c]));
	const ids: string[] = [...CAPABILITY_ORDER];
	for (const c of report.capabilities) {
		if (!ids.includes(c.capability)) ids.push(c.capability);
	}
	const rows: CapabilityRow[] = ids.map((id) => {
		const data = byId.get(id);
		return { capability: id, data, tested: !!data && Object.values(data.arms).some((m) => (m?.runs ?? 0) > 0) };
	});
	if (!rows.some((r) => r.tested)) return [];
	return [...rows.filter((r) => r.tested), ...rows.filter((r) => !r.tested)];
}

export function reportFileName(report: Report): string {
	return `report-card-${(report.evaluation || "evaluation").replace(/[^a-zA-Z0-9_-]/g, "_")}.json`;
}

/** How long the JSON download keeps its object URL: revoking in the same tick can cancel the download in some browsers. */
export const DOWNLOAD_REVOKE_MS = 1000;

/** Saves the report as JSON from memory (no server call); the object URL is revoked after DOWNLOAD_REVOKE_MS. */
export function downloadReportJSON(report: Report): void {
	const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
	const url = URL.createObjectURL(blob);
	const a = document.createElement("a");
	a.href = url;
	a.download = reportFileName(report);
	document.body.appendChild(a);
	a.click();
	a.remove();
	setTimeout(() => URL.revokeObjectURL(url), DOWNLOAD_REVOKE_MS);
}
/** Plain words for why runs were left out of a score. */
const EXCLUDED_NAMES: Record<string, string> = {
	out_of_memory: "out of memory",
	infra_error: "infrastructure error",
	invalid_evaluator: "grader could not decide",
	unknown: "unknown result",
};

/** "excluded: 2 out of memory, 1 infrastructure error", or "" when nothing was left out. Out of memory comes first. */
export function excludedText(excluded: Record<string, number> | undefined): string {
	if (!excluded) return "";
	const keys = Object.keys(excluded)
		.filter((k) => excluded[k] > 0)
		.sort((x, y) => (x === "out_of_memory" ? -1 : y === "out_of_memory" ? 1 : x.localeCompare(y)));
	if (keys.length === 0) return "";
	return "excluded: " + keys.map((k) => `${excluded[k]} ${EXCLUDED_NAMES[k] ?? k.replace(/_/g, " ")}`).join(", ");
}
/** Every reliability card in the report, by benchmark then arm; unknown cards are left out. */
export function reliabilityCards(report: Report): { benchmark: string; arm: string; card: ReportReliabilityCard }[] {
	const out: { benchmark: string; arm: string; card: ReportReliabilityCard }[] = [];
	for (const b of report.benchmarks) {
		for (const arm of report.arms) {
			const card = b.reliability_card?.[arm.label];
			if (card) out.push({ benchmark: b.benchmark, arm: arm.label, card });
		}
	}
	return out;
}

/** "x0.82", or "-" when unknown. */
export function ratio(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	return `x${x.toFixed(2)}`;
}
/** True when any arm on the row has process scores from at least one run. */
export function hasProcess(arms: Record<string, { process?: ReportProcess } | undefined>): boolean {
	return Object.values(arms).some((m) => (m?.process?.runs ?? 0) > 0);
}

/** "Path match 75% · Steps 1.4x ref · Repeats 0"; parts with no value are left out. */
export function formatProcess(p: ReportProcess | undefined): string {
	if (!p || p.runs <= 0) return "-";
	const parts: string[] = [];
	if (p.tool_match !== null && Number.isFinite(p.tool_match)) parts.push(`Path match ${pct(p.tool_match)}`);
	if (p.step_ratio !== null && Number.isFinite(p.step_ratio)) parts.push(`Steps ${p.step_ratio.toFixed(1)}x ref`);
	if (p.redundant_calls !== null && Number.isFinite(p.redundant_calls)) parts.push(`Repeats ${Math.round(p.redundant_calls * 10) / 10}`);
	return parts.length ? parts.join(" \u00b7 ") : "-";
}

export const PROCESS_NOTE =
	"Process scores compare the agent's tool calls with the task's reference path. They are report only and never change the score.";

/** How a judge-graded benchmark's badge reads: tone and words (G4-06). */
export interface JudgeBadge {
	tone: "success" | "warning" | "destructive" | "neutral";
	text: string;
	title: string;
}

/**
 * The judge badge of a benchmark row, undefined when a check (not a model judge) grades it.
 * Human-calibrated is good; calibrated on controls and few human labels are usable with the label;
 * anything else is not trusted, so A/B verdicts on it are capped and min_score gates are not applied.
 */
export function judgeBadge(row: ReportBenchmark): JudgeBadge | undefined {
	const j = row.judge;
	if (!j) return undefined;
	const counts =
		j.human_labels > 0 || j.control_items > 0 ? ` ${j.human_labels} human labels, ${j.control_items} admission-control answers.` : "";
	const title = `Graded by a model judge${j.model ? ` (${j.model})` : ""}: ${j.label}.${counts}${j.trusted ? "" : " Verdicts on it are not decisions until the judge is calibrated."}`;
	if (j.state === "calibrated") return { tone: "success", text: "Judge: human-calibrated", title };
	if (j.trusted) return { tone: "warning", text: `Judge: ${j.label}`, title };
	if (j.state === "unreliable") return { tone: "destructive", text: "Judge: unreliable", title };
	return { tone: "neutral", text: j.state === "unchecked" ? "Judge: not checked" : "Judge: not calibrated", title };
}

/** The paired change line of an A/B verdict: interval and smallest detectable change. */
export function pairedChangeText(v: ReportBenchmark["verdict"]): string | undefined {
	if (!v) return undefined;
	const parts: string[] = [];
	if (v.boot_low != null && v.boot_high != null) parts.push(`95% interval ${points(v.boot_low)} to ${points(v.boot_high)}`);
	if (v.mde != null && v.tasks) parts.push(`${v.tasks} paired tasks can detect about ${Math.round(v.mde * 100)} points`);
	return parts.length ? parts.join("; ") : undefined;
}