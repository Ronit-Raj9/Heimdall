import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ProbeList } from "./suiteCoverage";

// Wave 4, N164: a benchmark with no built-in failure tests points to evaluations instead of
// saying it has none at all.

describe("ProbeList empty text", () => {
	it("no built-in failure tests points to evaluations", () => {
		const html = render(<ProbeList probes={[]} />);
		expect(html).toContain("No built-in failure tests.");
		expect(html).not.toContain("This benchmark has no failure tests.");
	});
});