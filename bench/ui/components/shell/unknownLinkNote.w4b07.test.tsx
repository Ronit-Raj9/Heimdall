import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { UnknownLinkNote, isUnknownName } from "./unknownLinkNote";

// Wave 4, N76: the shared check and note for a link that names something that does not exist.

describe("isUnknownName", () => {
	it("is false while the list loads or when there is no name", () => {
		expect(isUnknownName(undefined, "x")).toBe(false);
		expect(isUnknownName(["a"], null)).toBe(false);
		expect(isUnknownName(["a"], undefined)).toBe(false);
		expect(isUnknownName(["a"], "")).toBe(false);
	});

	it("is true only for a name not in the loaded list", () => {
		expect(isUnknownName(["a", "b"], "b")).toBe(false);
		expect(isUnknownName(["a", "b"], "c")).toBe(true);
		expect(isUnknownName([], "c")).toBe(true);
	});
});

describe("UnknownLinkNote", () => {
	it("shows the text as a status, with Show all only when given", () => {
		const bare = render(<UnknownLinkNote text="No benchmark named x was found." testId="unknown-link-benchmark" />);
		expect(bare).toContain('role="status"');
		expect(bare).toContain('data-testid="unknown-link-benchmark"');
		expect(bare).toContain("No benchmark named x was found.");
		expect(bare).not.toContain("unknown-link-benchmark-show-all");
		const full = render(<UnknownLinkNote text="t" showAllLabel="Show all" onShowAll={() => undefined} testId="unknown-link-benchmark" />);
		expect(full).toContain('data-testid="unknown-link-benchmark-show-all"');
		expect(full).toContain("Show all");
	});
});