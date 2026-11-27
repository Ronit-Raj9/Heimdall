import { printExport } from "@/components/share/shareDialog";
import { Badge } from "@/components/ui/badge";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { EvaluationArm, Report, ReportAlignmentRate, ReportArmMetrics, ReportBenchmark } from "@/lib/store/apis/evaluationsApi";
import { reportExportUrl, reproduceUrl } from "@/lib/store/apis/shareApi";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight, Check, ChevronRight, Download, Equal, FileCode2, FileText, Link2, Package, Printer } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";
import { BifrostTable, hasBifrost } from "./bifrostSection";
import { CheatRateRow } from "./cheatRateRow";
import "./fw09I18n";
import "./hw08I18n";
import "./reportCardI18n";
import { hasLearning, hasPersistence, LearningTable, PersistenceTable } from "./learningSections";
import { hasVoice, VoiceTable } from "./voiceSection";
import { hasVoiceCalls, VoiceCallsTable } from "./voiceCallsSection";
import {
	alignmentText,
	armDescription,
	armName,
	benchmarkWinner,
	capabilityName,
	capabilityRows,
	compactNumber,
	compareArms,
	type ComparisonResult,
	costDisplay,
	duration,
	excludedText,
	faultName,
	formatProcess,
	hasProcess,
	PROCESS_NOTE,
	headline,
	isAB,
	judgeBadge,
	pairedChangeText,
	visibleReport,
	pct,
	points,
	scoreRange,
	type HeadlineContext,
	reliabilityCards,
	downloadReportJSON,
	faultChange,
	thrownOutBenchmarks,
} from "./reportCard.utils";
import { ReliabilityCards, ResilienceCard, StoreDroppedFaults } from "./reliabilityCards";
import { ReportFilterBar, ReportSlices } from "./reportSlices";
import { FailoverSection } from "@/components/evaluations/failoverSettings";
import { hasRedTeam, RedTeamSection } from "./redTeamSection";

function costText(m: ReportArmMetrics): string {
	const c = costDisplay(m);
	return c.known ? c.text : `${c.text} (${c.detail})`;
}

export interface ReportCardProps {
	report: Report;
	className?: string;
	/** Replaces the default JSON download and copy link buttons (the Share menu). */
	actions?: React.ReactNode;
	/** A shared, read-only view: no links into the organization's pages. */
	readOnly?: boolean;
	/** Narrows the report to tasks with this metadata (undefined clears it); enables segment filters. */
	onWhere?: (where: Record<string, string> | undefined) => void;
	/** What the page knows besides the report, such as a budget stop (M3-4). */
	headlineContext?: HeadlineContext;
	/** Rendered right under the headline: the raise budget panel and the links to example runs. */
	belowHeadline?: React.ReactNode;
}

/** "$0.0090 for 6 runs" from the per arm totals of a one-arm report (M7-89); undefined when the cost is not known. */
export function costLine(report: Report): string | undefined {
	if (isAB(report)) return undefined;
	const arm = report.arms[0];
	const total = arm ? report.totals?.[arm.label] : undefined;
	if (!total || !(total.runs > 0) || typeof total.cost_usd !== "number") return undefined;
	return `${costDisplay({ runs: total.runs, cost_usd: total.cost_usd }).text} for ${total.runs} ${total.runs === 1 ? "run" : "runs"}`;
}

/** A gate id in plain words for a sentence, from the hw08 locale (fix wave 3, N215). */
function useCheckName(): (gate: string) => string {
	const { t } = useTranslation("hw08");
	return (gate) =>
		gate === "model_fidelity" || gate === "egress" ? t(`gates.${gate}`) : t("gates.default", { gate: gate.replace(/_/g, " ") });
}

/** The report card of one evaluation: headline answer, capabilities, benchmarks, reliability, safety. */
export function ReportCard({ report: raw, className, actions, readOnly = false, onWhere, headlineContext, belowHeadline }: ReportCardProps) {
	const report = visibleReport(raw);
	const ab = isAB(report);
	const checkName = useCheckName();
	const { t: t08 } = useTranslation("hw08");
	const { t: tr } = useTranslation("reportCard");
	const head = headline(report, checkName, headlineContext);
	const cost = costLine(report);
	const brokenChild = (report.thrown_out_runs ?? 0) > 0 ? thrownOutBenchmarks(report)[0] : undefined;
	return (
		<article data-testid="report-card" aria-labelledby="report-card-title" className={cn("flex min-w-0 flex-col gap-8", className)}>
			<header className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<h2
						id="report-card-title"
						data-testid="report-card-headline"
						className="text-foreground text-2xl font-semibold tracking-tight text-balance"
					>
						{head.title}
					</h2>
					<p data-testid="report-card-verdict" className="text-muted-foreground max-w-prose text-sm">
						{head.detail}
					</p>
					{cost && (
						<p data-testid="report-card-cost" className="text-muted-foreground text-sm tabular-nums">
							{cost}
						</p>
					)}
					{brokenChild?.experiment_id && !readOnly && (
						<Link
							to="/experiments/$expId"
							params={{ expId: brokenChild.experiment_id }}
							data-testid="report-card-thrown-out-link"
							className="text-primary inline-flex w-fit items-center gap-1 text-sm font-medium underline-offset-4 hover:underline"
						>
							{t08("thrownOut.openComparison")}
							<ArrowUpRight className="size-3.5" aria-hidden="true" />
						</Link>
					)}
					{report.split === "held_out" && (
						<p data-testid="report-card-split" className="text-muted-foreground text-xs">
							Held-out tasks only
						</p>
					)}
					{report.mcp_route && report.mcp_route !== "direct" && (
						<p data-testid="report-card-mcp-route" className="text-muted-foreground text-xs">
							{report.mcp_route === "bifrost_code_mode" ? "MCP tools through Bifrost, Code Mode" : "MCP tools through Bifrost MCP gateway"}
						</p>
					)}
					{ab && <ArmLegend arms={report.arms} />}
					{report.practice_runs ? (
						<p data-testid="report-card-practice" className="text-muted-foreground max-w-prose text-xs">
							{report.note || `${report.practice_runs} practice runs were left out.`}
						</p>
					) : null}
				</div>
				{actions ?? (readOnly ? null : <ExportButtons report={report} />)}
			</header>
			{belowHeadline}

			<ReportFilterBar where={report.where} onClear={onWhere && (() => onWhere(undefined))} />

			<Section title={tr("sections.capability")} description={tr("sections.capabilityHelp")}>
				<CapabilityTable report={report} />
			</Section>

			<Section title={tr("sections.benchmark")} description={tr("sections.benchmarkHelp")}>
				<BenchmarkList report={report} readOnly={readOnly} />
			</Section>

			<ReportSlices report={report} onShowOnly={onWhere && ((key, value) => onWhere({ ...report.where, [key]: value }))} />

			{reliabilityCards(report).length > 0 && (
				<Section
					title={tr("sections.reliability")}
					description={tr("sections.reliabilityHelp")}
				>
					<ReliabilityCards report={report} />
				</Section>
			)}

			{(report.resilience?.length ?? 0) > 0 && (
				<Section
					title={tr("sections.resilience")}
					description={tr("sections.resilienceHelp")}
				>
					<ResilienceCard report={report} />
				</Section>
			)}

			{(report.resilience?.length ?? 0) === 0 && <StoreDroppedFaults evaluationId={report.evaluation} />}

			{report.failover && <FailoverSection report={report.failover} />}

			{report.reliability.length > 0 && (
				<Section title={tr("sections.faults")} description={tr("sections.faultsHelp")}>
					<ReliabilityTable report={report} />
				</Section>
			)}

			{hasLearning(report) && (
				<Section
					title={tr("sections.learning")}
					description={tr("sections.learningHelp")}
				>
					<LearningTable report={report} />
				</Section>
			)}

			{hasVoice(report) && (
				<Section title={tr("sections.voice")} description={tr("sections.voiceHelp")}>
					<VoiceTable report={report} />
				</Section>
			)}

			{hasPersistence(report) && (
				<Section title={tr("sections.persistence")} description={tr("sections.persistenceHelp")}>
					<PersistenceTable report={report} />
				</Section>
			)}

			{hasVoiceCalls(report) && (
				<Section
					title={tr("sections.voiceCalls")}
					description={tr("sections.voiceCallsHelp")}
				>
					<VoiceCallsTable report={report} />
				</Section>
			)}

			{hasBifrost(report) && (
				<Section
					title={tr("sections.bifrost")}
					description={tr("sections.bifrostHelp")}
				>
					<BifrostTable report={report} />
				</Section>
			)}

			{report.safety && (Object.keys(report.safety.attack_resisted).length > 0 || Object.keys(report.safety.cheat_rate).length > 0) && (
				<Section title={tr("sections.safety")} description={tr("sections.safetyHelp")}>
					<SafetyTable report={report} />
				</Section>
			)}
			<CheatRateRow report={report} />

			{hasRedTeam(report) && (
				<Section
					title={tr("sections.redTeam")}
					description={tr("sections.redTeamHelp")}
				>
					<RedTeamSection report={report} />
				</Section>
			)}

			{report.alignment && report.alignment.categories.length > 0 && (
				<Section
					title={tr("sections.alignment")}
					description={tr("sections.alignmentHelp")}
				>
					<AlignmentSection report={report} />
				</Section>
			)}
		</article>
	);
}

function ArmLegend({ arms }: { arms: EvaluationArm[] }) {
	return (
		<dl className="text-muted-foreground mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
			{arms.map((arm) => (
				<div key={arm.label} className="flex min-w-0 gap-1.5">
					<dt className="text-foreground font-semibold">{arm.label}</dt>
					<dd className="truncate">{armDescription(arm)}</dd>
				</div>
			))}
		</dl>
	);
}

/** Visual CI range plus score tick. Decorative: the numbers are always printed next to it. */
export function CiBar({ metrics }: { metrics: ReportArmMetrics }) {
	const clamp = (x: number) => Math.max(0, Math.min(1, Number.isFinite(x) ? x : 0)) * 100;
	const range = scoreRange(metrics);
	const low = clamp(range.low);
	const high = clamp(range.high);
	return (
		<span aria-hidden="true" className="bg-muted relative block h-1.5 w-20 shrink-0 rounded-full">
			<span className="bg-primary/30 absolute inset-y-0 rounded-full" style={{ left: `${low}%`, width: `${Math.max(high - low, 1)}%` }} />
			<span className="bg-primary absolute -top-0.5 h-2.5 w-0.5 rounded-full" style={{ left: `calc(${clamp(metrics.score)}% - 1px)` }} />
		</span>
	);
}

function ScoreCell({ metrics }: { metrics?: ReportArmMetrics }) {
	if (!metrics || !metrics.runs) return <span className="text-muted-foreground">-</span>; // no runs is no score, never 0%
	const range = scoreRange(metrics);
	return (
		<span className="flex items-center gap-2">
			<span className="text-foreground w-10 font-medium tabular-nums">{pct(metrics.score)}</span>
			<CiBar metrics={metrics} />
			<span className="text-muted-foreground text-sm tabular-nums">
				<span className="sr-only">95% range </span>
				{pct(range.low)}-{pct(range.high)}
			</span>
		</span>
	);
}

/** Winner marker: icon plus text, never color alone. */
export function WinnerMark({ result, name }: { result?: ComparisonResult; name?: string }) {
	if (!result) return <span className="text-muted-foreground">-</span>;
	if (result.winner === "tie") {
		return (
			<span
				data-testid="winner-mark"
				className="text-muted-foreground inline-flex items-center gap-1 text-xs font-medium whitespace-nowrap"
			>
				<Equal className="size-3.5" aria-hidden="true" />
				No clear winner
			</span>
		);
	}
	return (
		<span data-testid="winner-mark" className="text-foreground inline-flex items-center gap-1 text-xs font-semibold whitespace-nowrap">
			<Check className="text-chart-success-ink size-3.5" aria-hidden="true" />
			{name || result.winner} better
		</span>
	);
}

const METRIC_HEADS = ["Score", "Runs", "Cost", "Tokens", "Steps", "Tool calls", "Time"] as const;

function MetricCells({ metrics }: { metrics?: ReportArmMetrics }) {
	const num = "text-right tabular-nums";
	return (
		<>
			<TableCell>
				<ScoreCell metrics={metrics} />
			</TableCell>
			<TableCell className={num}>{metrics ? compactNumber(metrics.runs) : "-"}</TableCell>
			<TableCell className={num}>
				<CostValue metrics={metrics} />
			</TableCell>
			<TableCell className={num}>
				{metrics && metrics.tokens != null ? compactNumber(metrics.tokens) : metrics?.runs ? "Not known" : "-"}
			</TableCell>
			<TableCell className={num}>{metrics ? compactNumber(metrics.steps) : "-"}</TableCell>
			<TableCell className={num}>{metrics ? compactNumber(metrics.tool_calls) : "-"}</TableCell>
			<TableCell className={num}>{metrics ? duration(metrics.wall_s) : "-"}</TableCell>
		</>
	);
}

/** The cost of one arm: the sum, or "Not known" with the count of unpriced runs (reason in the tooltip). */
export function CostValue({ metrics }: { metrics?: ReportArmMetrics }) {
	const c = costDisplay(metrics);
	if (c.known) return <>{c.text}</>;
	return (
		<span data-testid="cost-not-known" title={c.reason} className="inline-flex flex-col items-end leading-tight">
			<span className="text-muted-foreground">{c.text}</span>
			<span className="text-muted-foreground text-[11px]">{c.detail}</span>
		</span>
	);
}

function CapabilityTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const [a, b] = report.arms;
	const rows = capabilityRows(report);
	const { t } = useTranslation("hw08");
	const { t: tr } = useTranslation("reportCard");
	const untested = rows.filter((r) => !r.tested).map((r) => capabilityName(r.capability));
	if (rows.length === 0) return <p className="text-muted-foreground text-sm">{t("noCapabilities")}</p>;
	return (
		<div className="flex min-w-0 flex-col gap-2">
		<Table data-testid="report-capabilities" aria-label="Score by capability">
			<TableHeader>
				<TableRow>
					<TableHead scope="col">Capability</TableHead>
					{ab && <TableHead scope="col">Setup</TableHead>}
					{METRIC_HEADS.map((h) => (
						<TableHead key={h} scope="col" className={h === "Score" ? undefined : "text-right"}>
							{h}
						</TableHead>
					))}
					{ab && <TableHead scope="col" className="text-right">{`Change (${b.label} vs ${a.label})`}</TableHead>}
					{ab && <TableHead scope="col">Winner</TableHead>}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map(({ capability, data, tested }, i) => {
					const name = capabilityName(capability);
					if (!data || !tested) return null; // listed once under the table (M3-12)
					if (!ab) {
						return (
							<TableRow key={capability} data-testid={`capability-${capability}`}>
								<TableHead scope="row" className="font-medium">
									{name}
								</TableHead>
								<MetricCells metrics={data.arms[a.label]} />
							</TableRow>
						);
					}
					const cmp = compareArms(data.arms, a.label, b.label);
					return (
						<React.Fragment key={capability}>
							{[a, b].map((arm, i) => (
								<TableRow key={arm.label} data-testid={i === 0 ? `capability-${capability}` : `capability-${capability}-${arm.label}`}>
									{i === 0 && (
										<TableHead scope="rowgroup" rowSpan={2} className="align-top font-medium">
											{name}
										</TableHead>
									)}
									<TableCell className="font-semibold">{arm.label}</TableCell>
									<MetricCells metrics={data.arms[arm.label]} />
									{i === 0 && (
										<>
											<TableCell rowSpan={2} className="text-right align-top tabular-nums">
												{cmp ? points(cmp.delta) : "-"}
											</TableCell>
											<TableCell rowSpan={2} className="align-top">
												<WinnerMark result={cmp} name={cmp ? armName(report, cmp.winner) : undefined} />
											</TableCell>
										</>
									)}
								</TableRow>
							))}
						</React.Fragment>
					);
				})}
			</TableBody>
		</Table>
		{untested.length > 0 && (
			<p className="text-muted-foreground text-xs" data-testid="capabilities-not-tested">
				{tr("sections.notTested", { list: untested.join(", ") })}
			</p>
		)}
		</div>
	);
}

function BenchmarkList({ report, readOnly }: { report: Report; readOnly: boolean }) {
	if (report.benchmarks.length === 0) return <p className="text-muted-foreground text-sm">No benchmark results yet.</p>;
	return (
		<ul data-testid="report-benchmarks" className="border-border/80 divide-border/80 divide-y rounded-lg border">
			{report.benchmarks.map((row) => (
				<li key={row.benchmark}>
					<BenchmarkRow report={report} row={row} readOnly={readOnly} />
				</li>
			))}
		</ul>
	);
}

function BenchmarkRow({ report, row, readOnly }: { report: Report; row: ReportBenchmark; readOnly: boolean }) {
	const ab = isAB(report);
	const [a, b] = report.arms;
	const result = ab ? benchmarkWinner(row, a.label, b.label) : undefined;
	const single = row.arms[a?.label ?? ""];
	const judge = judgeBadge(row);
	const paired = ab ? pairedChangeText(row.verdict) : undefined;
	const summary =
		row.verdict?.summary ||
		(ab
			? result && result.winner !== "tie"
				? `${armName(report, result.winner)} solves ${Math.abs(Math.round(result.delta * 100))} points more`
				: "No clear difference between the agents"
			: single?.runs
				? report.mode === "score"
					? `Score ${pct(single.score)} over ${compactNumber(single.runs)} runs`
					: `Solves ${pct(single.score)} of ${compactNumber(single.runs)} runs`
				: "No runs yet");
	return (
		<details data-testid={`benchmark-${row.benchmark}`} className="group/bench">
			<summary className="hover:bg-muted/40 focus-visible:ring-ring/50 flex cursor-pointer list-none flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden">
				<ChevronRight
					className="text-muted-foreground size-4 shrink-0 transition-transform duration-150 group-open/bench:rotate-90"
					aria-hidden="true"
				/>
				<span className="text-foreground min-w-0 flex-1 truncate font-medium">{row.benchmark}</span>
				<span className="text-muted-foreground text-xs">{capabilityName(row.capability)}</span>
				{judge && (
					<Badge variant={judge.tone} title={judge.title} data-testid={`benchmark-judge-${row.benchmark}`}>
						{judge.text}
					</Badge>
				)}
				<span className="flex items-center gap-3 tabular-nums">
					{report.arms.map((arm) => (
						<span key={arm.label} className="text-foreground">
							{ab && <span className="text-muted-foreground mr-1 text-xs">{arm.label}</span>}
							{row.arms[arm.label]?.runs ? pct(row.arms[arm.label]?.score) : "-"}
						</span>
					))}
				</span>
				{ab && <WinnerMark result={result} name={result ? armName(report, result.winner) : undefined} />}
			</summary>
			<div className="flex flex-col gap-3 px-4 pb-4 pl-12 text-sm">
				<p className="text-foreground">{summary}</p>
				{report.arms.map((arm) => {
					const text = excludedText(row.arms[arm.label]?.excluded);
					if (!text) return null;
					return (
						<p key={arm.label} data-testid={`benchmark-excluded-${row.benchmark}`} className="text-muted-foreground text-xs">
							{ab ? `${arm.label} ${text}` : text[0].toUpperCase() + text.slice(1)}. Not counted in the score.
						</p>
					);
				})}
				<ThrownOutLines report={report} row={row} />
				{row.verdict && ab && (
					<p className="text-muted-foreground text-xs">
						{points(row.verdict.delta)}, {row.verdict.significant ? "a real difference" : "within noise"}
						{paired ? `; ${paired}` : ""}
					</p>
				)}
				{judge && (
					<p data-testid={`benchmark-judge-detail-${row.benchmark}`} className="text-muted-foreground text-xs">
						{judge.title}
						{row.suite_version ? ` Benchmark version ${row.suite_version}.` : ""}
					</p>
				)}
				<dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs sm:grid-cols-4">
					{report.arms.map((arm) => {
						const m = row.arms[arm.label];
						const prefix = ab ? `${arm.label} ` : "";
						return (
							<React.Fragment key={arm.label}>
								<MetricPair label={`${prefix}runs`} value={m ? compactNumber(m.runs) : "-"} />
								<MetricPair label={`${prefix}cost`} value={m ? costText(m) : "-"} />
								<MetricPair
									label={`${prefix}tokens`}
									value={m && m.tokens != null ? compactNumber(m.tokens) : m?.runs ? "Not known" : "-"}
								/>
								<MetricPair label={`${prefix}time`} value={m ? duration(m.wall_s) : "-"} />
							</React.Fragment>
						);
					})}
				</dl>
				{report.arms.map((arm) => {
					const c = costDisplay(row.arms[arm.label]);
					return c.known ? null : (
						<p key={`cost-${arm.label}`} data-testid="cost-reason" className="text-muted-foreground text-xs">
							{ab ? `${arm.label}: ` : ""}
							{c.reason}
						</p>
					);
				})}
				{hasProcess(row.arms) && (
					<div className="flex flex-col gap-1 text-xs" data-testid={`benchmark-process-${row.benchmark}`}>
						<span className="text-muted-foreground font-medium" title={PROCESS_NOTE}>
							Process (report only)
						</span>
						{report.arms.map((arm) => (
							<span key={arm.label} className="text-foreground tabular-nums">
								{ab && <span className="text-muted-foreground mr-1">{arm.label}</span>}
								{formatProcess(row.arms[arm.label]?.process)}
							</span>
						))}
						<span className="text-muted-foreground">{PROCESS_NOTE}</span>
					</div>
				)}
				{row.experiment_id && !readOnly && (
					<Link
						to="/experiments/$expId"
						params={{ expId: row.experiment_id }}
						data-testid={`benchmark-link-${row.benchmark}`}
						className="text-primary inline-flex w-fit items-center gap-1 text-sm font-medium underline-offset-4 hover:underline"
					>
						Open experiment
						<ArrowUpRight className="size-3.5" aria-hidden="true" />
					</Link>
				)}
			</div>
		</details>
	);
}

/** Per arm, the runs a safety check threw out of this benchmark's score and why (fix wave 3, N215). */
function ThrownOutLines({ report, row }: { report: Report; row: ReportBenchmark }) {
	const { t } = useTranslation("hw08");
	const checkName = useCheckName();
	const ab = isAB(report);
	return (
		<>
			{report.arms.map((arm) => {
				const to = row.thrown_out?.[arm.label];
				if (!to || to.runs <= 0) return null;
				const ids = Object.keys(to.checks ?? {}).filter((id) => (to.checks?.[id] ?? 0) > 0);
				const text = ids.length
					? t("thrownOut.checks", { count: to.runs, checks: ids.map(checkName).join(" and ") })
					: t("thrownOut.whole", { count: to.runs });
				return (
					<div
						key={arm.label}
						data-testid={`benchmark-thrown-out-${row.benchmark}`}
						className="text-muted-foreground flex flex-col gap-0.5 text-xs"
					>
						<p>{ab ? t("thrownOut.setup", { arm: arm.label, text }) : text}</p>
						{to.reasons && to.reasons.length > 0 && <p>{t("thrownOut.why", { reasons: to.reasons.join("; ") })}</p>}
					</div>
				);
			})}
		</>
	);
}

function MetricPair({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex justify-between gap-2">
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-foreground tabular-nums">{value}</dd>
		</div>
	);
}

function ReliabilityTable({ report }: { report: Report }) {
	// Fault runs whose task has no finished normal run are left out of both columns (paired by task).
	const unpaired = report.reliability.reduce((n, row) => n + Object.values(row.arms ?? {}).reduce((m, a) => m + (a?.unpaired ?? 0), 0), 0);
	return (
		<>
			<Table data-testid="report-reliability" aria-label="Score under each fault">
				<TableHeader>
					<TableRow>
						<TableHead scope="col">Fault</TableHead>
						{report.arms.map((arm) => (
							<React.Fragment key={arm.label}>
								<TableHead scope="col" className="text-right">
									{isAB(report) ? `${arm.label} score` : "Score"}
								</TableHead>
								<TableHead scope="col" className="text-right">
									{isAB(report) ? `${arm.label} change from normal` : "Change from normal"}
								</TableHead>
							</React.Fragment>
						))}
					</TableRow>
				</TableHeader>
				<TableBody>
					{report.reliability.map((row) => (
						<TableRow key={row.fault} data-testid={`fault-${row.fault}`}>
							<TableHead scope="row" className="font-medium">
								{faultName(row.fault)}
							</TableHead>
							{report.arms.map((arm) => {
								const m = row.arms[arm.label];
								return (
									<React.Fragment key={arm.label}>
										<TableCell className="text-right tabular-nums">{pct(m?.score)}</TableCell>
										<TableCell className="text-right tabular-nums">{m ? faultChange(m).text : "-"}</TableCell>
									</React.Fragment>
								);
							})}
						</TableRow>
					))}
				</TableBody>
			</Table>
			{unpaired > 0 && (
				<p className="text-muted-foreground mt-2 text-xs" data-testid="report-reliability-unpaired">
					{unpaired === 1
						? "1 run under faults was left out: its task has no finished normal run to compare with."
						: `${unpaired} runs under faults were left out: their task has no finished normal run to compare with.`}
				</p>
			)}
		</>
	);
}

function SafetyTable({ report }: { report: Report }) {
	const safety = report.safety!;
	return (
		<Table data-testid="report-safety" aria-label="Safety">
			<TableHeader>
				<TableRow>
					<TableHead scope="col">Measure</TableHead>
					{report.arms.map((arm) => (
						<TableHead key={arm.label} scope="col" className="text-right">
							{isAB(report) ? arm.label : "Your agent"}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				<TableRow data-testid="safety-attacks">
					<TableHead scope="row" className="font-medium">
						Attacks resisted
					</TableHead>
					{report.arms.map((arm) => (
						<TableCell key={arm.label} className="text-right tabular-nums">
							{pct(safety.attack_resisted[arm.label])}
						</TableCell>
					))}
				</TableRow>
				<TableRow data-testid="safety-cheat">
					<TableHead scope="row" className="font-medium">
						Cheat rate
					</TableHead>
					{report.arms.map((arm) => (
						<TableCell key={arm.label} className="text-right tabular-nums">
							{pct(safety.cheat_rate[arm.label])}
						</TableCell>
					))}
				</TableRow>
			</TableBody>
		</Table>
	);
}

function AlignmentCell({ rate }: { rate?: ReportAlignmentRate }) {
	if (!rate) return <span className="text-muted-foreground">Not tested</span>;
	return (
		<span className="flex flex-col items-end">
			<span>{alignmentText(rate)}</span>
			<span className="text-muted-foreground text-xs">
				{rate.fired} of {rate.runs} runs
			</span>
		</span>
	);
}

function AlignmentSection({ report }: { report: Report }) {
	const alignment = report.alignment!;
	const flagged = alignment.impossible_flagged ?? {};
	const coverage = alignment.owasp_agentic ?? [];
	const tested = coverage.filter((c) => c.tested).length;
	const { t: tr } = useTranslation("reportCard");
	// Only tested rows; the untested ones are named once under the table (M3-12).
	const testedCategories = alignment.categories.filter((c) => report.arms.some((a) => c.arms[a.label]));
	const untestedCategories = alignment.categories.filter((c) => !report.arms.some((a) => c.arms[a.label]));
	return (
		<div className="flex flex-col gap-4">
			<Table data-testid="report-alignment" aria-label="Safety and alignment">
				<TableHeader>
					<TableRow>
						<TableHead scope="col">Category</TableHead>
						{report.arms.map((arm) => (
							<TableHead key={arm.label} scope="col" className="text-right">
								{isAB(report) ? arm.label : "Your agent"}
							</TableHead>
						))}
					</TableRow>
				</TableHeader>
				<TableBody>
					{testedCategories.map((c) => (
						<TableRow key={c.id} data-testid={`alignment-${c.id}`}>
							<TableHead scope="row" className="font-medium whitespace-normal">
								<span className="block">{c.label}</span>
								{c.description && (
									<span className="text-muted-foreground block text-xs font-normal">
										{/* The server's "heuristic phrase scan" help in plain words (M3-12). */}
										{/heuristic phrase scan/i.test(c.description) ? tr("alignment.awareness") : c.description}
									</span>
								)}
							</TableHead>
							{report.arms.map((arm) => (
								<TableCell key={arm.label} className="text-right tabular-nums">
									<AlignmentCell rate={c.arms[arm.label]} />
								</TableCell>
							))}
						</TableRow>
					))}
					{Object.keys(flagged).length > 0 && (
						<TableRow data-testid="alignment-impossible-flagged">
							<TableHead scope="row" className="font-medium whitespace-normal">
								<span className="block">Impossible tasks flagged</span>
								<span className="text-muted-foreground block text-xs font-normal">
									Said the tests and the instruction conflict instead of faking a pass (higher is better).
								</span>
							</TableHead>
							{report.arms.map((arm) => (
								<TableCell key={arm.label} className="text-right tabular-nums">
									<AlignmentCell rate={flagged[arm.label]} />
								</TableCell>
							))}
						</TableRow>
					)}
				</TableBody>
			</Table>
			{untestedCategories.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="alignment-not-tested">
					{tr("sections.notTested", { list: untestedCategories.map((c) => c.label).join(", ") })}
				</p>
			)}
			{coverage.length > 0 && (
				<div data-testid="alignment-owasp" className="flex flex-col gap-2">
					<p className="text-muted-foreground text-xs">
						{tr("redTeam.owaspCovers", {
							tested: `${tested} of ${coverage.length}`,
							names: coverage
								.filter((c) => c.tested)
								.map((c) => `${c.id} ${c.name}`)
								.join(", "),
						})}
					</p>
					<DetailsDisclosure label={tr("redTeam.owaspAll")} className="text-xs">
					<ul className="flex flex-wrap gap-1.5">
						{coverage.map((c) => (
							<li key={c.id}>
								<Badge variant={c.tested ? "success" : "neutral"} title={`${c.name}: ${c.tested ? "tested" : "not tested"}`}>
									{c.id} {c.name}
									<span className="sr-only">{c.tested ? ", tested" : ", not tested"}</span>
								</Badge>
							</li>
						))}
					</ul>
					</DetailsDisclosure>
				</div>
			)}
		</div>
	);
}

/** JSON download, the server exports of the report's evaluation (HTML, Markdown, PDF, reproduce kit) and copy link. */
export function ExportButtons({ report }: { report: Report }) {
	const { t } = useTranslation("fw09");
	const [copied, setCopied] = React.useState<"" | "ok" | "fail">("");
	const [pdfFailed, setPdfFailed] = React.useState(false);
	const id = report.evaluation;

	const download = () => downloadReportJSON(report);

	const copyLink = async () => {
		try {
			await navigator.clipboard.writeText(window.location.href);
			setCopied("ok");
		} catch {
			setCopied("fail");
		}
		window.setTimeout(() => setCopied(""), 2000);
	};

	return (
		<div className="flex shrink-0 flex-wrap items-center gap-2" data-testid="report-export">
			<Button variant="outline" size="sm" onClick={download} data-testid="report-download-json">
				<Download aria-hidden="true" />
				Download JSON
			</Button>
			{id && (
				<>
					<Button asChild variant="outline" size="sm">
						<a href={reportExportUrl(id, "html")} download data-testid="report-download-html">
							<FileText aria-hidden="true" />
							{t("export.html")}
						</a>
					</Button>
					<Button asChild variant="outline" size="sm">
						<a href={reportExportUrl(id, "md")} download data-testid="report-download-md">
							<FileCode2 aria-hidden="true" />
							{t("export.markdown")}
						</a>
					</Button>
					<Button
						type="button"
						variant="outline"
						size="sm"
						dataTestId="report-download-pdf"
						onClick={async () => setPdfFailed(!(await printExport(reportExportUrl(id, "html"))))}
					>
						<Printer aria-hidden="true" />
						{t("export.pdf")}
					</Button>
					<Button asChild variant="outline" size="sm">
						<a href={reproduceUrl(id)} download data-testid="report-download-reproduce">
							<Package aria-hidden="true" />
							{t("export.reproduce")}
						</a>
					</Button>
				</>
			)}
			<Button variant="outline" size="sm" onClick={copyLink} data-testid="report-copy-link">
				<Link2 aria-hidden="true" />
				{copied === "ok" ? "Link copied" : "Copy link"}
			</Button>
			<span role="status" aria-live="polite" className="sr-only">
				{copied === "ok" ? "Link copied" : copied === "fail" ? "Could not copy the link" : ""}
			</span>
			{pdfFailed && (
				<p className="text-destructive w-full text-xs" role="alert" data-testid="report-pdf-failed">
					{t("export.pdfFailed")}
				</p>
			)}
		</div>
	);
}