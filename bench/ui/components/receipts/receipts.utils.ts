import { RECEIPT_STEPS, type ProbeLayerName, type Ratio, type ReceiptStep } from "@/lib/types/bench";

/** Steps each probe layer applies (DESIGN.md 10.3). Any other step is not_applicable for that layer, never blank. */
export const LAYER_STEPS: Record<ProbeLayerName, readonly ReceiptStep[]> = {
	fixture: ["assigned", "armed", "visible", "acknowledged", "effect"],
	tool_shim: ["assigned", "armed", "fired", "visible", "acknowledged", "effect"],
	gateway: RECEIPT_STEPS,
};

export type ArmRatios = Record<string, Partial<Record<ReceiptStep, Ratio>>>;

/**
 * Whether a step applies. The layer decides when it is known. Without a layer, a step no arm
 * recorded a ratio for is treated as not applicable.
 */
export function stepApplies(step: ReceiptStep, layer: ProbeLayerName | null | undefined, ratios: ArmRatios | undefined): boolean {
	if (layer && layer in LAYER_STEPS) return LAYER_STEPS[layer].includes(step);
	return Object.values(ratios ?? {}).some((byStep) => byStep[step] !== undefined);
}

export interface RatioShares {
	/** Fraction of den that reached the step, 0..1. */
	yes: number;
	/** Fraction of den that is unknown, 0..1. */
	unknown: number;
}

/** Bar widths for a ratio. Unknowns are their own share, never counted as reached or as not reached. */
export function ratioShares(ratio: Ratio | undefined): RatioShares | null {
	if (!ratio || ratio.den <= 0) return null;
	const clamp = (x: number) => Math.min(1, Math.max(0, x));
	const yes = clamp(ratio.num / ratio.den);
	return { yes, unknown: clamp(Math.min(ratio.unknown / ratio.den, 1 - yes)) };
}