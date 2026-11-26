import { describe, expect, it } from "vitest";
import { render } from "@/lib/testing/render";
import { WhyDisabled, firstReason } from "@/components/ui/whyDisabled";

describe("firstReason (wave 4)", () => {
	it("gives the first non-empty string", () => {
		expect(firstReason(false, null, undefined, "  ", "need a name", "need a file")).toBe("need a name");
	});
	it("is null when there is no reason", () => {
		expect(firstReason(false, null, undefined, "")).toBeNull();
		expect(firstReason()).toBeNull();
	});
});

describe("WhyDisabled (wave 4)", () => {
	it("renders nothing without a reason", () => {
		expect(render(<WhyDisabled reason={null} testId="x-why" />)).toBe("");
		expect(render(<WhyDisabled reason="" testId="x-why" />)).toBe("");
	});
	it("renders the reason with its test id as the element id", () => {
		const html = render(<WhyDisabled reason="Enter a name first." testId="x-why" />);
		expect(html).toContain('data-testid="x-why"');
		expect(html).toContain('id="x-why"');
		expect(html).toContain("Enter a name first.");
	});
});