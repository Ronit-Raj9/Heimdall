import type { EvaluationArm, Report, ReportArmMetrics, ReportBenchmark, ReportCapability } from "@/lib/store/apis/evaluationsApi";

/** Capability rows, always shown in this order. Others found in the report follow. */
export const CAPABILITY_ORDER = ["terminal", "coding", "memory", "support", "research", "voice", "safety"] as const;

const CAPABILITY_NAMES: Record<string, string> = {
	terminal: "Terminal",
	coding: "Coding",
	memory: "Memory",
	support: "Customer support",
	research: "Research",
	voice: "Voice",
	safety: "Safety",
	team: "Teamwork",
	gpu: "GPU",
};

const FAULT_NAMES: Record<string, string> = {
	clean: "No fault",
	"model-down": "Model goes down",
	"broken-tool": "Broken tool",
	"misleading-instruction": "Misleading instruction",
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
		capabilities: report.capabilities.map((c) => ({ ...c, arms: onlyLabel(c.arms, label) })),
		benchmarks: report.benchmarks.map(({ verdict: _verdict, ...b }) => ({ ...b, arms: onlyLabel(b.arms, label) })),
		reliability: report.reliability.map((r) => ({ ...r, arms: onlyLabel(r.arms, label) })),
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
	};
}

/** "12% (5-25%)" for a safety rate; "Not tested" when the arm has none. */
export function alignmentText(rate: { rate: number; ci_low: number; ci_high: number } | undefined): string {
	if (!rate) return "Not tested";
	return `${pct(rate.rate)} (${Math.round(rate.ci_low * 100)}-${pct(rate.ci_high)})`;
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

export function usd(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	return x < 10 ? `$${x.toFixed(2)}` : `$${Math.round(x).toLocaleString("en-US")}`;
}

export function compactNumber(x: number | undefined | null): string {
	if (x === undefined || x === null || !Number.isFinite(x)) return "-";
	if (Math.abs(x) >= 1_000_000) return `${(x / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
	if (Math.abs(x) >= 1_000) return `${(x / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
	return String(Math.round(x));
}

export function duration(seconds: number | undefined | null): string {
	if (seconds === undefined || seconds === null || !Number.isFinite(seconds)) return "-";
	const s = Math.round(seconds);
	if (s < 60) return `${s}s`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ${s % 60}s`;
	return `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** Runs-weighted mean score of one arm across benchmarks (falls back to capabilities). */
export function overallScore(report: Report, label: string): number | undefined {
	const rows: Array<ReportArmMetrics | undefined> = (report.benchmarks.length > 0 ? report.benchmarks : report.capabilities).map(
		(r) => r.arms[label],
	);
	let runs = 0;
	let sum = 0;
	let count = 0;
	let plain = 0;
	for (const m of rows) {
		if (!m || !Number.isFinite(m.score)) continue;
		count++;
		plain += m.score;
		if (m.runs > 0) {
			runs += m.runs;
			sum += m.score * m.runs;
		}
	}
	if (count === 0) return undefined;
	return runs > 0 ? sum / runs : plain / count;
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
	return { winner: significant ? (delta > 0 ? b : a) : "tie", delta, significant };
}

/** Benchmark winner: the server verdict when present, else the CI comparison. */
export function benchmarkWinner(row: ReportBenchmark, a: string, b: string): ComparisonResult | undefined {
	if (row.verdict) return { winner: row.verdict.winner || "tie", delta: row.verdict.delta, significant: row.verdict.significant };
	return compareArms(row.arms, a, b);
}

export interface Headline {
	title: string;
	detail: string;
}

export function headline(report: Report): Headline {
	const total = report.benchmarks.length;
	const runs = report.benchmarks.reduce((acc, r) => acc + Object.values(r.arms).reduce((n, m) => n + (m?.runs ?? 0), 0), 0);
	const benchWord = total === 1 ? "benchmark" : "benchmarks";
	if (!isAB(report)) {
		const arm = report.arms[0];
		const score = arm ? overallScore(report, arm.label) : undefined;
		return {
			title:
				score === undefined
					? "No results yet"
					: report.mode === "score"
						? `Your agent scores ${pct(score)} overall`
						: `Your agent solves ${pct(score)} overall`,
			detail: `${arm ? armDescription(arm) + ", " : ""}${total} ${benchWord}, ${runs.toLocaleString("en-US")} runs`,
		};
	}
	const [a, b] = report.arms;
	const wins: Record<string, number> = { [a.label]: 0, [b.label]: 0, tie: 0 };
	for (const row of report.benchmarks) {
		const w = benchmarkWinner(row, a.label, b.label);
		const key = w && w.winner in wins ? w.winner : "tie";
		wins[key]++;
	}
	const counted = report.overall?.wins ?? wins;
	let winner = report.overall?.winner;
	if (!winner) winner = wins[b.label] > wins[a.label] ? b.label : wins[a.label] > wins[b.label] ? a.label : "tie";
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
	return { title, detail: verdict };
}

export interface CapabilityRow {
	capability: string;
	data?: ReportCapability;
}

/** Fixed capability rows first, then any extra capability the report carries. */
export function capabilityRows(report: Report): CapabilityRow[] {
	const byId = new Map(report.capabilities.map((c) => [c.capability, c]));
	const rows: CapabilityRow[] = CAPABILITY_ORDER.map((id) => ({ capability: id, data: byId.get(id) }));
	for (const c of report.capabilities) {
		if (!(CAPABILITY_ORDER as readonly string[]).includes(c.capability)) rows.push({ capability: c.capability, data: c });
	}
	return rows;
}

export function reportFileName(report: Report): string {
	return `report-card-${(report.evaluation || "evaluation").replace(/[^a-zA-Z0-9_-]/g, "_")}.json`;
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
