import "@/lib/i18n";

import type { RunDetail } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, search, ...rest }: { children: unknown; search?: Record<string, unknown> }) => (
		<a data-search={JSON.stringify(search)} {...(rest as object)}>
			{children as never}
		</a>
	),
}));

const { RunSkillsBlock } = await import("./runSkills");

// The handler's JSON for GET /runs/{id} (handlers/runskills_test.go), the skills field only.
const read = JSON.parse(
	'{"skills":{"installed":[{"name":"bfcl-check","files":2,"read":true,"read_step":4,"read_path":"bfcl-check/SKILL.md"}]}}',
) as Pick<RunDetail, "skills">;
const unread = JSON.parse('{"skills":{"installed":[{"name":"bfcl-check","files":2,"read":false}]}}') as Pick<RunDetail, "skills">;
const none = JSON.parse('{"skills":{"installed":[]}}') as Pick<RunDetail, "skills">;

describe("RunSkillsBlock", () => {
	it("links the step that read the skill", () => {
		const html = renderToStaticMarkup(<RunSkillsBlock runId="r_1" skills={read.skills} />);
		expect(html).toContain("Skills in this run");
		expect(html).toContain("bfcl-check");
		expect(html).toContain("2 files");
		expect(html).toContain("Read at step 4");
		expect(html).toContain("&quot;step&quot;:4");
		expect(html).toContain("&quot;tab&quot;:&quot;timeline&quot;");
	});

	it("never claims a read without a step", () => {
		const html = renderToStaticMarkup(<RunSkillsBlock runId="r_1" skills={unread.skills} />);
		expect(html).toContain("No sign it was read");
		expect(html).not.toContain("Read at step");
	});

	it("says when the run installed none", () => {
		const html = renderToStaticMarkup(<RunSkillsBlock runId="r_1" skills={none.skills} />);
		expect(html).toContain("No skills installed in this run.");
	});

	it("renders nothing without a record", () => {
		expect(renderToStaticMarkup(<RunSkillsBlock runId="r_1" />)).toBe("");
	});
});
