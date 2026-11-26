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