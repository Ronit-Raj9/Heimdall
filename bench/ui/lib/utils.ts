import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}
/**
 * A Radix Select value that stays controlled from the first render: an empty string shows the
 * placeholder, so a picker filled in by a later response never switches from uncontrolled.
 */
export function controlledSelectValue(value: string | null | undefined): string {
	return value ?? "";
}

/**
 * One duration format for every page (M3-24): "13 s" under a minute, "4 min 12 s" under an hour, "1 h 5 min" above.
 * "-" when unknown.
 */
export function formatDuration(seconds: number | undefined | null): string {
	if (seconds === undefined || seconds === null || !Number.isFinite(seconds)) return "-";
	const s = Math.max(0, Math.round(seconds));
	if (s < 60) return `${s} s`;
	const m = Math.floor(s / 60);
	if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
	const h = Math.floor(m / 60);
	return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
}
