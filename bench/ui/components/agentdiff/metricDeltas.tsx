import { EmptyNote } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TONE_TEXT } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { AgentDiffGate, AgentDiffMetric } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { BellRing } from "lucide-react";
import { useTranslation } from "react-i18next";
import { DIRECTION_TONE, formatMetric, formatSigned, testIdPart } from "./agentDiff.utils";

/** Paired metric means of A and B with B minus A, coloured by what the metric's direction makes of it. */
export function MetricDeltasTable({ metrics }: { metrics: AgentDiffMetric[] }) {
	const { t } = useTranslation("bench");
	if (metrics.length === 0) return <EmptyNote testId="agentdiff-metrics-empty">{t("agentDiff.metrics.empty")}</EmptyNote>;
	return (
		<div className="min-w-0 rounded-sm border">
			<Table data-testid="agentdiff-metrics-table" aria-label={t("agentDiff.metrics.title")} containerClassName="rounded-sm">
				<TableHeader>
					<TableRow className="hover:bg-transparent">
						<TableHead>{t("agentDiff.metrics.metric")}</TableHead>
						<TableHead className="text-right">{t("agentDiff.metrics.a")}</TableHead>
						<TableHead className="text-right">{t("agentDiff.metrics.b")}</TableHead>
						<TableHead className="text-right">{t("agentDiff.metrics.delta")}</TableHead>
						<TableHead>{t("agentDiff.metrics.direction")}</TableHead>
						<TableHead className="text-right">{t("agentDiff.metrics.tasks")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{metrics.map((metric) => (
						<MetricRow key={metric.metric} metric={metric} />
					))}
				</TableBody>
			</Table>
		</div>
	);
}

function MetricRow({ metric }: { metric: AgentDiffMetric }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const tone = DIRECTION_TONE[metric.direction] ?? "neutral";
	const unknownCounts = metric.unknown_a > 0 || metric.unknown_b > 0;
	return (
		<TableRow data-testid={`agentdiff-metric-${testIdPart(metric.metric)}`} data-direction={metric.direction}>
			<TableCell className="text-xs">
				<span className="text-foreground font-mono">{metric.metric}</span>
				<span className="text-muted-foreground block text-[11px]">
					{t(metric.higher_is_better ? "agentDiff.metrics.higher" : "agentDiff.metrics.lower")}
				</span>
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				<MetricNumber value={metric.a} format={(v) => formatMetric(v, locale)} />
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				<MetricNumber value={metric.b} format={(v) => formatMetric(v, locale)} />
			</TableCell>
			<TableCell className={cn("text-right text-xs font-semibold tabular-nums", TONE_TEXT[tone])}>
				<MetricNumber value={metric.delta} format={(v) => formatSigned(v, locale)} />
			</TableCell>
			<TableCell>
				<ToneBadge tone={tone}>{t(`agentDiff.metrics.dir.${metric.direction}`, { defaultValue: metric.direction })}</ToneBadge>
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{metric.tasks}
				{unknownCounts && (
					<span className="text-muted-foreground block text-[11px]">
						{t("agentDiff.metrics.unknownCounts", { a: metric.unknown_a, b: metric.unknown_b })}
					</span>
				)}
			</TableCell>
		</TableRow>
	);
}

function MetricNumber({ value, format }: { value: number | null; format: (v: number) => string }) {
	const { t } = useTranslation("bench");
	if (value === null) {
		return (
			<span className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1 font-normal">
				{t("agentDiff.metrics.unknown")}
			</span>
		);
	}
	return <span>{format(value)}</span>;
}

/** Gates that fired in either version; the ones new in B come first and say on which tasks. */
export function GateChanges({ gates }: { gates: AgentDiffGate[] }) {
	const { t } = useTranslation("bench");
	if (gates.length === 0) return <EmptyNote testId="agentdiff-gates-empty">{t("agentDiff.gates.empty")}</EmptyNote>;
	const ordered = [...gates].sort((x, y) => Number(y.new) - Number(x.new));
	return (
		<ul className="flex min-w-0 flex-col gap-2" data-testid="agentdiff-gates">
			{ordered.map((gate) => (
				<li
					key={gate.gate}
					className="bg-card flex min-w-0 flex-col gap-1 rounded-sm border px-3 py-2"
					data-testid={`agentdiff-gate-${testIdPart(gate.gate)}`}
					data-new={gate.new}
				>
					<div className="flex min-w-0 flex-wrap items-center gap-2">
						<span className="text-foreground font-mono text-xs">{gate.gate}</span>
						{gate.new && (
							<ToneBadge tone="bad" icon={BellRing}>
								{t("agentDiff.gates.new")}
							</ToneBadge>
						)}
						<span className="text-muted-foreground text-xs tabular-nums">
							{t("agentDiff.gates.counts", { a: gate.fired_a, b: gate.fired_b })}
						</span>
					</div>
					{(gate.tasks ?? []).length > 0 && (
						<p className="text-muted-foreground font-mono text-[11px] break-all">
							{t("agentDiff.gates.tasks", { list: gate.tasks.join(", ") })}
						</p>
					)}
				</li>
			))}
		</ul>
	);
}