import { describe, expect, it } from "vitest";
import { builderDirty } from "./matrixBuilder";

describe("builderDirty (M4-88)", () => {
	it("is false until a value differs from the defaults", () => {
		const d = { name: "compare-1", suite: "", taskCount: 5 } as never;
		expect(builderDirty(d, d)).toBe(false);
		expect(builderDirty({ ...(d as object), suite: "x@1" } as never, d)).toBe(true);
	});
});
