import { describe, expect, it } from "vitest";
import type { ConnectCandidate } from "@/lib/types/connect";
import { withAnswerPath } from "./connect.utils";

describe("withAnswerPath (wave 4, p02-3)", () => {
	it("keeps the pick question so the choice can be changed", () => {
		const c = {
			kind: "http",
			label: "x",
			questions: [{ kind: "pick_field", param: "answer_path", label: "f", options: ["a.b", "c"] }],
		} as unknown as ConnectCandidate;
		const out = withAnswerPath(c, "a.b");
		expect(out.mapping?.answer_path).toBe("a.b");
		expect(out.questions?.some((q) => q.kind === "pick_field")).toBe(true);
	});
});