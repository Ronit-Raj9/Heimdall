/**
 * Client-side check of a five-field cron line (minute hour day month weekday) and the next times
 * it fires in a time zone. The server keeps its own check; this one only keeps the Create buttons
 * off while the text cannot be a schedule.
 */

/** The range of each field, in order: minute, hour, day of month, month, weekday (0 and 7 are Sunday). */
const FIELDS: readonly { min: number; max: number }[] = [
	{ min: 0, max: 59 },
	{ min: 0, max: 23 },
	{ min: 1, max: 31 },
	{ min: 1, max: 12 },
	{ min: 0, max: 7 },
];

/** The values one field allows, or null when the field is not valid. */
function parseField(text: string, min: number, max: number): Set<number> | null {
	const out = new Set<number>();
	for (const part of text.split(",")) {
		const m = /^(\*|\d+(?:-\d+)?)(?:\/(\d+))?$/.exec(part);
		if (!m) return null;
		const step = m[2] === undefined ? 1 : Number(m[2]);
		if (!(step >= 1)) return null;
		let lo = min;
		let hi = max;
		if (m[1] !== "*") {
			const [a, b] = m[1].split("-");
			lo = Number(a);
			hi = b === undefined ? (m[2] === undefined ? lo : max) : Number(b);
		}
		if (lo < min || hi > max || lo > hi) return null;
		for (let v = lo; v <= hi; v += step) out.add(v);
	}
	return out;
}

export interface ParsedCron {
	minutes: Set<number>;
	hours: Set<number>;
	days: Set<number>;
	months: Set<number>;
	/** 0 to 6, Sunday is 0. */
	weekdays: Set<number>;
	/** True when the day field is "*" (cron matches either day or weekday when both are set). */
	anyDay: boolean;
	anyWeekday: boolean;
}

/** The parsed cron line, or null when it is not a valid five-field line. */
export function parseCron(text: string): ParsedCron | null {
	const parts = text.trim().split(/\s+/);
	if (parts.length !== 5 || parts[0] === "") return null;
	const sets = parts.map((p, i) => parseField(p, FIELDS[i].min, FIELDS[i].max));
	if (sets.some((s) => s === null)) return null;
	const [minutes, hours, days, months, wd] = sets as Set<number>[];
	const weekdays = new Set([...wd].map((d) => d % 7));
	return { minutes, hours, days, months, weekdays, anyDay: parts[2] === "*", anyWeekday: parts[4] === "*" };
}

/** "invalid" when the text is not a schedule, "empty" when blank, null when it is fine. */
export function cronProblem(text: string): "empty" | "invalid" | null {
	if (!text.trim()) return "empty";
	return parseCron(text) ? null : "invalid";
}

interface WallTime {
	month: number;
	day: number;
	weekday: number;
	hour: number;
	minute: number;
}

const WEEKDAYS: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

function wallClock(fmt: Intl.DateTimeFormat, t: number): WallTime {
	const p: Record<string, string> = {};
	for (const x of fmt.formatToParts(new Date(t))) p[x.type] = x.value;
	return {
		month: Number(p.month),
		day: Number(p.day),
		weekday: WEEKDAYS[p.weekday] ?? 0,
		hour: Number(p.hour) % 24,
		minute: Number(p.minute),
	};
}

/** A time zone the browser knows, else "UTC". */
export function safeZone(tz: string | undefined): string {
	const zone = tz?.trim() || "UTC";
	try {
		new Intl.DateTimeFormat("en-US", { timeZone: zone });
		return zone;
	} catch {
		return "UTC";
	}
}

/** The browser's own time zone, "UTC" when it cannot be read. */
export function browserZone(): string {
	try {
		return safeZone(Intl.DateTimeFormat().resolvedOptions().timeZone);
	} catch {
		return "UTC";
	}
}

/**
 * The next n times (after `from`) the cron line fires in the zone, as Dates. Empty when the line is
 * not valid or never fires within about a year.
 */
export function nextRuns(text: string, tz: string, n: number, from: Date = new Date()): Date[] {
	const c = parseCron(text);
	if (!c || n <= 0) return [];
	const fmt = new Intl.DateTimeFormat("en-US", {
		timeZone: safeZone(tz),
		hourCycle: "h23",
		month: "numeric",
		day: "numeric",
		weekday: "short",
		hour: "numeric",
		minute: "numeric",
	});
	const dayOk = (w: WallTime) => {
		if (!c.months.has(w.month)) return false;
		const d = c.days.has(w.day);
		const wd = c.weekdays.has(w.weekday);
		if (c.anyDay && c.anyWeekday) return true;
		if (c.anyDay) return wd;
		if (c.anyWeekday) return d;
		return d || wd;
	};
	const out: Date[] = [];
	const MIN = 60_000;
	let t = Math.floor(from.getTime() / MIN) * MIN + MIN;
	const end = t + 400 * 24 * 60 * MIN;
	for (let guard = 0; t < end && out.length < n && guard < 200_000; guard++) {
		const w = wallClock(fmt, t);
		if (!dayOk(w)) t += (24 * 60 - (w.hour * 60 + w.minute)) * MIN;
		else if (!c.hours.has(w.hour)) t += (60 - w.minute) * MIN;
		else if (!c.minutes.has(w.minute)) t += MIN;
		else {
			out.push(new Date(t));
			t += MIN;
		}
	}
	return out;
}

/** "Fri 28 Nov 02:00" in the zone. */
export function formatRun(d: Date, tz: string): string {
	const p: Record<string, string> = {};
	for (const x of new Intl.DateTimeFormat("en-GB", {
		timeZone: safeZone(tz),
		hourCycle: "h23",
		weekday: "short",
		day: "numeric",
		month: "short",
		hour: "2-digit",
		minute: "2-digit",
	}).formatToParts(d))
		p[x.type] = x.value;
	return `${p.weekday} ${p.day} ${p.month} ${p.hour}:${p.minute}`;
}

/** How many times a day the line fires on average over the next week (for cost lines). 0 when not valid. */
export function runsPerDay(text: string, from: Date = new Date()): number {
	const c = parseCron(text);
	if (!c) return 0;
	const week = nextRuns(text, "UTC", 7 * 24 * 60 + 1, from);
	const limit = from.getTime() + 7 * 24 * 60 * 60_000;
	return week.filter((d) => d.getTime() <= limit).length / 7;
}
