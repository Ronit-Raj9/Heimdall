import { describe, expect, it } from "vitest";
import { evaluationCardUrl } from "./evaluationsApi";

describe("evaluationCardUrl (Q-121)", () => {
	it("asks the card route with the report's where filters", () => {
		expect(evaluationCardUrl("ev_1")).toMatch(/^\/evaluations\/ev_1\/card(\?|$)/);
		const url = evaluationCardUrl({ id: "ev 1", where: { lang: "go" } });
		expect(url).toContain("/evaluations/ev%201/card?");
		expect(url).toContain(`where=${encodeURIComponent("lang:go")}`);
	});
});
