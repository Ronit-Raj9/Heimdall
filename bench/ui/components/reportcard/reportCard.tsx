import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { EvaluationArm, Report, ReportArmMetrics, ReportBenchmark } from "@/lib/store/apis/evaluationsApi";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight, Check, ChevronRight, Download, Equal, Link2 } from "lucide-react";
import * as React from "react";
import {
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
	headline,
	isAB,
	visibleReport,
	pct,
	points,
	reportFileName,
} from "./reportCard.utils";

function costText(m: ReportArmMetrics): string {
	const c = costDisplay(m);
	return c.known ? c.text : `${c.text} (${c.detail})`;
}

export interface ReportCardProps {
	report: Report;
	className?: string;
}

/** The report card of one evaluation: headline answer, capabilities, benchmarks, reliability, safety. */
export function ReportCard({ report: raw, className }: ReportCardProps) {
	const report = visibleReport(raw);
	const ab = isAB(report);
	const head = headline(report);
	return (
		<article data-testid="report-card" aria-labelledby="report-card-title" className={cn("flex min-w-0 flex-col gap-8", className)}>
			<header className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">Report card</p>
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
					{report.split === "held_out" && (
						<p data-testid="report-card-split" className="text-muted-foreground text-xs">
							Held-out tasks only
						</p>
					)}
					{ab && <ArmLegend arms={report.arms} />}
					{report.practice_runs ? (
						<p data-testid="report-card-practice" className="text-muted-foreground max-w-prose text-xs">
							{report.note || `${report.practice_runs} practice runs were left out.`}
						</p>
					) : null}
				</div>
				<ExportButtons report={report} />
			</header>

			<Section title="By capability" description="Score is the share of runs solved. The bar shows the 95% confidence range.">
				<CapabilityTable report={report} />
			</Section>

			<Section title="By benchmark" description="Open a row for the verdict and a link to the experiment.">
				<BenchmarkList report={report} />
			</Section>

			{report.reliability.length > 0 && (
				<Section title="Reliability" description="Score when something goes wrong, and how far it drops from a clean run.">
					<ReliabilityTable report={report} />
				</Section>
			)}

			{report.safety && (Object.keys(report.safety.attack_resisted).length > 0 || Object.keys(report.safety.cheat_rate).length > 0) && (
				<Section title="Safety" description="Attacks resisted (higher is better) and cheat rate (lower is better).">
					<SafetyTable report={report} />
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
	const low = clamp(metrics.ci_low);
	const high = clamp(metrics.ci_high);
	return (
		<span aria-hidden="true" className="bg-muted relative block h-1.5 w-20 shrink-0 rounded-full">
			<span className="bg-primary/30 absolute inset-y-0 rounded-full" style={{ left: `${low}%`, width: `${Math.max(high - low, 1)}%` }} />
			<span className="bg-primary absolute -top-0.5 h-2.5 w-0.5 rounded-full" style={{ left: `calc(${clamp(metrics.score)}% - 1px)` }} />
		</span>
	);
}

function ScoreCell({ metrics }: { metrics?: ReportArmMetrics }) {
	if (!metrics) return <span className="text-muted-foreground">-</span>;
	return (
		<span className="flex items-center gap-2">
			<span className="text-foreground w-10 font-medium tabular-nums">{pct(metrics.score)}</span>
			<CiBar metrics={metrics} />
			<span className="text-muted-foreground text-xs tabular-nums">
				<span className="sr-only">95% range </span>
				{pct(metrics.ci_low)}-{pct(metrics.ci_high)}
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
			<TableCell className={num}>{metrics && metrics.tokens != null ? compactNumber(metrics.tokens) : metrics?.runs ? "Not known" : "-"}</TableCell>
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
	return (
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
				{rows.map(({ capability, data }) => {
					const name = capabilityName(capability);
					if (!data) {
						return (
							<TableRow key={capability} data-testid={`capability-${capability}`}>
								<TableHead scope="row" className="text-muted-foreground font-normal">
									{name}
								</TableHead>
								<TableCell colSpan={METRIC_HEADS.length + (ab ? 3 : 0)} className="text-muted-foreground text-xs">
									Not tested
								</TableCell>
							</TableRow>
						);
					}
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
	);
}

function BenchmarkList({ report }: { report: Report }) {
	if (report.benchmarks.length === 0) return <p className="text-muted-foreground text-sm">No benchmark results yet.</p>;
	return (
		<ul data-testid="report-benchmarks" className="border-border/80 divide-border/80 divide-y rounded-lg border">
			{report.benchmarks.map((row) => (
				<li key={row.benchmark}>
					<BenchmarkRow report={report} row={row} />
				</li>
			))}
		</ul>
	);
}

function BenchmarkRow({ report, row }: { report: Report; row: ReportBenchmark }) {
	const ab = isAB(report);
	const [a, b] = report.arms;
	const result = ab ? benchmarkWinner(row, a.label, b.label) : undefined;
	const single = row.arms[a?.label ?? ""];
	const summary =
		row.verdict?.summary ||
		(ab
			? result && result.winner !== "tie"
				? `${armName(report, result.winner)} solves ${Math.abs(Math.round(result.delta * 100))} points more`
				: "No clear difference between the agents"
			: single
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
				<span className="flex items-center gap-3 tabular-nums">
					{report.arms.map((arm) => (
						<span key={arm.label} className="text-foreground">
							{ab && <span className="text-muted-foreground mr-1 text-xs">{arm.label}</span>}
							{pct(row.arms[arm.label]?.score)}
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
				{row.verdict && ab && (
					<p className="text-muted-foreground text-xs">
						{points(row.verdict.delta)}, {row.verdict.significant ? "a real difference" : "within noise"}
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
								<MetricPair label={`${prefix}tokens`} value={m && m.tokens != null ? compactNumber(m.tokens) : m?.runs ? "Not known" : "-"} />
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
				{row.experiment_id && (
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

function MetricPair({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex justify-between gap-2">
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-foreground tabular-nums">{value}</dd>
		</div>
	);
}

function ReliabilityTable({ report }: { report: Report }) {
	return (
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
								{isAB(report) ? `${arm.label} drop` : "Drop"}
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
									<TableCell className="text-right tabular-nums">{m ? points(m.drop) : "-"}</TableCell>
								</React.Fragment>
							);
						})}
					</TableRow>
				))}
			</TableBody>
		</Table>
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

/** JSON download and copy link. */
export function ExportButtons({ report }: { report: Report }) {
	const [copied, setCopied] = React.useState<"" | "ok" | "fail">("");

	const download = () => {
		const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
		const url = URL.createObjectURL(blob);
		const a = document.createElement("a");
		a.href = url;
		a.download = reportFileName(report);
		document.body.appendChild(a);
		a.click();
		a.remove();
		URL.revokeObjectURL(url);
	};

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
			<Button variant="outline" size="sm" onClick={copyLink} data-testid="report-copy-link">
				<Link2 aria-hidden="true" />
				{copied === "ok" ? "Link copied" : "Copy link"}
			</Button>
			<span role="status" aria-live="polite" className="sr-only">
				{copied === "ok" ? "Link copied" : copied === "fail" ? "Could not copy the link" : ""}
			</span>
		</div>
	);
}