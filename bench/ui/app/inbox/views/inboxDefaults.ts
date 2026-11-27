import { INBOX_ALL, inboxCount, type InboxFilter } from "@/components/inbox/inbox.utils";
import type { InboxItem, InboxList } from "@/lib/types/bench";

/**
 * The state the inbox opens on when the URL names none: New while something is new, else All when
 * anything exists, so the screen never opens on an empty "New 0" list while older items exist.
 */
export function openingInboxFilter(counts: InboxList["counts"] | null | undefined): InboxFilter {
	if (inboxCount(counts, "new") > 0) return "new";
	return inboxCount(counts, INBOX_ALL) > 0 ? INBOX_ALL : "new";
}

/** The "Since" choices of the inbox list, read against each item's last sighting. */
export const INBOX_SINCE = ["", "24h", "7d", "30d"] as const;
export type InboxSince = (typeof INBOX_SINCE)[number];

const SINCE_MS: Record<Exclude<InboxSince, "">, number> = {
	"24h": 86_400_000,
	"7d": 7 * 86_400_000,
	"30d": 30 * 86_400_000,
};

/** The items seen within the window, and matching the search text (title, id, trace id, flagged reasons, model, source). */
export function narrowInboxItems<T extends InboxItem>(items: readonly T[], q: string, since: InboxSince, now = Date.now()): T[] {
	const needle = q.trim().toLowerCase();
	const from = since ? now - SINCE_MS[since] : null;
	return items.filter((item) => {
		if (from !== null) {
			const seen = Date.parse(item.last_seen);
			if (!Number.isNaN(seen) && seen < from) return false;
		}
		if (!needle) return true;
		return [item.title, item.id, item.trace_id, item.model ?? "", item.source, item.signature, ...(item.flagged ?? [])].some((s) =>
			(s ?? "").toLowerCase().includes(needle),
		);
	});
}