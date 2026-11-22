import "@/lib/i18n";

import type { RunLiveStatus } from "@/lib/store/apis/recordingsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

const state: { data?: RunLiveStatus; polling?: number } = {};

vi.mock("@/lib/store/apis/recordingsApi", () => ({
	liveFrameUrl: (runId: string, at?: string) => `/api/bench/runs/${runId}/live/frame?at=${at ?? ""}`,
	useGetRunLiveQuery: (_id: string, opts: { pollingInterval: number }) => {
		state.polling = opts.pollingInterval;
		return { data: state.data };
	},
	useTakeoverRunMutation: () => [vi.fn(), { isLoading: false }],
}));

const { LiveScreen } = await import("./liveScreen");

describe("LiveScreen", () => {
	it("renders nothing for a run that shows no screen", () => {
		state.data = { run_id: "r1", live: false };
		expect(renderToStaticMarkup(<LiveScreen runId="r1" />)).toBe("");
		expect(state.polling).toBe(2000);
	});
	it("shows the newest frame and says only a debug run can be taken over", () => {
		state.data = { run_id: "r1", live: true, service: "browser", at: "2026-11-22T10:00:00Z" };
		const html = renderToStaticMarkup(<LiveScreen runId="r1" />);
		expect(html).toContain('data-testid="live-frame"');
		expect(html).toContain("/api/bench/runs/r1/live/frame?at=2026-11-22T10:00:00Z");
		expect(html).toContain('data-testid="live-debug-only"');
		expect(html).not.toContain('data-testid="live-take-control"');
	});
	it("offers takeover in a debug run", () => {
		state.data = { run_id: "r1", live: true, service: "desktop", debug: true, takeover_ok: true };
		const html = renderToStaticMarkup(<LiveScreen runId="r1" />);
		expect(html).toContain('data-testid="live-take-control"');
		expect(html).toContain("Take control");
		expect(html).not.toMatch(/\b(arm|condition|probe|canary|sealed|lease|suite|runner)\b/i);
	});
	it("stops polling when inactive", () => {
		state.data = undefined;
		renderToStaticMarkup(<LiveScreen runId="r1" active={false} />);
		expect(state.polling).toBe(0);
	});
});
