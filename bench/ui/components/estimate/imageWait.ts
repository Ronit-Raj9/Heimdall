import type { TaskImageWait } from "@/lib/types/gateway";
import "./ufh2I18n";

/**
 * "First run in about 2 minutes: its task image is built first. Up to 5 task images, about 7 minutes in all." from the estimate's image_wait
 * (bench/handlers/imagewait_ufh2.go); null when no task image must be built. Pure.
 */
export function imageWaitText(
	t: (key: string, opts?: Record<string, unknown>) => string,
	w: TaskImageWait | null | undefined,
): string | null {
	if (!w || !(w.images > 0)) return null;
	const o = { ns: "ufh2", minutes: Math.max(1, w.minutes), count: w.images, total: Math.max(1, w.total_minutes ?? w.minutes) };
	return `${t("images.first", o)} ${t("images.total", o)}`;
}

/**
 * The newest image progress when the experiment's newest stage event is the worker preparing a task
 * image (engine StageImage, forwarded on the lease heartbeat) and no run started after it; null
 * otherwise. Pure.
 */
export function imageBuilding(
	events: readonly { kind: string; stage?: string | null; message?: string | null }[],
): { message: string } | null {
	for (let i = events.length - 1; i >= 0; i--) {
		const e = events[i];
		if (e.kind === "stage" && e.stage === "image") return { message: e.message ?? "" };
		if (e.kind === "stage" || e.kind === "slot_sealed") return null;
	}
	return null;
}