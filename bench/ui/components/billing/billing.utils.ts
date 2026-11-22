import type { MeterView, Plan } from "@/lib/types/billing";

/** Bytes in plain units: 0 B, 12.3 KB, 4.5 GB (powers of 1024). */
export function formatBytes(n: number): string {
	if (!Number.isFinite(n) || n <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	let i = 0;
	let v = n;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${i === 0 ? Math.round(v) : v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/** A whole count with thousands separators. */
export function formatWhole(n: number): string {
	return Math.round(n).toLocaleString("en-US");
}

/** A meter's value in its unit. */
export function formatMeterValue(unit: MeterView["unit"], n: number): string {
	return unit === "bytes" ? formatBytes(n) : formatWhole(n);
}

/** How full a meter is, 0 to 100; 0 when it has no limit. */
export function meterPercent(m: Pick<MeterView, "used" | "limit">): number {
	if (m.limit <= 0) return 0;
	return Math.min(100, Math.max(0, (m.used / m.limit) * 100));
}

/** The bar tone: calm under 80%, a warning from 80%, an alert at the limit. */
export function meterTone(m: Pick<MeterView, "used" | "limit">): "ok" | "warn" | "full" {
	const p = meterPercent(m);
	if (m.limit > 0 && m.used >= m.limit) return "full";
	return p >= 80 ? "warn" : "ok";
}

/** The plans in display order with the current one marked. */
export function planCards(plans: Plan[], current: string): { plan: Plan; current: boolean }[] {
	return plans.map((plan) => ({ plan, current: plan.id === current }));
}

/** A money amount in its currency, e.g. $78.00. */
export function formatMoney(amount: number, currency: string): string {
	try {
		return new Intl.NumberFormat("en-US", { style: "currency", currency: currency.toUpperCase() || "USD" }).format(amount);
	} catch {
		return `${amount.toFixed(2)} ${currency.toUpperCase()}`;
	}
}
