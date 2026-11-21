import { clipText } from "@/components/runs/stepTimeline.utils";
import { Input } from "@/components/ui/input";
import { useGetRunStepsQuery } from "@/lib/store/apis/runStepsApi";
import type { StepRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { alignTraces, type DiffRow } from "./debug.utils";

/**
 * Two runs' traces side by side, aligned by model call: same rows are quiet, differing rows are
 * marked, and the first difference is named. The other run defaults to otherRunId and can be changed.
 */
export function TraceDiff({ runId, otherRunId }: { runId: string; otherRunId?: string }) {
	const { t } = useTranslation("runDebug");
	const [other, setOther] = useState(otherRunId ?? "");
	const left = useGetRunStepsQuery(runId);
	const right = useGetRunStepsQuery(other, { skip: !other });
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid="trace-diff">
			<div className="flex flex-wrap items-end gap-3">
				<div className="flex flex-col gap-0.5">
					<h3 className="text-sm font-semibold">{t("diff.title")}</h3>
					<p className="text-muted-foreground text-xs">{t("diff.description")}</p>
				</div>
				<label className="ml-auto flex flex-col gap-1 text-xs font-medium">
					{t("diff.pick")}
					<Input value={other} onChange={(e) => setOther(e.target.value.trim())} className="h-8 w-48 font-mono text-xs" data-testid="trace-diff-other" />
				</label>
			</div>
			{right.error ? (
				<p className="text-chart-error-ink text-xs">{t("diff.loadError")}</p>
			) : left.data && right.data ? (
				<DiffTable left={left.data.steps} right={right.data.steps} />
			) : null}
		</section>
	);
}

export function DiffTable({ left, right }: { left: readonly StepRecord[]; right: readonly StepRecord[] }) {
	const { t } = useTranslation("runDebug");
	const { rows, firstDiff } = useMemo(() => alignTraces(left, right), [left, right]);
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<p className={cn("text-xs", firstDiff === null ? "text-muted-foreground" : "text-chart-error-ink font-medium")} data-testid="trace-diff-first">
				{firstDiff === null ? t("diff.noDiff") : t("diff.firstDiff", { call: firstDiff })}
			</p>
			<div className="overflow-x-auto rounded-xl border">
				<table className="w-full min-w-[36rem] table-fixed text-xs">
					<thead className="bg-muted/40 text-muted-foreground">
						<tr>
							<th className="w-20 px-3 py-2 text-left font-medium" />
							<th className="px-3 py-2 text-left font-medium">{t("diff.left")}</th>
							<th className="px-3 py-2 text-left font-medium">{t("diff.right")}</th>
						</tr>
					</thead>
					<tbody className="divide-y">
						{rows.map((row) => (
							<DiffTableRow key={row.turn} row={row} />
						))}
					</tbody>
				</table>
			</div>
		</div>
	);
}

function DiffTableRow({ row }: { row: DiffRow }) {
	const { t } = useTranslation("runDebug");
	return (
		<tr className={cn(!row.same && "bg-chart-error/5")} data-testid={`trace-diff-row-${row.turn}`} data-same={row.same}>
			<td className="px-3 py-2 align-top">
				<span className="font-mono">{t("diff.turn", { n: row.turn })}</span>
				{!row.same && <span className="text-chart-error-ink block text-[11px]">{t("diff.differs")}</span>}
			</td>
			<DiffCell steps={row.left} />
			<DiffCell steps={row.right} />
		</tr>
	);
}

function DiffCell({ steps }: { steps: readonly StepRecord[] }) {
	const { t } = useTranslation("runDebug");
	if (steps.length === 0) return <td className="text-muted-foreground px-3 py-2 align-top italic">{t("diff.missing")}</td>;
	return (
		<td className="min-w-0 px-3 py-2 align-top">
			<ul className="flex flex-col gap-1">
				{steps.map((s) => (
					<li key={s.seq} className="min-w-0 truncate" title={clipText(s.summary ?? "", 400)}>
						<span className="font-mono">{s.kind === "tool_request" ? clipText(s.tool_name ?? "", 40) : s.kind}</span>
						{s.summary && <span className="text-muted-foreground"> {clipText(s.summary, 80)}</span>}
					</li>
				))}
			</ul>
		</td>
	);
}
