import type { TFunction } from "i18next";
import { RECEIPT_STEPS, type Catalog, type ProbeLayerName, type ProbeSpec, type Ratio, type ReceiptStep } from "@/lib/types/bench";

/** Steps each probe layer applies (DESIGN.md 10.3). Any other step is not_applicable for that layer, never blank. */
export const LAYER_STEPS: Partial<Record<ProbeLayerName, readonly ReceiptStep[]>> = {
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
	const steps = layer ? LAYER_STEPS[layer] : undefined;
	if (steps) return steps.includes(step);
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
/**
 * The readable name of a probe layer. A layer this UI has no translation for (a newer server)
 * shows its own name with spaces, never the internal translation key.
 */
export function probeLayerLabel(layer: string, t: TFunction<"bench">): string {
	return KNOWN_LAYERS.has(layer) ? t(`receipts.layers.${layer as ProbeLayerName}`) : layer.replace(/_/g, " ");
}

const KNOWN_LAYERS: ReadonlySet<string> = new Set<ProbeLayerName>(["fixture", "gateway", "tool_shim", "adapter", "a2a", "tool", "service"]);

/**
 * The catalog probe a condition of an experiment on suite ran. The catalog flattens probes across
 * suites, so the experiment's own suite (id or id@version) decides the layer when two suites share
 * a probe id; without a matching suite only an unambiguous id is used.
 */
export function catalogProbeFor(catalog: Catalog | undefined, suite: string | undefined, condition: string): ProbeSpec | undefined {
	const candidates = catalog?.probes.filter((p) => p.id === condition) ?? [];
	const own = suite ? catalog?.suites.find((s) => `${s.id}@${s.version}` === suite || s.id === suite) : undefined;
	const layer = own?.probe_layers?.[condition];
	if (layer) return candidates.find((p) => p.layer === layer);
	return candidates.length === 1 ? candidates[0] : undefined;
}
