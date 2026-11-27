import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { VerdictPromote } from "./verdictTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("VerdictPromote (J7.promote)", () => {
	it("is a Promote this version button to the release form for two setups", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} />);
		const tag = tagOf(html, "verdict-promote") ?? "";
		expect(tag).toContain('href="/releases?exp=e1"');
		expect(tag).toMatch(/data-slot="button"|inline-flex/);
		expect(html).toContain("Promote this version");
	});
	it("opens the evaluation on a one-setup run of an evaluation", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A"]} evaluationId="ev_9" />);
		expect(tagOf(html, "verdict-promote")).toContain('href="/evaluations/ev_9"');
		expect(html).toContain("Promote this version");
	});
	it("is absent on one setup outside an evaluation", () => {
		expect(render(<VerdictPromote expId="e1" arms={["A"]} />)).not.toContain("verdict-promote");
	});
});
describe("VerdictPromote follows what differs between the setups (N165)", () => {
	it("offers the model, not a promote, on a model-only comparison B wins", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="model" decision="improved" modelB="gpt-6-luna" />);
		expect(html).not.toContain('data-testid="verdict-promote"');
		expect(html).toContain("Use model gpt-6-luna");
		expect(tagOf(html, "verdict-use-model") ?? "").toContain("tab=migration");
	});
	it("offers no model on a no-winner, same-cost comparison (M4-3)", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="model" decision="inconclusive" modelB="gpt-6-luna" />);
		expect(html).not.toContain("verdict-use-model");
		expect(html).not.toContain("verdict-check-switch");
	});
	it("offers a check of the switch when B is much cheaper at no clear difference (M4-3)", () => {
		const html = render(
			<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="model" decision="inconclusive" modelB="gpt-6-luna" cheaperB />,
		);
		expect(html).toContain("Check switching to gpt-6-luna");
		expect(tagOf(html, "verdict-check-switch") ?? "").toContain("tab=migration");
	});
	it("shows nothing on an A/A check", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="none" decision="inconclusive" />);
		expect(html).not.toContain("verdict-promote");
		expect(html).not.toContain("verdict-use-model");
	});
	it("promotes a winning agent version with no exception line", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="agent" decision="improved" />);
		expect(html).toContain('data-testid="verdict-promote"');
		expect(html).not.toContain("verdict-promote-exception");
	});
	it("warns that promoting without a clear winner is an exception", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} armsDiffer="agent" decision="inconclusive" />);
		expect(html).toContain('data-testid="verdict-promote"');
		expect(html).toContain("No clear winner yet. Promoting now records the release as an exception to your release rules.");
	});
});