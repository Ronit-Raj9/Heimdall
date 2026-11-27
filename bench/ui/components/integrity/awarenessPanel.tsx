import { Badge } from "@/components/ui/badge";
import { CopyId } from "@/components/ui/copyId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TONE_CLASS } from "@/components/verdict/verdict.utils";
import type { AwarenessReport } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import "./gw06I18n";
import { splitOnPhrase } from "./integrity.utils";

export interface AwarenessPanelProps {
	report: AwarenessReport;
	/** Arm order, A then B; arms the report has beyond these follow in name order. */
	arms: string[];
	className?: string;
}

/**
 * The eval-awareness scan of an experiment (Eval integrity area, S2): per arm, runs whose agent
 * said it might be under test, and the sentences that matched. A heuristic, labelled as one: it
 * counts words, not intent, and never gates a run.
 */
export function AwarenessPanel({ report, arms, className }: AwarenessPanelProps) {
	const { t } = useTranslation("bench");
	const order = [
		...arms.filter((a) => report.per_arm[a]),
		...Object.keys(report.per_arm)
			.filter((a) => !arms.includes(a))
			.sort(),
	];
	const showSignals = signalsColumnNeeded(order.map((a) => report.per_arm[a]));

	return (
		<div className={cn("flex flex-col gap-4", className)} data-testid="awareness-panel">
			<div className="flex flex-wrap items-center gap-2 text-xs">
				<Badge variant="outline" className={TONE_CLASS.warn} data-testid="awareness-heuristic" title={report.label}>
					{t("integrity.awareness.heuristic")}
				</Badge>
				<Badge variant="outline" className={TONE_CLASS.neutral}>
					{t("integrity.awareness.reportOnly")}
				</Badge>
				<Badge variant="outline" className={TONE_CLASS.neutral} data-testid="awareness-realism">
					{t("integrity.awareness.realism", { realism: report.realism || "standard" })}
				</Badge>
			</div>
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("integrity.awareness.arm")}</TableHead>
						<TableHead className="text-right">{t("integrity.awareness.runs")}</TableHead>
						<TableHead className="text-right">{t("integrity.awareness.runsWithSignals")}</TableHead>
						{showSignals && <TableHead className="text-right">{t("integrity.awareness.signals")}</TableHead>}
					</TableRow>
				</TableHeader>
				<TableBody>
					{order.map((arm) => {
						const row = report.per_arm[arm];
						return (
							<TableRow key={arm} data-testid={`awareness-arm-${arm}`}>
								<TableCell className="font-mono text-sm">{arm}</TableCell>
								<TableCell className="text-right tabular-nums">
									{row.runs_scanned}
									<span className="text-muted-foreground"> / {row.runs}</span>
								</TableCell>
								<TableCell className={cn("text-right tabular-nums", row.runs_with_signals > 0 && "text-chart-warning-ink font-semibold")}>
									{row.runs_with_signals}
								</TableCell>
								{showSignals && <TableCell className="text-right tabular-nums">{row.signals}</TableCell>}
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
			<NotScannedList runs={report.not_scanned ?? []} />
			<section className="flex flex-col gap-2">
				<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("integrity.awareness.examples")}</h3>
				{report.examples.length === 0 ? (
					<p className="text-muted-foreground max-w-prose text-sm" data-testid="awareness-no-examples">
						{t("integrity.awareness.noExamples")}
					</p>
				) : (
					<ul className="flex flex-col gap-2" data-testid="awareness-examples">
						{report.examples.map((ex, i) => (
							<Example key={`${ex.run_id}-${i}`} example={ex} />
						))}
					</ul>
				)}
			</section>
		</div>
	);
}

/**
 * The "Signals" column only when it says more than "Runs with signals" (M4-43): on any arm with
 * more matched sentences than flagged runs. Pure.
 */
export function signalsColumnNeeded(rows: readonly { runs_with_signals: number; signals: number }[]): boolean {
	return rows.some((r) => r.signals !== r.runs_with_signals);
}

/**
 * The runs the awareness scan could not read (N62, N68): a disclosure with the count, then each run
 * as a link with the reason the server gave (no gateway log, no agent text, ...).
 */
export function NotScannedList({ runs, className }: { runs: readonly { run_id: string; reason: string }[]; className?: string }) {
	const { t } = useTranslation("gw06");
	if (runs.length === 0) return null;
	return (
		<details className={cn("text-xs", className)} data-testid="awareness-not-scanned">
			<summary className="text-muted-foreground hover:text-foreground cursor-pointer select-none">
				{t("integrity.notScanned", { count: runs.length })}
			</summary>
			<ul className="mt-2 flex flex-col gap-1" data-testid="awareness-not-scanned-list">
				{runs.map((r) => (
					<li key={r.run_id} className="flex flex-wrap items-baseline gap-x-2">
						<Link
							to="/runs/$runId"
							params={{ runId: r.run_id }}
							className="text-primary font-mono hover:underline"
							title={t("integrity.notScannedRun", { runId: r.run_id })}
						>
							{r.run_id}
						</Link>
						<span className="text-muted-foreground">{r.reason}</span>
					</li>
				))}
			</ul>
		</details>
	);
}

function Example({ example }: { example: AwarenessReport["examples"][number] }) {
	const { t } = useTranslation("bench");
	const { before, match, after } = splitOnPhrase(example.excerpt, example.phrase);
	const sourceKey = ["assistant_message", "reasoning_summary", "final_message", "agent_stream"].includes(example.source)
		? (example.source as "assistant_message" | "reasoning_summary" | "final_message" | "agent_stream")
		: null;
	return (
		<li className="border-chart-warning/50 bg-muted/30 flex flex-col gap-1 border-l-2 py-1.5 pr-2 pl-3" data-testid="awareness-example">
			<blockquote className="text-foreground text-sm leading-relaxed">
				{before}
				{match && <mark className="bg-chart-warning/25 text-foreground rounded-[2px] px-0.5">{match}</mark>}
				{after}
			</blockquote>
			<div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px]">
				<span>{example.arm}</span>
				<span>{example.task}</span>
				<span>{example.condition}</span>
				<span>{sourceKey ? t(`integrity.awareness.source.${sourceKey}`) : example.source}</span>
				<CopyId value={example.run_id} className="max-w-40" testId="awareness-example-run" />
			</div>
		</li>
	);
}