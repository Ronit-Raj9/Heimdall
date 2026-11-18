// Pure helpers for the failure inbox screen: filters, form validation and request bodies, and how an
// accepted item's regression task is doing. No I/O, so each is tested alone.
import {
	INBOX_STATES,
	INBOX_TASK_KINDS,
	type InboxAcceptRequest,
	type InboxItem,
	type InboxList,
	type InboxRejectRequest,
	type InboxReleaseStatus,
	type InboxSnoozeRequest,
	type InboxState,
	type InboxSuite,
	type InboxTaskKind,
	type Mode,
} from "@/lib/types/bench";

/** The state filter's value for every state. */
export const INBOX_ALL = "all";
export const INBOX_FILTERS = [INBOX_ALL, ...INBOX_STATES] as const;
export type InboxFilter = (typeof INBOX_FILTERS)[number];

/** Snooze bounds the server accepts, and the default. */
export const SNOOZE_MIN_DAYS = 1;
export const SNOOZE_MAX_DAYS = 90;
export const SNOOZE_DEFAULT_DAYS = 7;

export function isInboxState(value: string): value is InboxState {
	return (INBOX_STATES as readonly string[]).includes(value);
}

export function isInboxTaskKind(value: string): value is InboxTaskKind {
	return (INBOX_TASK_KINDS as readonly string[]).includes(value);
}

/** Items per state, with absent states read as zero. */
export function inboxCount(counts: InboxList["counts"] | null | undefined, filter: InboxFilter): number {
	const all = counts ?? {};
	if (filter === INBOX_ALL) return INBOX_STATES.reduce((sum, state) => sum + (all[state] ?? 0), 0);
	return all[filter] ?? 0;
}

/**
 * Items in the state. The server already filters by state; filtering again keeps the table right
 * when a response holds more (fixture mode answers one list for every filter).
 */
export function filterInboxItems(items: readonly InboxItem[] | null | undefined, filter: InboxFilter, source?: string | null): InboxItem[] {
	return (items ?? []).filter((item) => (filter === INBOX_ALL || item.state === filter) && (!source || item.source === source));
}

/** Only new and snoozed items can be accepted, rejected or snoozed; the server answers 409 otherwise. */
export function isActionable(item: Pick<InboxItem, "state">): boolean {
	return item.state === "new" || item.state === "snoozed";
}

/** Only a live server writes; recorded and synthetic data are read-only. */
export function canWriteInbox(mode: Mode | undefined): boolean {
	return mode === "live";
}

/**
 * An http or https link from a trace source, or null. A source link is data from outside, so any
 * other scheme (javascript:, data:) is never rendered as a link.
 */
export function safeExternalHref(link: string | undefined | null): string | null {
	if (!link) return null;
	try {
		const url = new URL(link);
		return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
	} catch {
		return null;
	}
}

// ---------------------------------------------------------------------------------------------
// Accept
// ---------------------------------------------------------------------------------------------

export interface AcceptFormValues {
	suite: string;
	kind: InboxTaskKind;
	/** Empty: the server generates one. */
	taskId: string;
}

export type AcceptFormError = "suite";

/** The form's starting values: the suggested suite when it is offered, and the suggested kind. */
export function acceptDefaults(
	item: Pick<InboxItem, "suggested_suite" | "suggested_kind">,
	suites: readonly InboxSuite[],
): AcceptFormValues {
	const suite = suites.some((s) => s.id === item.suggested_suite) ? (item.suggested_suite ?? "") : suites.length === 1 ? suites[0].id : "";
	return { suite, kind: isInboxTaskKind(item.suggested_kind) ? item.suggested_kind : "qa", taskId: "" };
}

export function acceptFormErrors(values: AcceptFormValues): AcceptFormError[] {
	return values.suite.trim() === "" ? ["suite"] : [];
}

/** The POST /inbox/{id}/accept body: suite and kind always, task_id only when one was typed. */
export function acceptRequestFrom(values: AcceptFormValues): InboxAcceptRequest {
	const suite = values.suite.trim();
	if (suite === "") throw new Error("accept request needs a suite");
	const taskId = values.taskId.trim();
	return { suite, kind: values.kind, ...(taskId ? { task_id: taskId } : {}) };
}

// ---------------------------------------------------------------------------------------------
// Reject
// ---------------------------------------------------------------------------------------------

export interface RejectFormValues {
	reason: string;
	/** Empty: a plain reject. An item id: the item becomes a duplicate of it. */
	duplicateOf: string;
}

export type RejectFormError = "reason" | "duplicateSelf";

export function rejectFormErrors(values: RejectFormValues, itemId?: string): RejectFormError[] {
	const errors: RejectFormError[] = [];
	if (values.reason.trim() === "") errors.push("reason");
	if (itemId && values.duplicateOf.trim() === itemId) errors.push("duplicateSelf");
	return errors;
}

/** The POST /inbox/{id}/reject body: the reason, and duplicate_of only when one was given. */
export function rejectRequestFrom(values: RejectFormValues): InboxRejectRequest {
	const reason = values.reason.trim();
	if (reason === "") throw new Error("reject request needs a reason");
	const duplicateOf = values.duplicateOf.trim();
	return { reason, ...(duplicateOf ? { duplicate_of: duplicateOf } : {}) };
}

// ---------------------------------------------------------------------------------------------
// Snooze
// ---------------------------------------------------------------------------------------------

/** Days as typed, or null when not a whole number from 1 to 90. */
export function parseSnoozeDays(value: string): number | null {
	const text = value.trim();
	if (!/^\d+$/.test(text)) return null;
	const days = Number(text);
	return days >= SNOOZE_MIN_DAYS && days <= SNOOZE_MAX_DAYS ? days : null;
}

export function snoozeRequestFrom(value: string): InboxSnoozeRequest {
	const days = parseSnoozeDays(value);
	if (days === null) throw new Error("snooze request needs 1 to 90 days");
	return { days };
}

// ---------------------------------------------------------------------------------------------
// Regression task status
// ---------------------------------------------------------------------------------------------

/** The i18n key and values that say how an accepted item's task did in its newest experiment. */
export type ReleaseSummary =
	| { key: "inbox.release.passes" | "inbox.release.fails"; values: { experiment: string; passed: number; runs: number } }
	| { key: "inbox.release.passesNoExp" | "inbox.release.failsNoExp"; values: { passed: number; runs: number } }
	| { key: "inbox.release.notRun"; values: Record<string, never> }
	| { key: "inbox.release.other"; values: { status: string } };

export function releaseSummary(release: InboxReleaseStatus | undefined): ReleaseSummary {
	if (!release || release.status === "not_run") return { key: "inbox.release.notRun", values: {} };
	const counts = { passed: release.passed, runs: release.runs };
	if (release.status === "passes" || release.status === "fails") {
		if (release.experiment) {
			return {
				key: release.status === "passes" ? "inbox.release.passes" : "inbox.release.fails",
				values: { experiment: release.experiment, ...counts },
			};
		}
		return { key: release.status === "passes" ? "inbox.release.passesNoExp" : "inbox.release.failsNoExp", values: counts };
	}
	return { key: "inbox.release.other", values: { status: String(release.status) } };
}

/** Badge colour for a release status. */
export function releaseVariant(release: InboxReleaseStatus | undefined): "success" | "destructive" | "secondary" {
	if (release?.status === "passes") return "success";
	if (release?.status === "fails") return "destructive";
	return "secondary";
}

/** Badge colour for an item state. */
export function stateVariant(state: string): "default" | "success" | "secondary" | "outline" | "warning" {
	switch (state) {
		case "new":
			return "default";
		case "accepted":
			return "success";
		case "snoozed":
			return "warning";
		case "rejected":
		case "duplicate":
			return "secondary";
		default:
			return "outline";
	}
}

/** Redaction counts as "1 email, 2 phone", largest first. Never values: the server sends counts only. */
export function redactionEntries(redacted: Record<string, number> | undefined): Array<[string, number]> {
	return Object.entries(redacted ?? {})
		.filter(([, n]) => n > 0)
		.sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}