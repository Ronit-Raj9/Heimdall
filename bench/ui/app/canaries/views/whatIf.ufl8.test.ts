import { canarySimulateRequest } from "@/lib/store/apis/canarySimulateApi";
import { describe, expect, it } from "vitest";
import { whatIfBody } from "./whatIf";

describe("what if", () => {
	it("posts the body TestCanarySimulateFromWebBody sends", () => {
		const body = whatIfBody({ tasks: ["a", "b", "c", "d", "e", "f", "g", "h"], trials: 1, sensitivity: "normal" }, "", "20");
		const r = canarySimulateRequest(body!);
		expect(r).toMatchObject({ url: "/canaries/simulate", method: "POST" });
		expect(JSON.stringify(r.body)).toBe('{"tasks":8,"trials":1,"sensitivity":"normal","drop":0.2}');
	});
	it("uses the typed task count for all tasks, refuses out of range", () => {
		expect(whatIfBody({ tasks: "all", trials: 2 }, "12", "0")).toEqual({ tasks: 12, trials: 2 });
		expect(whatIfBody({ tasks: "all", trials: 1 }, "0", "10")).toBeNull();
		expect(whatIfBody({ tasks: "all", trials: 1 }, "8", "150")).toBeNull();
	});
});