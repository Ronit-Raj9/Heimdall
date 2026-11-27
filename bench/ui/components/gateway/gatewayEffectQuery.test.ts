import { describe, expect, it } from "vitest";
import { gatewayEffectPath } from "./gatewayEffectQuery";

describe("gatewayEffectPath", () => {
	it("asks for the optional answer and encodes the id", () => {
		expect(gatewayEffectPath("exp 1")).toBe("/experiments/exp%201/gateway-effect?optional=1");
	});
});
