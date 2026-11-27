import i18n from "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MappingStep } from "./rowMapper";

const t = ((k: string) => i18n.t(k as never, { ns: "suiteBuild" } as never)) as never;

describe("MappingStep help (p10-2)", () => {
	it("shows the help the caller gives", () => {
		const help = "Choose which column feeds each part of a task. The mapping is used when you press Draft tasks.";
		const html = renderToStaticMarkup(<MappingStep columns={[]} mapping={{}} onChange={() => {}} showGradeOn={false} t={t} help={help} />);
		expect(html).toContain(help);
		expect(html).not.toContain("refreshes");
	});
});
describe("MappingStep columns (M3-60)", () => {
	it("shows Question and Expected answer, the rest under More columns", async () => {
		const { splitMapFields } = await import("./rowMapper");
		expect(splitMapFields(["question", "messages", "expected", "id"])).toEqual({ main: ["question", "expected"], more: ["messages", "id"] });
		const html = renderToStaticMarkup(<MappingStep columns={[]} mapping={{}} onChange={() => {}} showGradeOn={false} t={t} />);
		const more = html.indexOf('data-testid="suite-build-map-more"');
		expect(more).toBeGreaterThan(html.indexOf('data-testid="suite-build-map-question"'));
		expect(more).toBeGreaterThan(html.indexOf('data-testid="suite-build-map-expected"'));
		expect(html.indexOf('data-testid="suite-build-map-messages"')).toBeGreaterThan(more);
		expect(html).toContain("More columns");
	});
});
