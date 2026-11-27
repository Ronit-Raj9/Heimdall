import type { OnboardDraftSummary, OnboardInput } from "@/lib/types/onboard";

// Pure helpers of /suites/add (lane 6b): the step line, the earlier drafts list and the source
// kept for a reload.

export const ONBOARD_STEPS = ["source", "found", "preview", "ready", "publish"] as const;
export type OnboardStep = (typeof ONBOARD_STEPS)[number];

/** How far the page is: 0 with only the source box, 1 once something was found, 4 with a draft (steps 3 to 5 show together). */
export function stepReached({ found, draft }: { found: boolean; draft: boolean }): number {
	return draft ? 4 : found ? 1 : 0;
}

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * The drafts worth listing: a draft with no tasks that was last touched more than a day ago is a
 * dead test, so it is left out (M6-53). The current draft always stays.
 */
export function visibleDrafts(drafts: readonly OnboardDraftSummary[], now: number, current: string | null): OnboardDraftSummary[] {
	return drafts.filter((d) => {
		if (d.id === current || d.tasks > 0) return true;
		const at = Date.parse(d.updated);
		return Number.isNaN(at) || now - at < DAY_MS;
	});
}

/** The file or link name of a draft's source, without the server's folders ("orders.csv", not "/data/uploads/ab12/orders.csv"). */
export function draftSourceName(input: string): string {
	const text = input.trim();
	if (!text) return "";
	if (/^https?:\/\//i.test(text)) {
		try {
			const u = new URL(text);
			const last = u.pathname.split("/").filter(Boolean).slice(-2).join("/");
			return last ? `${u.hostname}/${last}` : u.hostname;
		} catch {
			return text;
		}
	}
	const parts = text.split(/[\\/]/).filter(Boolean);
	return parts[parts.length - 1] ?? text;
}

/** Where the looked-at source is kept so a reload comes back to step 2 (X-83). */
export const SOURCE_STORAGE_KEY = "heimdall.addBenchmark.source";

/** Only a link or pasted rows can be kept: an upload id is gone after a reload. */
export function keepableSource(input: OnboardInput | null): OnboardInput | null {
	if (!input || input.upload) return null;
	if (!input.url && !input.data) return null;
	return input;
}

export function readKeptSource(raw: string | null): OnboardInput | null {
	if (!raw) return null;
	try {
		const v = JSON.parse(raw) as unknown;
		if (!v || typeof v !== "object") return null;
		return keepableSource(v as OnboardInput);
	} catch {
		return null;
	}
}
