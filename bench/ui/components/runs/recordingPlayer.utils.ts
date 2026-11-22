// Pure helpers of the recording player (lane Z03): where each step falls in a video, and which
// step is under the playhead. Tested without a page.
import type { RecordingStep, RecordingView } from "@/lib/store/apis/recordingsApi";

/** One step placed on a video's timeline. */
export interface StepMark {
	step: RecordingStep;
	/** Milliseconds from the video's first frame to the start of the call. */
	offsetMs: number;
}

/**
 * The steps of one service placed on its video: each call's start minus the video's start. A
 * video without a recorded start is aligned to the service's first call, which is when Playwright
 * opens the page. Steps that do not parse are left out; a step before the start sits at 0.
 */
export function stepMarks(steps: RecordingStep[], service: string, videoStart?: string): StepMark[] {
	const own = steps.filter((s) => s.service === service);
	const times = own.map((s) => Date.parse(s.started));
	let start = videoStart ? Date.parse(videoStart) : NaN;
	if (Number.isNaN(start)) start = times.find((t) => !Number.isNaN(t)) ?? NaN;
	if (Number.isNaN(start)) return [];
	const out: StepMark[] = [];
	own.forEach((step, i) => {
		if (!Number.isNaN(times[i])) out.push({ step, offsetMs: Math.max(0, times[i] - start) });
	});
	return out.sort((a, b) => a.offsetMs - b.offsetMs);
}

/** The index of the step under the playhead: the last one that started by then, or -1. */
export function activeStepIndex(marks: StepMark[], currentMs: number): number {
	let idx = -1;
	for (let i = 0; i < marks.length; i++) {
		if (marks[i].offsetMs <= currentMs + 150) idx = i;
		else break;
	}
	return idx;
}

/** The recordings a player can show, videos first in file order. */
export function playableVideos(recordings: RecordingView[]): RecordingView[] {
	return recordings.filter((r) => r.kind === "video" && r.url && !r.error);
}

/** The other recordings (HAR, console, log) and the failed ones, for the file list. */
export function otherRecordings(recordings: RecordingView[]): RecordingView[] {
	return recordings.filter((r) => r.kind !== "video" || !r.url || r.error);
}

/** m:ss of a millisecond offset. */
export function formatClock(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** A byte count in KB or MB, one decimal. */
export function formatSize(bytes?: number): string {
	if (!bytes || bytes < 0) return "";
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Where a click on a shown image lands on the frame: the pointer position scaled from the image's
 * displayed box to its natural size, rounded, inside the frame.
 */
export function framePoint(
	clientX: number,
	clientY: number,
	box: { left: number; top: number; width: number; height: number },
	natural: { width: number; height: number },
): { x: number; y: number } | null {
	if (box.width <= 0 || box.height <= 0 || natural.width <= 0 || natural.height <= 0) return null;
	const x = Math.round(((clientX - box.left) / box.width) * natural.width);
	const y = Math.round(((clientY - box.top) / box.height) * natural.height);
	if (x < 0 || y < 0 || x >= natural.width || y >= natural.height) return null;
	return { x, y };
}
