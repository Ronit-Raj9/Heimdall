import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { RunDetail } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { RunDebugActions } from "./debugActions";
import en from "@/locales/en/runDebug.json";

const trigger = vi.fn();
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/runDebugApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/runDebugApi")>()),
	useStartDebugRunMutation: () => [trigger, { isLoading: false, error: undefined }],
}));

const detail = (sealed: boolean, allowed: RunDetail["debug_allowed"]) =>
	({ sealed, slot: { exp_id: "e_1", condition: "clean", arm: "A", task: "t1" }, run: { run_id: "r_1" }, debug_allowed: allowed }) as unknown as RunDetail;

describe("Replay asks first (M5-2)", () => {
	it("renders Replay without starting anything and without the dialog open", () => {
		const html = render(<RunDebugActions detail={detail(true, { replay: true, fork: true })} />);
		expect(html).toContain("run-replay");
		expect(html).not.toContain("replay-dialog");
		expect(trigger).not.toHaveBeenCalled();
	});

	it("the confirm text says what it does, that it is free, never counted and creates a debug run", () => {
		expect(en.actions.replayConfirm).toContain("costs nothing");
		expect(en.actions.replayConfirm).toContain("never counts toward results");
		expect(en.actions.replayConfirm).toContain("creates a new debug run");
		expect(en.actions.replayStart).toBe("Start replay");
		expect(en.actions.replayCancel).toBe("Cancel");
	});
});

describe("worker note (M5-14)", () => {
	const allowed = { replay: true, fork: true, breakpoints: false };
	it("is a line only on a live run; on a finished run it moves into the Replay tooltip", () => {
		const done = render(<RunDebugActions detail={detail(true, allowed)} />);
		expect(done).not.toContain("run-debug-breakpoints-reason");
		expect(done).toContain("Pausing before model calls needs a worker");
		const live = render(<RunDebugActions detail={detail(false, allowed)} />);
		expect(live).toContain("run-debug-breakpoints-reason");
	});
});
