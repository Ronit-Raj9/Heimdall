import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReplayFormFields, bifrostSourceNames, type ReplayForm } from "./replaysView";

vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ testId, value }: { testId?: string; value: { model: string } | null }) => (
		<div data-testid={testId} data-model={value?.model ?? ""} />
	),
}));


vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const form: ReplayForm = {
	source: "",
	window: "7d",
	sessions: "10",
	onlyFailed: false,
	mode: "fixed",
	candidate: "openai/gpt-4o-mini",
	judge: "",
	cap: "2",
	yes: true,
};

describe("shadow replay source picker", () => {
	it("offers only the Bifrost sources of the inbox", () => {
		expect(
			bifrostSourceNames([
				{ name: "prod", kind: "bifrost" },
				{ name: "hook", kind: "webhook" },
				{ name: "gw2", kind: "bifrost" },
			]),
		).toEqual(["prod", "gw2"]);
		expect(bifrostSourceNames(undefined)).toEqual([]);
	});

	it("is a picker of those names, not a text box", () => {
		const html = renderToStaticMarkup(
			<ReplayFormFields form={{ ...form, source: "gw2" }} onChange={() => undefined} sources={["prod", "gw2"]} />,
		);
		expect(html).toMatch(/<select[^>]*data-testid="replays-source"/);
		expect(html).toContain('<option value="prod">prod</option>');
		expect(html).toMatch(/<option value="gw2" selected="">gw2<\/option>/);
		expect(html).toContain("Pick a source");
		expect(html).not.toMatch(/<input[^>]*data-testid="replays-source"/);
	});

	it("says how to add a source when the inbox has none", () => {
		const html = renderToStaticMarkup(<ReplayFormFields form={form} onChange={() => undefined} sources={[]} />);
		expect(html).toContain("No recorded sessions yet. Connect Bifrost in the Failure inbox");
		expect(html).toContain('href="/inbox?tab=connect"');
		expect(html).toMatch(/<select[^>]*disabled=""/);
	});
});