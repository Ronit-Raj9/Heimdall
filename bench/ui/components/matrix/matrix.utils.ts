import { P_VALUE_FLOOR, formatPValue, type Tone } from "@/components/verdict/verdict.utils";
import type { MatrixPair } from "@/lib/types/bench";

// Pure helpers for the model matrix screen (GET /experiments/{id}/matrix, docs/model-matrix.md),
// tested without rendering.

/** One cell of the pairwise grid: the pair tested, and the success difference read as row minus column. */
export interface PairCell {
	pair: MatrixPair;
	/** Mean over common tasks of the row arm's success rate minus the column arm's, a fraction. */
	diff: number;
}

/** Looks up pairs by both orders of their arms. The server sends each pair once. */
export function pairIndex(pairs: readonly MatrixPair[] | null | undefined): (rowArm: string, colArm: string) => PairCell | null {
	const byKey = new Map<string, MatrixPair>();
	for (const pair of pairs ?? []) byKey.set(`${pair.arm_x}\u0000${pair.arm_y}`, pair);
	return (rowArm, colArm) => {
		const direct = byKey.get(`${rowArm}\u0000${colArm}`);
		if (direct) return { pair: direct, diff: direct.mean_diff };
		const reversed = byKey.get(`${colArm}\u0000${rowArm}`);
		return reversed ? { pair: reversed, diff: -reversed.mean_diff } : null;
	};
}

/** Significant cells are coloured by direction from the row's side; everything else stays neutral. */
export function pairTone(cell: PairCell): Tone {
	if (!cell.pair.significant) return "neutral";
	if (cell.diff > 0) return "good";
	if (cell.diff < 0) return "bad";
	return "neutral";
}

/** A p-value for a grid cell: up to four decimals, "< 0.0001" below the floor, null when there was no test. */
export function formatCellP(p: number | null | undefined, locale: string): string | null {
	if (typeof p !== "number" || !Number.isFinite(p)) return null;
	if (p > 0 && p < P_VALUE_FLOOR) return `< ${formatPValue(P_VALUE_FLOOR, locale)}`;
	return formatPValue(p, locale);
}