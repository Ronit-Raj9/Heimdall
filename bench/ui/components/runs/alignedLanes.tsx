import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AlignedStep, BenchEvent, FirstDivergence } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { GitFork } from "lucide-react";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";

export interface AlignedLanesProps {
	alignment: readonly AlignedStep[];
	/** Lane order, for example ["A", "B"]. */
	arms: readonly string[];
	/** Each arm's events by id. Undefined while that arm's events load: cells then show the bare id. */
	eventsByArm: Record<string, Map<string, BenchEvent> | undefined>;
	firstDivergence: FirstDivergence | null;
}

/**
 * The A and B lanes, one row per AlignedStep (matched on event type and target, DESIGN.md 11).
 * The first observed divergence gets a marker row that says it is an observation, never a root
 * cause. Event text is data and renders as text only.
 */
export function AlignedLanes({ alignment, arms, eventsByArm, firstDivergence }: AlignedLanesProps) {
	const { t } = useTranslation("bench");
	const divergenceIndex = firstDivergence?.index ?? null;

	return (
		<Table data-testid="aligned-lanes" className="table-fixed">
			<colgroup>
				<col className="w-12" />
				<col className="w-56" />
				{arms.map((arm) => (
					<col key={`col-${arm}`} />
				))}
			</colgroup>
			<TableHeader>
				<TableRow>
					<TableHead className="px-3">{t("runs.lanes.index")}</TableHead>
					<TableHead className="px-3">{t("runs.lanes.step")}</TableHead>
					{arms.map((arm) => (
						<TableHead key={`head-${arm}`} className="px-3" data-testid={`aligned-lanes-head-${arm}`}>
							{t("runs.arm", { arm })}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{alignment.map((step) => {
					const isFirst = step.index === divergenceIndex;
					return (
						<Fragment key={`step-${step.index}`}>
							{isFirst && <DivergenceMarker note={firstDivergence?.note} colSpan={2 + arms.length} />}
							<TableRow
								data-testid={`aligned-step-${step.index}`}
								data-diverged={step.diverged ? "true" : "false"}
								data-first-divergence={isFirst ? "true" : undefined}
								className={cn("align-top", step.diverged && "bg-chart-warning/5", isFirst && "border-l-chart-warning border-l-2")}
							>
								<TableCell className="text-muted-foreground px-3 align-top font-mono text-xs tabular-nums">{step.index}</TableCell>
								<TableCell className="px-3 align-top whitespace-normal">
									<div className="flex min-w-0 flex-col gap-0.5">
										<span className="text-muted-foreground font-mono text-[11px]">{step.type}</span>
										<span className="font-mono text-xs break-all">{step.target}</span>
										{step.diverged && <span className="text-chart-warning-ink text-[11px] font-medium">{t("runs.lanes.diverged")}</span>}
									</div>
								</TableCell>
								{arms.map((arm) => {
									const id = step.events[arm] ?? null;
									return (
										<TableCell
											key={`cell-${arm}`}
											className="px-3 align-top whitespace-normal"
											data-testid={`aligned-step-${step.index}-${arm}`}
										>
											<EventCell id={id} event={id ? eventsByArm[arm]?.get(id) : undefined} loaded={Boolean(eventsByArm[arm])} />
										</TableCell>
									);
								})}
							</TableRow>
						</Fragment>
					);
				})}
			</TableBody>
		</Table>
	);
}

function DivergenceMarker({ note, colSpan }: { note: string | undefined; colSpan: number }) {
	const { t } = useTranslation("bench");
	return (
		<TableRow data-testid="first-divergence-marker" className="hover:bg-transparent">
			<TableCell colSpan={colSpan} className="border-l-chart-warning bg-chart-warning/10 border-l-2 px-3 py-2 whitespace-normal">
				<div className="flex flex-wrap items-center gap-2">
					<GitFork className="text-chart-warning-ink size-4" aria-hidden="true" />
					<span className="text-foreground text-sm font-semibold">{t("runs.lanes.divergence")}</span>
					<span
						className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1.5 py-0.5 text-[11px] font-medium"
						data-testid="first-divergence-label"
					>
						{t("runs.lanes.divergenceHint")}
					</span>
				</div>
				{note && (
					<p className="text-muted-foreground mt-1 text-xs whitespace-pre-wrap" data-testid="first-divergence-note">
						{note}
					</p>
				)}
			</TableCell>
		</TableRow>
	);
}

function EventCell({ id, event, loaded }: { id: string | null; event: BenchEvent | undefined; loaded: boolean }) {
	const { t } = useTranslation("bench");
	if (!id) {
		return (
			<span className="text-muted-foreground text-xs italic" data-state="empty">
				{t("runs.lanes.noEvent")}
			</span>
		);
	}
	if (!event) {
		return (
			<span className="text-muted-foreground font-mono text-[11px] break-all" title={loaded ? t("runs.lanes.notInLog") : undefined}>
				{id}
			</span>
		);
	}
	const text = event.summary || event.target || event.model_served || event.tool_name || "";
	return (
		<div className="flex min-w-0 flex-col gap-0.5" data-event-id={event.event_id}>
			<span className="line-clamp-3 text-xs break-words whitespace-pre-wrap">{text}</span>
			<span className="text-muted-foreground flex flex-wrap items-center gap-x-1.5 font-mono text-[11px]">
				{event.tool_name && <span>{event.tool_name}</span>}
				<span>
					{event.source}:{event.seq}
				</span>
				{event.is_error && <span className="text-chart-error-ink font-sans font-medium">{t("runs.lanes.error")}</span>}
			</span>
		</div>
	);
}