import { describe, expect, it } from "vitest";
import { planForName } from "./inventoryPanel";
import type { OnboardPlan } from "@/lib/types/onboard";

const plan: OnboardPlan = { source: "s", suite: "old-id", held_out: { pct: 0, seed: 1 } };

describe("Name gives the benchmark id (M2-112c)", () => {
	it("derives the id from the Name until the id was typed by hand", () => {
		expect(planForName(plan, "My Support Set", false, [])).toMatchObject({ title: "My Support Set", suite: "my-support-set" });
		expect(planForName(plan, "My Support Set", true, [])).toMatchObject({ title: "My Support Set", suite: "old-id" });
	});
});
