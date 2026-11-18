import type { ApiToken, Job, JobStatus, PlatformTime } from "@/lib/types/bench";

/** A platform time as a Date: RFC 3339 text or unix milliseconds. Null for absent or unreadable values. */
export function toDate(value: PlatformTime | null | undefined): Date | null {
	if (value === null || value === undefined || value === "") return null;
	const date = typeof value === "number" ? new Date(value) : new Date(value);
	if (Number.isNaN(date.getTime())) return null;
	// Go's zero time (0001-01-01) means "never set".
	if (date.getUTCFullYear() <= 1) return null;
	return date;
}

/** Absolute date and time in the reader's locale, for tables and titles. */
export function formatWhen(value: PlatformTime | null | undefined, locale: string): string | null {
	const date = toDate(value);
	if (!date) return null;
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
	["year", 365 * 24 * 3600],
	["month", 30 * 24 * 3600],
	["week", 7 * 24 * 3600],
	["day", 24 * 3600],
	["hour", 3600],
	["minute", 60],
	["second", 1],
];

/** "3 minutes ago", "in 2 days": the largest whole unit. Null for absent values. */
export function formatRelative(value: PlatformTime | null | undefined, locale: string, now: number = Date.now()): string | null {
	const date = toDate(value);
	if (!date) return null;
	const seconds = Math.round((date.getTime() - now) / 1000);
	const abs = Math.abs(seconds);
	const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	if (abs < 45) return format.format(0, "second");
	for (const [unit, size] of UNITS) {
		if (abs >= size) return format.format(Math.round(seconds / size), unit);
	}
	return format.format(seconds, "second");
}

/** Seconds between two times, or from start until now while running. */
export function durationSeconds(start: PlatformTime | undefined, end: PlatformTime | undefined, now: number = Date.now()): number | null {
	const from = toDate(start);
	if (!from) return null;
	const to = toDate(end)?.getTime() ?? now;
	return Math.max(0, Math.round((to - from.getTime()) / 1000));
}

/** 75 -> "1m 15s", 3700 -> "1h 1m". */
export function formatDuration(seconds: number | null): string | null {
	if (seconds === null) return null;
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
	const hours = Math.floor(minutes / 60);
	return `${hours}h ${minutes % 60}m`;
}

/** Statuses that will not change again. */
export const TERMINAL_JOB_STATUSES: ReadonlySet<JobStatus> = new Set(["succeeded", "failed", "cancelled"]);

/** True while a job can still change: queued or running. */
export function isActiveJob(job: Pick<Job, "status">): boolean {
	return !TERMINAL_JOB_STATUSES.has(job.status);
}

/** Running and queued jobs first (oldest first, the order they run), then the rest newest first. */
export function splitJobs(jobs: readonly Job[]): { active: Job[]; recent: Job[] } {
	const time = (job: Job) => toDate(job.created_at)?.getTime() ?? 0;
	const active = jobs.filter(isActiveJob).sort((a, b) => time(a) - time(b));
	const recent = jobs.filter((job) => !isActiveJob(job)).sort((a, b) => time(b) - time(a));
	return { active, recent };
}

/** A job's progress read from its kind-specific record: done of total, and its phase or message. */
export function jobProgress(job: Pick<Job, "progress">): { done: number; total: number; label?: string } | null {
	const p = job.progress;
	if (!p || typeof p !== "object") return null;
	const num = (key: string): number | undefined => (typeof p[key] === "number" ? (p[key] as number) : undefined);
	const label = typeof p.phase === "string" ? p.phase : typeof p.message === "string" ? p.message : undefined;
	// Generic {done, total}; experiment.run counts sealed and skipped slots of all slots.
	const total = num("total") ?? num("slots");
	const sealed = num("sealed");
	const done = num("done") ?? (sealed !== undefined ? sealed + (num("skipped") ?? 0) : undefined);
	if (total === undefined || done === undefined) return label ? { done: 0, total: 0, label } : null;
	return { done, total, label };
}

/** Share done of a job's progress, 0..1, or null when the job reports no total. */
export function jobFraction(job: Pick<Job, "progress" | "status">): number | null {
	if (job.status === "succeeded") return 1;
	const progress = jobProgress(job);
	if (!progress || progress.total <= 0) return null;
	return Math.min(1, Math.max(0, progress.done / progress.total));
}

/** The experiment a job acts on, from its target or params. */
export function jobExperimentId(job: Pick<Job, "target" | "params" | "kind">): string | null {
	if (!job.kind.startsWith("experiment.")) return null;
	const fromParams = job.params && typeof job.params.exp_id === "string" ? job.params.exp_id : null;
	return fromParams ?? (job.target ? job.target : null);
}

/** Token state at a moment: revoked wins over expired. */
export function tokenState(token: Pick<ApiToken, "revoked_at" | "expires_at">, now: number = Date.now()): "active" | "revoked" | "expired" {
	if (toDate(token.revoked_at)) return "revoked";
	const expires = toDate(token.expires_at);
	if (expires && expires.getTime() <= now) return "expired";
	return "active";
}

/** Initials for an avatar chip: "Ada Lovelace" -> "AL", "ops@acme.dev" -> "O". */
export function initials(name: string | undefined, email: string | undefined): string {
	const source = (name ?? "").trim();
	if (source) {
		const parts = source.split(/\s+/).filter(Boolean);
		return parts
			.slice(0, 2)
			.map((part) => part[0]?.toUpperCase() ?? "")
			.join("");
	}
	return (email ?? "?").trim().charAt(0).toUpperCase() || "?";
}

/** Pool names the runner accepts: letters, digits, dot, dash, underscore. */
export function isValidPool(pool: string): boolean {
	return /^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(pool);
}

/** A rough email check for forms: something@something.tld. The server decides. */
export function looksLikeEmail(value: string): boolean {
	return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim());
}

/**
 * The runner start command for a new token: what the server returned, or one built from the
 * page's origin when the server leaves it out.
 */
export function runnerStartCommand(origin: string, pool: string, serverCommand?: string): string {
	if (serverCommand && serverCommand.trim()) return serverCommand.trim();
	return `bifrost-bench runner start -server ${origin} -token-file ~/.heimdall/runner-token -pool ${pool}`;
}