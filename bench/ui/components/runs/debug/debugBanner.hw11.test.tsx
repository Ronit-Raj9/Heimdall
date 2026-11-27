import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { StepRecord } from "@/lib/types/bench";
import type { RunCall } from "@/lib/types/runDebug";
import type { FC } from "react";
import { describe, expect, it, vi } from "vitest";
import * as banner from "./debugBanner";
import * as badge from "./replayBadge";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// Fix wave 3, N247: the fork banner says what the operator changed and links that step; the trace
// chip of the forked call says what changed, at every width.

const calls: RunCall[] = [
	{ call: 1, request_id: "q1", status: 200, action: "pass", replay: "edited" },
	{ call: 2, request_id: "q2", status: 200, action: "pass" },
];
const steps = [
	{ seq: 1, kind: "agent_note", request_id: "", summary: "" },
	{ seq: 2, kind: "model_turn", request_id: "q1", summary: "" },
	{ seq: 3, kind: "tool_request", request_id: "q1", summary: "" },
] as unknown as StepRecord[];

describe("fork banner (N247)", () => {
	const view = (banner as Record<string, unknown>).DebugBannerView as
		| FC<{ debug: object; runId?: string; calls?: RunCall[]; steps?: StepRecord[] }>
		| undefined;
	it("says the message was added before the fork call and offers Show that step", () => {
		expect(view).toBeTypeOf("function");
		const View = view!;
		const html = render(
			<View debug={{ mode: "fork", from_run: "r_n697", call: 1, edit: "message" }} runId="r_fork" calls={calls} steps={steps} />,
		);
		expect(html).toContain("Fork of run r_n697: your message was added before model call 1.");
		expect(html).toContain('data-testid="debug-banner-show-step"');
		expect(html).toContain("Show that step");
		expect(html).toContain("/runs/r_fork?tab=timeline&amp;step=2");
	});
	it("says the answer was replaced, or that nothing changed", () => {
		const View = view!;
		expect(render(<View debug={{ mode: "fork", from_run: "r_0", call: 3, edit: "response" }} />)).toContain(
			"Fork of run r_0: the model&#x27;s answer at call 3 was replaced.",
		);
		expect(
			render(<View debug={{ mode: "fork", from_run: "r_0", call: 2 }} calls={[{ call: 2, status: 200, action: "pass" }]} />),
		).toContain("Fork of run r_0 from model call 2, unchanged");
	});
	it("keeps the plain sentence for an old fork whose edit is not known", () => {
		const html = render(<banner.DebugBanner debug={{ mode: "fork", from_run: "r_0", call: 4 }} />);
		expect(html).toContain("Fork of run r_0 from call 4");
		expect(html).not.toContain("debug-banner-show-step");
	});
});

describe("forked call chip (N247)", () => {
	it("reads Message added here or Answer replaced here", () => {
		const B = badge.ReplayBadge as unknown as FC<{ label: string; edit?: string; testId?: string }>;
		expect(render(<B label="edited" edit="message" />)).toContain("Message added here");
		expect(render(<B label="edited" edit="response" />)).toContain("Answer replaced here");
		expect(render(<B label="edited" />)).toContain("Edited");
	});
	it("is shown outside the row's narrow-screen hidden chips", async () => {
		const { TraceView } = await import("../traceView");
		const html = render(
			<TraceView data={{ run_id: "r_fork", available: true, observers: [], steps: [steps[1]] } as never} arm="A" calls={calls} />,
		);
		const chip = html.indexOf('data-testid="trace-row-A-2-replay"');
		const hidden = html.indexOf("hidden shrink-0 items-center gap-1.5 sm:flex");
		expect(chip).toBeGreaterThan(-1);
		expect(hidden).toBeGreaterThan(-1);
		expect(chip).toBeLessThan(hidden);
	});
});