import "@/lib/i18n";

import type { ReplayFrame, RunFrames } from "@/lib/store/apis/framesApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

const state: { data?: RunFrames; isLoading: boolean; error?: unknown; polling?: number } = { isLoading: false };

vi.mock("@/lib/store/apis/framesApi", () => ({
	frameImageUrl: (runId: string, n: number) => `/api/bench/runs/${runId}/frames/${n}`,
	useGetRunFramesQuery: (_id: string, opts: { pollingInterval: number }) => {
		state.polling = opts.pollingInterval;
		return state;
	},
}));

const { ScreenReplay, actionLabel, formatOffset, frameDelay } = await import("./screenReplay");

const frames: ReplayFrame[] = [
	{ index: 0, offset_ms: 0, tool: "screenshot", image: "frames/0001.png", url: "" },
	{ index: 1, offset_ms: 2500, tool: "screenshot", action: 'click {"x":10,"y":20}', image: "frames/0002.png", url: "" },
	{ index: 2, offset_ms: 64000, tool: "screenshot", action: 'type {"text":"hi"}', image: "frames/0003.png", url: "" },
];

describe("screen replay helpers", () => {
	it("clamps frame delay and divides by speed", () => {
		expect(frameDelay(frames, 0, 1)).toBe(2500);
		expect(frameDelay(frames, 0, 2)).toBe(1250);
		expect(frameDelay(frames, 1, 1)).toBe(3000);
		expect(frameDelay(frames, 2, 4)).toBe(150);
	});
	it("formats offsets and labels", () => {
		expect(formatOffset(64000)).toBe("1:04");
		expect(formatOffset(-5)).toBe("0:00");
		expect(actionLabel(frames[0], "Looked")).toBe("Looked");
		expect(actionLabel(frames[1], "Looked")).toBe('click {"x":10,"y":20}');
	});
});

describe("ScreenReplay", () => {
	it("renders the first frame, scrubber, speeds and step list", () => {
		state.data = { run_id: "r1", live: false, frames };
		const html = renderToStaticMarkup(<ScreenReplay runId="r1" />);
		expect(html).toContain('data-testid="replay-frame"');
		expect(html).toContain("/api/bench/runs/r1/frames/0");
		expect(html).toContain('data-testid="replay-scrubber"');
		for (const s of [1, 2, 4]) expect(html).toContain(`replay-speed-${s}`);
		expect(html).toContain("click {&quot;x&quot;:10,&quot;y&quot;:20}");
		expect(html).toContain("Frame 1 of 3");
		expect(html).toContain("0:00 / 1:04");
		expect(state.polling).toBe(0);
	});
	it("polls every 3 s while live", () => {
		state.data = { run_id: "r1", live: true, frames };
		const html = renderToStaticMarkup(<ScreenReplay runId="r1" live />);
		expect(state.polling).toBe(3000);
		expect(html).toContain("Live, updating");
	});
	it("shows an empty state without frames", () => {
		state.data = { run_id: "r1", live: false, frames: [] };
		expect(renderToStaticMarkup(<ScreenReplay runId="r1" />)).toContain('data-testid="replay-empty"');
	});
});
