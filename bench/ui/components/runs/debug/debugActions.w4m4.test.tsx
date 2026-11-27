import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { RunDetail } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { DebugFaultReason } from "./debugActions";

describe("DebugFaultReason on a replay run", () => {
	it("says the run is itself a replay, not that it ran under no fault", () => {
		const d = {
			sealed: true,
			slot: { exp_id: "e_1", condition: "clean", arm: "A", task: "t1" },
			run: { run_id: "r_2", debug: { mode: "replay", from_run: "r_1" } },
			debug_allowed: { replay: false, fork: false },
		} as unknown as RunDetail;
		const html = render(<DebugFaultReason detail={d} />);
		expect(html).toContain("This run is a replay. Open its source run to replay again.");
		expect(html).not.toContain("No fault");
	});
});