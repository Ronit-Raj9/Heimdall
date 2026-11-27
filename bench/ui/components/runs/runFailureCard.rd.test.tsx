import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { failureGroupLink } from "./runFailureCard";

describe("the failure group link under the cause card (R3-1)", () => {
	it("counts the evaluation's runs when it holds others", () => {
		expect(failureGroupLink({ signature: "c_1", text: "x", count: 3, exp_id: "e1" } as never)).toEqual({
			count: 3,
			search: { tab: "clusters", sig: "c_1", exp: "e1" },
		});
	});

	it("still opens the group across evaluations when this evaluation holds only this run", () => {
		expect(failureGroupLink({ signature: "c_9f", text: "x", count: 1, exp_id: "e1" } as never)).toEqual({
			search: { tab: "groups", sig: "c_9f" },
		});
	});

	it("has no link without a signature", () => {
		expect(failureGroupLink(undefined)).toBeUndefined();
		expect(failureGroupLink({ signature: "", text: "", count: 4 } as never)).toBeUndefined();
	});
});
