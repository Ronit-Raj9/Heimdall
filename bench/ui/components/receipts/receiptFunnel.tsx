import { RECEIPT_STEPS, type ProbeLayerName, type ReceiptStep } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { ratioShares, stepApplies, type ArmRatios } from "./receipts.utils";

export interface ReceiptFunnelProps {
	/** The probe's layer from the catalog; null when the catalog does not list the probe. */
	layer: ProbeLayerName | null;
	arms: readonly string[];
	/** arm -> step -> yes over applicable, from the diagnostics row's `receipts`. */
	ratios: ArmRatios;
}

/**
 * The R0 to R7 receipt funnel of one probe condition, one bar per arm and step (DESIGN.md 10.3).
 * A step the layer does not apply is shown greyed as n/a, never left out.
 */
export function ReceiptFunnel({ layer, arms, ratios }: ReceiptFunnelProps) {
	const { t } = useTranslation("bench");
	const columns = gridColumns(arms.length);
	return (
		<div className="overflow-x-auto">
			<div className="flex min-w-fit flex-col" role="table" data-testid="receipt-funnel" data-layer={layer ?? "unknown"}>
				<div role="row" style={columns} className="text-muted-foreground grid gap-3 border-b px-3 py-2 text-xs font-medium">
					<span role="columnheader">{t("receipts.funnel.step")}</span>
					{arms.map((arm) => (
						<span role="columnheader" key={`head-${arm}`}>
							{t("receipts.funnel.arm", { arm })}
						</span>
					))}
				</div>
				{RECEIPT_STEPS.map((step, index) => (
					<FunnelRow key={step} step={step} index={index} layer={layer} arms={arms} ratios={ratios} />
				))}
			</div>
		</div>
	);
}

// One label column, then one column per arm. Inline because the arm count comes from the data.
function gridColumns(armCount: number): React.CSSProperties {
	return { gridTemplateColumns: `minmax(9rem, 14rem) repeat(${Math.max(armCount, 1)}, minmax(8rem, 1fr))` };
}

function FunnelRow({
	step,
	index,
	layer,
	arms,
	ratios,
}: {
	step: ReceiptStep;
	index: number;
	layer: ProbeLayerName | null;
	arms: readonly string[];
	ratios: ArmRatios;
}) {
	const { t } = useTranslation("bench");
	const applies = stepApplies(step, layer, ratios);
	const naHint = layer
		? t("receipts.funnel.notApplicableHint", { layer: t(`receipts.layers.${layer}`) })
		: t("receipts.funnel.notApplicableNoLayer");

	return (
		<div
			role="row"
			data-testid={`receipt-step-${step}`}
			data-state={applies ? "applicable" : "not_applicable"}
			style={gridColumns(arms.length)}
			className={cn("grid items-center gap-3 border-b px-3 py-2 last:border-b-0", !applies && "bg-muted/40 text-muted-foreground")}
		>
			<div role="rowheader" className="flex min-w-0 flex-col">
				<span className={cn("text-sm font-medium", !applies && "text-muted-foreground font-normal")}>
					<span className="text-muted-foreground mr-2 font-mono text-[11px]">{t("receipts.stepCode", { index })}</span>
					{t(`receipts.steps.${step}`)}
				</span>
				<span className="text-muted-foreground truncate text-[11px]" title={t(`receipts.stepHints.${step}`)}>
					{applies ? t(`receipts.stepHints.${step}`) : naHint}
				</span>
			</div>
			{arms.map((arm) =>
				applies ? (
					<RatioBar key={`${step}-${arm}`} step={step} arm={arm} ratios={ratios} />
				) : (
					<span
						role="cell"
						key={`${step}-${arm}`}
						data-testid={`receipt-step-${step}-${arm}`}
						data-state="not_applicable"
						title={naHint}
						aria-label={naHint}
						className="text-muted-foreground/80 text-xs italic"
					>
						{t("receipts.funnel.notApplicable")}
					</span>
				),
			)}
		</div>
	);
}

function RatioBar({ step, arm, ratios }: { step: ReceiptStep; arm: string; ratios: ArmRatios }) {
	const { t } = useTranslation("bench");
	const ratio = ratios[arm]?.[step];
	const shares = ratioShares(ratio);
	if (!ratio || !shares) {
		return (
			<span role="cell" data-testid={`receipt-step-${step}-${arm}`} data-state="no_data" className="text-muted-foreground text-xs">
				{t("receipts.funnel.noData")}
			</span>
		);
	}
	return (
		<div role="cell" className="flex min-w-0 flex-col gap-1" data-testid={`receipt-step-${step}-${arm}`} data-state="applicable">
			<div className="bg-chart-track relative h-2 w-full overflow-hidden rounded-full" aria-hidden="true">
				<div className="bg-chart-seq-2 absolute inset-y-0 left-0 rounded-full" style={{ width: `${shares.yes * 100}%` }} />
				{shares.unknown > 0 && (
					<div
						className="bench-hatched absolute inset-y-0 border-x border-dashed"
						style={{ left: `${shares.yes * 100}%`, width: `${shares.unknown * 100}%` }}
					/>
				)}
			</div>
			<span className="flex items-baseline gap-2 text-xs">
				<span className="font-medium tabular-nums">{t("receipts.funnel.ratio", { num: ratio.num, den: ratio.den })}</span>
				{ratio.unknown > 0 && (
					<span className="text-muted-foreground bench-hatched rounded-sm px-1 text-[11px] italic">
						{t("receipts.funnel.unknown", { count: ratio.unknown })}
					</span>
				)}
			</span>
		</div>
	);
}