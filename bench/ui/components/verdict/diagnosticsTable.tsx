import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import type { DiagnosticsPayload, DiagnosticsRow, MetricDescriptor, Ratio, Value, ValueKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { MethodHint } from "./methodHint";
import { EFFICIENCY_ARM_METRICS, METER_GROUP_ORDER, formatCount, formatSignedPp, meterGroup, type MeterGroup } from "./verdict.utils";

export interface DiagnosticsTableProps {
	diagnostics: Pick<DiagnosticsPayload, "diagnostics" | "meters" | "arm_metrics">;
	/** Arm A then arm B. */
	arms: string[];
	/** Metric descriptors by name, from the catalog: labels and value kinds for the efficiency medians. */
	metrics?: Record<string, MetricDescriptor>;
	className?: string;
}

/** One row per condition that ran, then the honesty, safety and efficiency meters (DESIGN.md 9.6). */
export function DiagnosticsTable({ diagnostics, arms, metrics, className }: DiagnosticsTableProps) {
	const { t } = useTranslation("bench");
	return (
		<div className={cn("flex flex-col gap-6", className)}>
			{diagnostics.diagnostics.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="verdict-diagnostics-empty">
					{t("verdict.diagnostics.empty")}
				</p>
			) : (
				<ConditionTable rows={diagnostics.diagnostics} arms={arms} />
			)}
			<MeterTable diagnostics={diagnostics} arms={arms} metrics={metrics} />
		</div>
	);
}

function ConditionTable({ rows, arms }: { rows: DiagnosticsRow[]; arms: string[] }) {
	const { t } = useTranslation("bench");
	return (
		<Table stickyColumns={1} data-testid="verdict-diagnostics-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("verdict.diagnostics.condition")}</TableHead>
					<TableHead>{t("verdict.diagnostics.axis")}</TableHead>
					{arms.map((arm) => (
						<TableHead key={arm} className="text-right font-mono">
							{arm}
						</TableHead>
					))}
					<TableHead className="text-right">{t("verdict.diagnostics.releaseEffect")}</TableHead>
					<TableHead className="text-right">{t("verdict.diagnostics.tasks")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={row.condition} data-testid={`verdict-diagnostics-row-${row.condition}`} data-descriptive={row.descriptive}>
						<TableCell className="font-mono text-xs">{row.condition}</TableCell>
						<TableCell className="text-muted-foreground text-xs">{row.axis}</TableCell>
						{arms.map((arm) => (
							<TableCell key={arm} className="text-right">
								<RatioText ratio={row.per_arm[arm]} />
							</TableCell>
						))}
						<TableCell className="text-right">
							<EffectCell row={row} />
						</TableCell>
						<TableCell className="text-muted-foreground text-right text-xs tabular-nums">
							{t("verdict.diagnostics.tasksValue", { count: row.n_tasks })}
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function RatioText({ ratio }: { ratio: Ratio | undefined }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (!ratio) return <ValueCell value={undefined} kind="count" />;
	return (
		<span className="inline-flex items-center gap-1.5 tabular-nums">
			{t("verdict.naive.ratio", { num: formatCount(ratio.num, locale), den: formatCount(ratio.den, locale) })}
			{ratio.unknown > 0 && (
				<span className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1 text-[10px]">
					{t("verdict.naive.unknown", { count: ratio.unknown })}
				</span>
			)}
		</span>
	);
}

function EffectCell({ row }: { row: DiagnosticsRow }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const value = row.effect ? t("verdict.card.pp", { value: formatSignedPp(row.effect.d_bar, locale) }) : null;
	if (row.descriptive) {
		return (
			<span className="inline-flex items-center justify-end gap-2" data-testid={`verdict-diagnostics-effect-${row.condition}`}>
				{value && <span className="text-muted-foreground text-xs tabular-nums">{value}</span>}
				<span className="text-muted-foreground rounded-sm border border-dashed px-1 text-[10px] uppercase">
					{t("verdict.diagnostics.descriptive")}
				</span>
			</span>
		);
	}
	return (
		<span className="inline-flex items-center justify-end gap-2" data-testid={`verdict-diagnostics-effect-${row.condition}`}>
			<span className="font-semibold tabular-nums">{value ?? t("verdict.card.noEffect")}</span>
			<span className="border-primary/50 text-primary rounded-sm border px-1 text-[10px] uppercase">
				{t("verdict.diagnostics.primary")}
			</span>
		</span>
	);
}

interface MeterLine {
	key: string;
	group: MeterGroup;
	label: string;
	kind: ValueKind;
	perArm: Record<string, Value | undefined>;
	definition: string;
	method?: "costPerSuccess";
}

function MeterTable({ diagnostics, arms, metrics }: Omit<DiagnosticsTableProps, "className">) {
	const { t } = useTranslation("bench");

	const lines: MeterLine[] = diagnostics.meters.map((meter) => ({
		key: `meter-${meter.name}`,
		group: meterGroup(meter.name),
		label: meter.label,
		kind: meter.kind,
		perArm: meter.per_arm,
		definition: t("verdict.method.meter", { numerator: meter.numerator, denominator: meter.denominator }),
		method: meter.name === "cost_per_verified_success" ? "costPerSuccess" : undefined,
	}));
	for (const name of EFFICIENCY_ARM_METRICS) {
		if (!arms.some((arm) => diagnostics.arm_metrics[arm]?.[name])) continue;
		const descriptor = metrics?.[name];
		lines.push({
			key: `arm-${name}`,
			group: "efficiency",
			label: t("verdict.diagnostics.median", { label: descriptor?.label ?? name }),
			// Without a descriptor the kind is unknown; count prints the number plainly.
			kind: descriptor?.kind ?? "count",
			perArm: Object.fromEntries(arms.map((arm) => [arm, diagnostics.arm_metrics[arm]?.[name]])),
			definition: descriptor?.description ?? "",
		});
	}

	if (lines.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="verdict-meters-empty">
				{t("verdict.diagnostics.noMeters")}
			</p>
		);
	}

	const groups = METER_GROUP_ORDER.map((group) => ({ group, lines: lines.filter((line) => line.group === group) })).filter(
		(entry) => entry.lines.length > 0,
	);

	return (
		<Table stickyColumns={1} data-testid="verdict-meters-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("verdict.diagnostics.meters")}</TableHead>
					<TableHead>{t("verdict.diagnostics.meter")}</TableHead>
					{arms.map((arm) => (
						<TableHead key={arm} className="text-right font-mono">
							{arm}
						</TableHead>
					))}
					<TableHead>{t("verdict.diagnostics.definition")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{groups.flatMap(({ group, lines: groupLines }) =>
					groupLines.map((line, index) => (
						<TableRow key={line.key} data-testid={`verdict-meter-row-${line.key}`} data-group={group}>
							{index === 0 && (
								<TableCell rowSpan={groupLines.length} className="align-top text-xs font-medium">
									{t(`verdict.diagnostics.meterGroup.${group}`)}
								</TableCell>
							)}
							<TableCell className="text-xs">
								<span className="inline-flex items-center gap-1.5">
									{line.label}
									{line.method && <MethodHint topic={line.method} />}
								</span>
							</TableCell>
							{arms.map((arm) => (
								<TableCell key={arm} className="text-right">
									<ValueCell value={line.perArm[arm]} kind={line.kind} testId={`verdict-meter-${line.key}-${arm}`} />
								</TableCell>
							))}
							<TableCell className="text-muted-foreground max-w-xs text-xs whitespace-normal">{line.definition}</TableCell>
						</TableRow>
					)),
				)}
			</TableBody>
		</Table>
	);
}