import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import type { RunDetail } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { RunDebugActions, canFork, canReplay } from "./debugActions";
import { DebugBanner } from "./debugBanner";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/runDebugApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/runDebugApi")>()),
	useStartDebugRunMutation: () => mutation(),
}));

const detail = (condition: string, allowed?: RunDetail["debug_allowed"]) =>
	({
		sealed: true,
		slot: { exp_id: "e_1", condition, arm: "A", task: "t1" },
		run: { run_id: "r_1" },
		debug_allowed: allowed,
	}) as unknown as RunDetail;

describe("debug actions of a fault run (N133)", () => {
	it("offers Replay for a model-fault run, hides Debug and says why", () => {
		const d = detail("heimdall-model-brownout", { replay: true, fork: false, reason: "x" });
		expect(canReplay(d)).toBe(true);
		expect(canFork(d)).toBe(false);
		const html = render(<RunDebugActions detail={d} />);
		expect(html).toContain("run-replay");
		expect(html).not.toContain('data-testid="run-debug"');
		expect(html).toContain("Fork and Debug need a run without a fault. This run ran under Model partly down.");
	});
	it("offers nothing for a tool-fault run and says why", () => {
		const d = detail("heimdall-broken-tool", { replay: false, fork: false, reason: "x" });
		const html = render(<RunDebugActions detail={d} />);
		expect(html).not.toContain("run-replay");
		expect(html).not.toContain('data-testid="run-debug"');
		expect(html).toContain("run-debug-reason");
		expect(html).toContain("Broken tool");
	});
	it("offers everything for a clean run, with no reason line", () => {
		const html = render(<RunDebugActions detail={detail("clean", { replay: true, fork: true })} />);
		expect(html).toContain("run-replay");
		expect(html).toContain('data-testid="run-debug"');
		expect(html).not.toContain("run-debug-reason");
	});
	it("the replay banner names the source run's fault", () => {
		const html = render(<DebugBanner debug={{ mode: "replay", from_run: "r_0", from_condition: "heimdall-model-brownout" }} />);
		expect(html).toContain("The source run ran under Model partly down: its fault answers are replayed as recorded.");
	});
});