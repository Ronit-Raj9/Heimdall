import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report, ReportCurvePoint, ReportRate } from "@/lib/store/apis/evaluationsApi";
import { isAB, pct, points } from "./reportCard.utils";

/** Rows of the learning section, or none when no benchmark ran as a task sequence. */
export function hasLearning(report: Report): boolean {
	return (report.learning?.length ?? 0) > 0;
}

export function hasPersistence(report: Report): boolean {
	return (report.persistence?.length ?? 0) > 0;
}

/** Success per dollar, "4.2 per $". */
export function perDollar(rate: ReportRate | undefined): string {
	if (!rate || !Number.isFinite(rate.value)) return "-";
	const v = rate.value;
	return `${v >= 10 ? Math.round(v) : v.toFixed(1)} per $`;
}

/** Small learning curve. Decorative: every number is printed beside it. */
export function Sparkline({ curve, width = 96, height = 24 }: { curve: ReportCurvePoint[]; width?: number; height?: number }) {
	if (curve.length < 2) return <span className="text-muted-foreground text-xs">-</span>;
	const first = curve[0].index;
	const span = Math.max(curve[curve.length - 1].index - first, 1);
	const pad = 2;
	const xy = curve.map((p) => {
		const x = pad + ((p.index - first) / span) * (width - 2 * pad);
		const y = pad + (1 - Math.max(0, Math.min(1, p.success))) * (height - 2 * pad);
		return `${x.toFixed(1)},${y.toFixed(1)}`;
	});
	return (
		<svg aria-hidden="true" width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="text-primary shrink-0" data-testid="learning-sparkline">
			<line x1={pad} x2={width - pad} y1={height - pad} y2={height - pad} className="stroke-border" strokeWidth={1} />
			<polyline points={xy.join(" ")} fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
		</svg>
	);
}

/** "Learning over time": how each benchmark run as a task sequence with memory kept improved. */
export function LearningTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const rows = report.learning ?? [];
	const controls = [...new Set(rows.map((r) => r.control).filter((c): c is string => Boolean(c)))];
	return (
		<div className="flex flex-col gap-2">
			{controls.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="learning-control">
					Gain from memory compares with {controls.join(", ")}: the same agent on the same tasks in the same order, starting each task with
					empty memory. It is not on any leaderboard.
				</p>
			)}
			<Table data-testid="report-learning" aria-label="Learning over time">
				<TableHeader>
					<TableRow>
						<TableHead scope="col">Benchmark</TableHead>
						{ab && <TableHead scope="col">Agent</TableHead>}
						<TableHead scope="col">Curve</TableHead>
						<TableHead scope="col" className="text-right">
							Average success
						</TableHead>
						<TableHead scope="col" className="text-right">
							Gain start to end
						</TableHead>
						<TableHead scope="col" className="text-right">
							Kept on old tasks
						</TableHead>
						<TableHead scope="col" className="text-right">
							Gain from memory
						</TableHead>
						<TableHead scope="col" className="text-right">
							Tasks to reach target
						</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{rows.map((row) => (
						<TableRow key={`${row.benchmark}-${row.arm}`} data-testid={`learning-${row.benchmark}-${row.arm}`}>
							<TableHead scope="row" className="font-medium">
								{row.benchmark}
							</TableHead>
							{ab && <TableCell className="font-semibold">{row.arm}</TableCell>}
							<TableCell>
								<Sparkline curve={row.curve} />
							</TableCell>
							<TableCell className="text-right tabular-nums">{pct(row.aulc)}</TableCell>
							<TableCell className="text-right tabular-nums">{points(row.final_gain)}</TableCell>
							<TableCell className="text-right tabular-nums">{points(row.bwt)}</TableCell>
							<TableCell className="text-right tabular-nums">{points(row.fwt)}</TableCell>
							<TableCell className="text-right tabular-nums">
								{row.episodes_to_threshold == null ? `Not reached (${pct(row.threshold)})` : `${row.episodes_to_threshold} (${pct(row.threshold)})`}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}

/** "Persistence": resume after a crash, finish under a cut budget, success per dollar. */
export function PersistenceTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const rows = report.persistence ?? [];
	const rate = (r?: ReportRate) => (r ? `${pct(r.value)} of ${r.runs}` : "-");
	return (
		<Table data-testid="report-persistence" aria-label="Persistence">
			<TableHeader>
				<TableRow>
					<TableHead scope="col">Agent</TableHead>
					<TableHead scope="col" className="text-right">
						Resumed without redoing work
					</TableHead>
					<TableHead scope="col" className="text-right">
						Finished cleanly when budget ran out
					</TableHead>
					<TableHead scope="col" className="text-right">
						Success per dollar
					</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={row.arm} data-testid={`persistence-${row.arm}`}>
						<TableHead scope="row" className="font-medium">
							{ab ? row.arm : "Your agent"}
						</TableHead>
						<TableCell className="text-right tabular-nums">{rate(row.resumed_without_redo)}</TableCell>
						<TableCell className="text-right tabular-nums">{rate(row.graceful_finish)}</TableCell>
						<TableCell className="text-right tabular-nums">{perDollar(row.cost_normalized_success)}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}
