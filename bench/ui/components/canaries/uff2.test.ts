import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import type { CanarySummary, SuiteSummary } from "@/lib/types/bench";
import { suiteTaskPick } from "@/app/canaries/views/newCanaryForm";
import { steadyWords } from "./canaryList";
import { currentZone, howOftenOf, zoneOptions } from "./howOften";

// Lane F2 (PT-3, PH5-2, PH5-5, PH5-7).
describe("drift monitor and zone rules (lane F2)", () => {
	it("sends the current zone name for an old one the browser reports", () => {
		expect(currentZone("Asia/Calcutta")).toBe("Asia/Kolkata");
		expect(currentZone("Asia/Kolkata")).toBe("Asia/Kolkata");
		expect(currentZone("UTC")).toBe("UTC");
		expect(howOftenOf("0 6 * * *", "Asia/Calcutta").timezone).toBe("Asia/Kolkata");
		expect(zoneOptions("Asia/Calcutta")).toContain("Asia/Calcutta");
	});

	it("offers only ready tasks and counts the hidden ones", () => {
		const suite = { id: "b", version: "1", tasks: [{ id: "r1", admitted: true }, { id: "n1", admitted: false }, { id: "n2", admitted: false }] } as unknown as SuiteSummary;
		expect(suiteTaskPick([suite], "b@1")).toEqual({ ready: ["r1"], hidden: 2 });
	});

	it("never says a plain No change before a check or on too few runs", () => {
		const base = { state: "steady", looks: 0, last_look: null } as unknown as CanarySummary;
		expect(steadyWords(base)).toBe("noCheckYet");
		expect(steadyWords({ ...base, looks: 1, last_look: { kind: "run", runs: 2 } } as unknown as CanarySummary)).toBe("tooFew");
		expect(steadyWords({ ...base, looks: 1, last_look: { kind: "run", runs: 40 } } as unknown as CanarySummary)).toBeNull();
		expect(steadyWords({ ...base, state: "drifted" } as CanarySummary)).toBeNull();
	});
});
