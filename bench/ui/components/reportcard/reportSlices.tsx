import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report, ReportMetrics, ReportSlice } from "@/lib/store/apis/evaluationsApi";
import { cn } from "@/lib/utils";
import { Filter, X } from "lucide-react";
import * as React from "react";
import { armName, isAB, pct, points } from "./reportCard.utils";

/** The server narrows by at most this many key:value filters. */
export const MAX_WHERE = 4;

/** "tier = enterprise and region = eu" for a where filter; "" when there is none. */
export function whereText(where: Record<string, string> | undefined): string {
	return Object.entries(where ?? {})
		.map(([k, v]) => `${k} = ${v}`)
		.join(" and ");
}

/** The segment keys worth showing: at least one value. */
export function visibleSlices(report: Report): ReportSlice[] {
	return (report.slices ?? []).filter((s) => s.values.length > 0);
}

/** Shown at the top of a narrowed report card: what the filter is and a way to clear it. */
export function ReportFilterBar({ where, onClear }: { where?: Record<string, string>; onClear?: () => void }) {
	const text = whereText(where);
	if (!text) return null;
	return (
		<div data-testid="report-filter" className="bg-muted/50 flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
			<Filter className="text-muted-foreground size-4" aria-hidden="true" />
			<span>
				Showing tasks with <span className="font-medium">{text}</span>
			</span>
			{onClear && (
				<Button type="button" size="sm" variant="ghost" className="ml-auto" onClick={onClear} data-testid="report-filter-clear">
					<X className="size-4" aria-hidden="true" />
					Show all tasks
				</Button>
			)}
		</div>
	);
}

function SliceScore({ metrics }: { metrics?: ReportMetrics }) {
	if (!metrics) return <span className="text-muted-foreground">-</span>;
	return (
		<span className="inline-flex items-center gap-2">
			<span className="text-foreground font-medium tabular-nums">{pct(metrics.score)}</span>
			<span className="text-muted-foreground text-xs tabular-nums">
				<span className="sr-only">95% range </span>
				{pct(metrics.ci_low)}-{pct(metrics.ci_high)}
			</span>
		</span>
	);
}

export interface ReportSlicesProps {
	report: Report;
	/** Narrows the whole report card to one segment; no button when absent (shared view). */
	onShowOnly?: (key: string, value: string) => void;
}

/** Scores per task segment, one table per metadata key. Hidden when the report has no segments. */
export function ReportSlices({ report, onShowOnly }: ReportSlicesProps) {
	const slices = visibleSlices(report);
	const [picked, setPicked] = React.useState(0);
	if (slices.length === 0) return null;
	const slice = slices[Math.min(picked, slices.length - 1)];
	const ab = isAB(report);
	const labels = report.arms.map((a) => a.label);
	const filtered = report.where ?? {};
	return (
		<Section
			title="By segment"
			description="Tasks grouped by their metadata. Score is the share of runs solved, with the 95% confidence range. Segments with fewer than 3 tasks are shown but never ranked."
		>
			<div data-testid="report-slices" className="flex min-w-0 flex-col gap-3">
				{slices.length > 1 && (
					<div role="group" aria-label="Segment by" className="flex flex-wrap gap-1">
						{slices.map((s, i) => (
							<Button
								key={s.key}
								type="button"
								size="sm"
								variant={s === slice ? "secondary" : "ghost"}
								aria-pressed={s === slice}
								onClick={() => setPicked(i)}
								data-testid={`slice-key-${s.key}`}
							>
								{s.key}
							</Button>
						))}
					</div>
				)}
				<Table aria-label={`Score by ${slice.key}`}>
					<TableHeader>
						<TableRow>
							<TableHead scope="col">{slice.key}</TableHead>
							<TableHead scope="col" className="text-right">
								Tasks
							</TableHead>
							{labels.map((l) => (
								<TableHead key={l} scope="col">
									{ab ? `${armName(report, l)} score` : "Score"}
								</TableHead>
							))}
							{ab && (
								<TableHead scope="col" className="text-right">
									Change
								</TableHead>
							)}
							{onShowOnly && (
								<TableHead scope="col">
									<span className="sr-only">Actions</span>
								</TableHead>
							)}
						</TableRow>
					</TableHeader>
					<TableBody>
						{slice.values.map((v) => {
							const active = filtered[slice.key] === v.value;
							return (
								<TableRow key={v.value} data-testid={`slice-${slice.key}-${v.value}`} className={cn(v.small && "text-muted-foreground")}>
									<TableHead scope="row" className="font-medium">
										<span className="flex flex-col gap-0.5">
											<span>{v.value}</span>
											{v.small && <span className="text-xs font-normal">Too few tasks to rank</span>}
										</span>
									</TableHead>
									<TableCell className="text-right tabular-nums">{v.tasks}</TableCell>
									{labels.map((l) => (
										<TableCell key={l}>
											<span className="flex flex-wrap items-center gap-2">
												<SliceScore metrics={v.arms[l]} />
												{!v.small && slice.worst[l] === v.value && (
													<Badge variant="warning" data-testid={`slice-lowest-${l}`}>
														Lowest
													</Badge>
												)}
											</span>
										</TableCell>
									))}
									{ab && <TableCell className="text-right tabular-nums">{v.delta === undefined ? "-" : points(v.delta)}</TableCell>}
									{onShowOnly && (
										<TableCell className="text-right">
											{!active && (slice.key in filtered || Object.keys(filtered).length < MAX_WHERE) && (
												<Button type="button" size="sm" variant="outline" onClick={() => onShowOnly(slice.key, v.value)} data-testid={`slice-only-${v.value}`}>
													Show only this segment
												</Button>
											)}
										</TableCell>
									)}
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
				{slice.other ? (
					<p className="text-muted-foreground text-xs" data-testid="slice-other">
						{slice.other} more {slice.other === 1 ? "value" : "values"} not listed.
					</p>
				) : null}
			</div>
		</Section>
	);
}
