import type { EvaluationArm, Report, ReportArmMetrics, ReportBenchmark, ReportCapability, ReportProcess } from "@/lib/store/apis/evaluationsApi";

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
		overall: undefined,
	};
}

/** "codex v2 on gpt-6.2-sol". */
export function armDescription(arm: EvaluationArm): string {
	const version = arm.version ? ` v${arm.version}` : "";
	return `${arm.agent}${version} on ${arm.model}`;
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
			title: score === undefined ? "No results yet" : `Your agent solves ${pct(score)} overall`,
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
	const title =
		winner === "tie"
			? `No clear winner across ${total} ${benchWord}`
			: `${winner} is better on ${counted[winner] ?? 0} of ${total} ${benchWord}`;
	const verdict =
		report.overall?.summary ||
		(winner === "tie"
			? "Neither arm is clearly better overall."
			: `Overall ${winner} wins; worse on ${counted[loser] ?? 0}, tied on ${counted.tie ?? 0}.`);
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
