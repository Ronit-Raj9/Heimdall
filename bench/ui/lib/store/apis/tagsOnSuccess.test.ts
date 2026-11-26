import { describe, expect, it } from "vitest";
import { tagsOnSuccess } from "./tagsOnSuccess";

describe("tagsOnSuccess", () => {
	const tags = tagsOnSuccess((_r: { ok: boolean }, arg: string) => [arg]);

	it("returns the tags after a successful save", () => {
		expect(tags({ ok: true }, undefined, "a")).toEqual(["a"]);
	});

	it("returns no tags when the save failed", () => {
		expect(tags(undefined, { status: 409 }, "a")).toEqual([]);
		expect(tags({ ok: true }, { status: 409 }, "a")).toEqual([]);
	});
});