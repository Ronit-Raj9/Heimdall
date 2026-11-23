/**
 * The one money format of the UI (fix wave 3, N190 and N207): "-" when there is no amount, "$0"
 * for zero, 4 decimals under $0.01 so a cent-sized run cost never reads "$0.00", 2 decimals under
 * $10, and whole dollars with separators from $10. The server's twin is `formatUSD` in
 * `handlers/evaluationbudget.go`.
 */
export function formatUsd(v: number | null | undefined): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return "-";
	if (v === 0) return "$0";
	const sign = v < 0 ? "-" : "";
	const a = Math.abs(v);
	if (a < 0.01) return `${sign}$${a.toFixed(4)}`;
	if (a < 10) return `${sign}$${a.toFixed(2)}`;
	return `${sign}$${Math.round(a).toLocaleString("en-US")}`;
}