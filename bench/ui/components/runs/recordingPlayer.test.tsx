import "@/lib/i18n";

import type { RecordingStep, RecordingView, RunRecordings } from "@/lib/store/apis/recordingsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

const state: { data?: RunRecordings; isLoading: boolean; error?: unknown } = { isLoading: false };

vi.mock("@/lib/store/apis/recordingsApi", () => ({
	useGetRunRecordingsQuery: () => state,
}));

const { RecordingPlayer, RecordingTab } = await import("./recordingPlayer");
const { activeStepIndex, formatClock, formatSize, framePoint, otherRecordings, playableVideos, stepMarks } = await import("./recordingPlayer.utils");

const steps: RecordingStep[] = [
	{ seq: 1, service: "browser", tool: "browser_navigate", label: "browser_navigate http://webapp:8000/", started: "2026-11-21T10:00:01Z", latency_ms: 300 },
	{ seq: 2, service: "other", tool: "search", label: "search", started: "2026-11-21T10:00:02Z", latency_ms: 10 },
	{ seq: 3, service: "browser", tool: "browser_click", label: "browser_click Save", started: "2026-11-21T10:00:05.500Z", latency_ms: 90, is_error: true },
];
const video: RecordingView = {
	index: 0, service: "browser", id: "video", kind: "video", file: "video-page-1.webm", mime: "video/webm", bytes: 2 * 1024 * 1024,
	started_at: "2026-11-21T10:00:00Z", url: "/api/bench/runs/r1/recordings/0",
};
const har: RecordingView = { index: 1, service: "browser", id: "har", kind: "har", file: "har-context-1.har", bytes: 2048, url: "/api/bench/runs/r1/recordings/1" };
const failed: RecordingView = { index: 2, service: "browser", id: "console", kind: "console", error: "not written" };

describe("recording player helpers", () => {
	it("places a service's steps on its video", () => {
		const marks = stepMarks(steps, "browser", video.started_at);
		expect(marks.map((m) => [m.step.seq, m.offsetMs])).toEqual([
			[1, 1000],
			[3, 5500],
		]);
		// Without a recorded start the first call is 0.
		expect(stepMarks(steps, "browser").map((m) => m.offsetMs)).toEqual([0, 4500]);
		expect(stepMarks(steps, "nobody")).toEqual([]);
	});
	it("finds the step under the playhead", () => {
		const marks = stepMarks(steps, "browser", video.started_at);
		expect(activeStepIndex(marks, 0)).toBe(-1);
		expect(activeStepIndex(marks, 1000)).toBe(0);
		expect(activeStepIndex(marks, 5400)).toBe(1);
		expect(activeStepIndex(marks, 99_000)).toBe(1);
	});
	it("splits videos from other files", () => {
		expect(playableVideos([video, har, failed]).map((r) => r.index)).toEqual([0]);
		expect(otherRecordings([video, har, failed]).map((r) => r.index)).toEqual([1, 2]);
	});
	it("formats clock and size", () => {
		expect(formatClock(65_000)).toBe("1:05");
		expect(formatSize(2048)).toBe("2.0 KB");
		expect(formatSize(3 * 1024 * 1024)).toBe("3.0 MB");
		expect(formatSize(undefined)).toBe("");
	});
	it("scales a click on a shown frame to the frame", () => {
		const box = { left: 10, top: 20, width: 480, height: 300 };
		expect(framePoint(250, 170, box, { width: 960, height: 600 })).toEqual({ x: 480, y: 300 });
		expect(framePoint(5, 170, box, { width: 960, height: 600 })).toBeNull();
		expect(framePoint(250, 170, { ...box, width: 0 }, { width: 960, height: 600 })).toBeNull();
	});
});

describe("RecordingPlayer", () => {
	it("shows the video, step list with marks and the other files", () => {
		state.data = { run_id: "r1", sealed: true, recordings: [video, har, failed], steps };
		const html = renderToStaticMarkup(<RecordingPlayer runId="r1" />);
		expect(html).toContain('data-testid="recording-video"');
		expect(html).toContain('src="/api/bench/runs/r1/recordings/0"');
		expect(html).toContain("browser_navigate http://webapp:8000/");
		expect(html).toContain('data-testid="recording-step-1"');
		expect(html).not.toContain(">search<");
		expect(html).toContain("0:01");
		expect(html).toContain("0:05");
		expect(html).toContain('data-testid="recording-files"');
		expect(html).toContain("Not recorded: not written");
		expect(html).toContain("2.0 MB");
	});
	it("says when a person took control", () => {
		state.data = {
			run_id: "r1", sealed: true, recordings: [video], steps, human_assisted: true,
			actions: [{ seq: 1, at: "2026-11-21T10:00:03Z", service: "browser", action: "click", x: 4, y: 5, by: "ana@example.com" }],
		};
		const html = renderToStaticMarkup(<RecordingPlayer runId="r1" />);
		expect(html).toContain('data-testid="recording-human-assisted"');
		expect(html).toContain("click (4, 5) by ana@example.com");
	});
	it("shows an empty state without a video", () => {
		state.data = { run_id: "r1", sealed: true, recordings: [failed], steps: [] };
		const html = renderToStaticMarkup(<RecordingPlayer runId="r1" />);
		expect(html).toContain('data-testid="recording-empty"');
		expect(html).toContain("Not recorded");
	});
	it("offers to compare with another run", () => {
		state.data = { run_id: "r1", sealed: true, recordings: [video], steps };
		const html = renderToStaticMarkup(<RecordingTab runId="r1" />);
		expect(html).toContain('data-testid="recording-compare-form"');
		expect(html).toContain("Compare two runs");
		expect(html).not.toMatch(/\b(arm|condition|probe|canary|sealed|lease|suite|runner)\b/i);
	});
});
