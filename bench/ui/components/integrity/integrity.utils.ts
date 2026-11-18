import type { Decision, MonitorCertificationSummary, OperatingPoint } from "@/lib/types/bench";

/** A directional A/A result: two identical arms differed, so the harness moved the number. */
export function isHarnessNoise(decision: Decision | undefined): boolean {
	return decision === "improved" || decision === "regressed";
}

/**
 * The symmetric percentage-point domain of the noise band and a position function mapping a pp
 * value to 0-100 %. The domain covers ±MDE and every marker (fractions), with headroom, and is at
 * least 5 pp so a tiny MDE still reads as a band.
 */
export function bandScale(mdePp: number, markers: (number | undefined)[]): { domain: number; pos: (pp: number) => number } {
	let extent = Number.isFinite(mdePp) ? Math.abs(mdePp) : 0;
	for (const m of markers) {
		if (typeof m === "number" && Number.isFinite(m)) extent = Math.max(extent, Math.abs(m * 100));
	}
	const domain = Math.max(5, Math.ceil(extent * 1.25));
	const pos = (pp: number) => Math.min(100, Math.max(0, ((pp + domain) / (2 * domain)) * 100));
	return { domain, pos };
}

/** Whether the release effect (a fraction) sits inside ±MDE (pp); null when either is unknown. */
export function insideBand(dBar: number | undefined, mdePp: number): boolean | null {
	if (typeof dBar !== "number" || !Number.isFinite(dBar) || !(mdePp > 0)) return null;
	return Math.abs(dBar * 100) < mdePp;
}

/** A rate in [0, 1] as a percentage with up to one decimal: 0.786 -> "78.6%"; null (no runs to rate) -> "-". */
export function formatRate(rate: number | null, locale: string): string {
	if (rate === null || !Number.isFinite(rate)) return "-";
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 1 }).format(rate);
}

/** A false-positive rate with enough decimals for 0.1%: 0.001 -> "0.1%"; null -> "-". */
export function formatFpr(rate: number | null, locale: string): string {
	if (rate === null || !Number.isFinite(rate)) return "-";
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 3 }).format(rate);
}

/** An interval of rates as "[44.0%, 97.0%]", or "" when absent. */
export function formatInterval(iv: [number, number] | undefined, locale: string): string {
	if (!iv) return "";
	return `[${formatRate(iv[0], locale)}, ${formatRate(iv[1], locale)}]`;
}

/** The operating point a list row headlines: the 1% target when present, else the first. */
export function headlinePoint(points: OperatingPoint[]): OperatingPoint | undefined {
	return points.find((p) => p.target_fpr === 0.01) ?? points[0];
}

/** Certifications that replayed one experiment, newest first. */
export function certificationsFor(certs: MonitorCertificationSummary[], expId: string): MonitorCertificationSummary[] {
	return certs.filter((c) => c.exp_ids.includes(expId)).sort((a, b) => b.created_at.localeCompare(a.created_at));
}

/**
 * Splits an excerpt around the first case-insensitive match of the phrase, so the matched words
 * can be marked. A phrase that does not occur leaves the excerpt whole.
 */
export function splitOnPhrase(excerpt: string, phrase: string): { before: string; match: string; after: string } {
	const at = phrase ? excerpt.toLowerCase().indexOf(phrase.toLowerCase()) : -1;
	if (at < 0) return { before: excerpt, match: "", after: "" };
	return { before: excerpt.slice(0, at), match: excerpt.slice(at, at + phrase.length), after: excerpt.slice(at + phrase.length) };
}