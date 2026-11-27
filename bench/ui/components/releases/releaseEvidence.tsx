import "@/components/reportcard/fw09I18n";
import { Badge } from "@/components/ui/badge";
import i18n from "@/lib/i18n";
import type { DatasetComparison, ReleaseRecord, ReleaseSide, ReleaseSummary, ScoreCI } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { rateText } from "@/components/reportcard/pageNumbers";
import { faultSplit } from "@/components/reportcard/reportCard.utils";
import "@/components/reportcard/ufrbI18n";
import "@/components/canaries/uf7bI18n";
import "@/app/evaluations_/$evalId/views/w4m2I18n";

/** "codex v4", or the agent alone when the version is unknown. */
export function versionName(agent: string, version?: number): string {
	return version ? `${agent} v${version}` : agent;
}

/**
 * The headline of a release: "codex v4 replaces v3", "codex v4 is the first release", or the old fallback.
 * compared_version is the version the evidence was compared with (a release preview's baseline): when it
 * is the same version, the title says so instead of "codex v1 replaces v1".
 */
export function releaseTitle(
	r: Pick<ReleaseRecord, "agent_id" | "version" | "replaces_version" | "agent_config_id" | "arm"> & {
		compared_version?: number;
		/** Tasks both evaluations ran, when the numbers cover only those (sharedTaskCount of the summary's dataset). */
		shared_tasks?: number;
	},
	t: (k: string, o?: Record<string, unknown>) => string,
): string {
	if (!r.agent_id) return r.agent_config_id || r.arm;
	const name = versionName(r.agent_id, r.version);
	const same = !!r.version && (r.replaces_version === r.version || r.compared_version === r.version);
	if (same && r.shared_tasks) return i18n.t("sharedTasks", { ns: "w4m2", name, count: r.shared_tasks });
	if (same) return i18n.t("release.sameVersion", { ns: "fw09", name });
	return r.replaces_version ? t("releases.replaces", { name, prev: `v${r.replaces_version}` }) : t("releases.first", { name });
}

/** Tasks both evaluations ran with the same content, summed over the benchmarks; 0 when unknown. */
export function sharedTaskCount(dataset: DatasetComparison | undefined): number {
	return (dataset?.benchmarks ?? []).reduce((n, b) => n + (b.compared ?? 0), 0);
}

const pct = (x: number) => `${Math.round(x * 100)}%`;
const usd = (x: number) => (x >= 1 ? `$${x.toFixed(2)}` : `$${x.toFixed(3)}`);

/**
 * The evaluation report the page already shows, so the release numbers read the same counts as the report card
 * (Q-14): the faults score from its fault rows, not a second computation. Absent on the releases page.
 */
export const ReleaseReportContext = React.createContext<Report | undefined>(undefined);

/** "41% (30%-52%)", or under 10 runs "2 of 3, too few runs to rate": the report card's rateText, one range rule. */
export function scoreText(s: ScoreCI | undefined): string {
	if (!s || s.runs === 0) return none();
	return rateText(s);
}

/** A number the server did not send: the word "none", never a bare dash. */
function none(): string {
	return i18n.t("evidence.none", { ns: "ufrb" });
}

/** The candidate's score under faults from the page's report (the headline's "2 of 3"), or undefined. Exported for tests. */
export function reportFaultText(report: Report | undefined, side: { label: string; evaluation_id?: string }): string | undefined {
	if (!report || (side.evaluation_id && report.evaluation && side.evaluation_id !== report.evaluation)) return undefined;
	const split = faultSplit(report, side.label);
	if (!split || split.faultRuns <= 0) return undefined;
	return rateText({ score: split.faultSolved / split.faultRuns, runs: split.faultRuns }, { range: false });
}

type Row = { key: string; label: string; before?: string; after: string; better?: boolean | null };

/** Which way a change went: true better, false worse, null no change or unknown. */
function direction(before: number | undefined, after: number | undefined, higherIsBetter: boolean): boolean | null {
	if (before === undefined || after === undefined || Math.abs(after - before) < 1e-9) return null;
	return higherIsBetter ? after > before : after < before;
}

function rows(s: ReleaseSummary, t: (k: string) => string, report?: Report): Row[] {
	const b: ReleaseSide | undefined = s.baseline;
	const c = s.candidate;
	const out: Row[] = [];
	for (const cap of s.capabilities ?? []) {
		out.push({
			key: `cap-${cap.capability}`,
			label: cap.capability.charAt(0).toUpperCase() + cap.capability.slice(1).replace(/[_-]/g, " "),
			before: cap.baseline ? scoreText(cap.baseline) : undefined,
			after: scoreText(cap.candidate),
			better: direction(cap.baseline?.score, cap.candidate.score, true),
		});
	}
	out.push({
		key: "score",
		label: t("releases.ev.overall"),
		before: b ? scoreText(b) : undefined,
		after: scoreText(c),
		better: direction(b?.score, c.score, true),
	});
	const opt = (v: number | undefined, f: (x: number) => string) => (v === undefined ? none() : f(v));
	out.push({
		key: "cost",
		label: t("releases.ev.costPerSolved"),
		before: b ? opt(b.cost_per_solved_usd, usd) : undefined,
		after: opt(c.cost_per_solved_usd, usd),
		better: direction(b?.cost_per_solved_usd, c.cost_per_solved_usd, false),
	});
	out.push({
		key: "false",
		label: t("releases.ev.falseCompletion"),
		before: b ? opt(b.false_completion, pct) : undefined,
		after: opt(c.false_completion, pct),
		better: direction(b?.false_completion, c.false_completion, false),
	});
	out.push({
		key: "faults",
		label: t("releases.ev.faultSurvival"),
		before: b ? (reportFaultText(report, b) ?? opt(b.fault_survival, pct)) : undefined,
		after: reportFaultText(report, c) ?? opt(c.fault_survival, pct),
		better: direction(b?.fault_survival, c.fault_survival, true),
	});
	return out;
}

/**
 * The two column heads of the decision table (M7-78): the version names, or, when both sides are the
 * same version, "This evaluation" and "Earlier run", each with its run count. Exported for tests.
 */
export function evidenceHeads(summary: ReleaseSummary, t: (k: string, o?: Record<string, unknown>) => string): { before?: string; after: string } {
	const b = summary.baseline;
	const c = summary.candidate;
	const after = versionName(c.agent, c.version);
	if (!b) return { after };
	const before = versionName(b.agent, b.version);
	if (before !== after) return { before, after };
	return {
		before: t("evidence.earlier", { count: b.runs ?? 0 }),
		after: t("evidence.this", { count: c.runs ?? 0 }),
	};
}

/** Before and after numbers of a release: score with its interval per capability, cost per solved task, false completion, fault survival. */
export function ReleaseEvidence({ summary, testId }: { summary: ReleaseSummary; testId?: string }) {
	const { t } = useTranslation("u14");
	const { t: m } = useTranslation("uf7b");
	const b = summary.baseline;
	const heads = evidenceHeads(summary, m as never);
	const report = React.useContext(ReleaseReportContext);
	return (
		<div className="flex flex-col gap-2" data-testid={testId}>
			<table className="w-full text-xs tabular-nums">
				<thead className="text-muted-foreground text-left">
					<tr>
						<th className="py-1 pr-3 font-medium" />
						{b && <th className="py-1 pr-3 font-medium">{heads.before}</th>}
						<th className="py-1 font-medium">{heads.after}</th>
					</tr>
				</thead>
				<tbody>
					{rows(summary, t as never, report).map((r) => (
						<tr key={r.key} className="border-t" data-testid={testId ? `${testId}-${r.key}` : undefined}>
							<td className="text-muted-foreground py-1.5 pr-3">{r.label}</td>
							{b && <td className="py-1.5 pr-3">{r.before ?? none()}</td>}
							<td
								className={cn(
									"py-1.5 font-medium",
									r.better === true && "text-chart-success-ink",
									r.better === false && "text-chart-error-ink",
								)}
							>
								{r.after}
							</td>
						</tr>
					))}
				</tbody>
			</table>
			{!b && <p className="text-muted-foreground text-xs">{t("releases.ev.noBaseline")}</p>}
			{summary.dataset && <DatasetChange dataset={summary.dataset} testId={testId ? `${testId}-dataset` : undefined} />}
			{summary.note && <p className="text-muted-foreground text-xs">{summary.note}</p>}
		</div>
	);
}

/**
 * Whether the benchmark changed since the baseline: a badge, and per changed benchmark how many
 * tasks both sides ran (the only ones compared), new and dropped. Nothing when it did not change.
 */
export function DatasetChange({ dataset, testId = "dataset-change" }: { dataset: DatasetComparison; testId?: string }) {
	const { t } = useTranslation("u14");
	if (!dataset.changed) return null;
	return (
		<div className="flex flex-col gap-1 text-xs" data-testid={testId}>
			<div>
				<Badge variant="warning">{t("dataset.changed")}</Badge>
			</div>
			<ul className="text-muted-foreground flex flex-col gap-0.5">
				{dataset.benchmarks
					.filter((b) => b.changed)
					.map((b) => (
						<li key={b.benchmark} data-testid={`${testId}-${b.benchmark}`}>
							<span className="text-foreground font-mono">{b.benchmark}</span>{" "}
							{b.not_compared
								? t("dataset.notCompared")
								: t("dataset.compared", { count: b.compared, added: b.new_tasks, dropped: b.dropped_tasks })}
						</li>
					))}
			</ul>
		</div>
	);
}