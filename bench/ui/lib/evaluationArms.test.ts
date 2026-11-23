import { describe, expect, it } from "vitest";
import { evalArmLabel } from "./evaluationArms";

// The server's arm label rule, copied from evalArmIDRe in handlers/evaluations.go.
const SERVER_ARM_LABEL = /^[A-Z][A-Za-z0-9]{0,15}$/;

describe("evalArmLabel", () => {
	it("labels arms A, B, C ... and every label passes the server rule", () => {
		expect([0, 1, 2].map(evalArmLabel)).toEqual(["A", "B", "C"]);
		for (let i = 0; i < 6; i++) expect(evalArmLabel(i)).toMatch(SERVER_ARM_LABEL);
	});
});