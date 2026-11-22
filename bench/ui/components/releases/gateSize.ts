// Smallest detectable change for a gate (G4-07), the same formula the server uses
// (stats.MDE: two-sided alpha 0.05, power 0.8, per-task SD 0.30, t quantiles).

const SD = 0.3;
const Z_ALPHA = 1.959964; // two-sided 0.05
const Z_POWER = 0.841621; // power 0.8

/** Student t quantile from the normal one (Cornish-Fisher, two terms): within a few percent from 4 degrees of freedom. */
function tq(z: number, df: number): number {
	const z3 = z ** 3;
	const z5 = z ** 5;
	return z + (z3 + z) / (4 * df) + (5 * z5 + 16 * z3 + 3 * z) / (96 * df * df);
}

/** Smallest change (fraction, 0.35 is 35 points) n paired tasks can detect; Infinity below two. */
export function mde(n: number): number {
	if (n < 2) return Infinity;
	const df = n - 1;
	return ((tq(Z_ALPHA, df) + tq(Z_POWER, df)) * SD) / Math.sqrt(n);
}

/** Fewest paired tasks that can detect a change of size (fraction); 0 when more than max. */
export function tasksFor(change: number, max = 100000): number {
	const goal = Math.abs(change);
	if (!(goal > 0)) return 0;
	let lo = 2;
	let hi = max;
	if (mde(hi) > goal) return 0;
	while (lo < hi) {
		const mid = Math.floor((lo + hi) / 2);
		if (mde(mid) <= goal) hi = mid;
		else lo = mid + 1;
	}
	return lo;
}
