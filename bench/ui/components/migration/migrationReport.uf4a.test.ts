import { describe, expect, it } from "vitest";
import { anyKnown } from "./migrationReport";

// M4-29: pass^k shows only when some setup has it.
describe("anyKnown", () => {
	it("needs one observed value", () => {
		expect(anyKnown([{ state: "unknown", v: null } as never, undefined])).toBe(false);
		expect(anyKnown([{ state: "observed", v: 0.5 } as never])).toBe(true);
	});
});