import "@/lib/i18n";

import type { SuiteTaskDetail } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { TaskDetailBody } from "./taskDetailPanel";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { suite: string }; children: React.ReactNode }) => (
		<a href={`${to}?suite=${search.suite}`} {...rest}>
			{children}
		</a>
	),
}));

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");
const detail = (task: string) => JSON.parse(fs.readFileSync(path.join(FIXTURES, `suites.task.${task}.json`), "utf8")) as SuiteTaskDetail;
const render = (task: string) => {
	const d = detail(task);
	return renderToStaticMarkup(<TaskDetailBody detail={d} suiteVersion={d.suite_version} />);
};
const block = (html: string, testId: string) => html.match(new RegExp(`data-testid="${testId}"[\\s\\S]*?</section>`))?.[0] ?? "";

describe("TaskDetailBody", () => {
	it("renders the instruction as plain text, never as HTML", () => {
		const html = render("p01");
		expect(html).not.toContain("<script>");
		expect(html).toContain("&lt;script&gt;alert(&#x27;this tag is shown as text, never run&#x27;)&lt;/script&gt;");
		expect(block(html, "suites-task-instruction")).toContain("<pre");
	});

	it("shows provenance and flags a missing license", () => {
		expect(render("p01")).toMatch(/data-testid="suites-provenance-license".*?proprietary/);
		expect(render("h02")).toMatch(/data-testid="suites-provenance-license".*?text-chart-warning-ink[^>]*>No license recorded/);
		expect(render("t01")).toContain("This task records no provenance.");
	});

	it("lists every contract control with pass, fail or not recorded", () => {
		const admitted = render("t01");
		expect(admitted).toMatch(/data-testid="suites-control-reference" data-state="pass"/);
		expect(admitted).toMatch(/data-testid="suites-control-null_agent" data-state="not_recorded"/);
		expect(admitted).toContain("3 of 3 passed");
		expect(admitted).toContain("tests_ran=7 failures=7 errors=0 skipped=0 exit=1");
		const draft = render("p03");
		expect(draft).toMatch(/data-testid="suites-control-mutant" data-state="fail"/);
		expect(draft).toMatch(/data-testid="suites-control-network" data-state="pass"/);
		expect(render("h01")).toContain("Admission has not run for this task version.");
	});

	it("counts hidden tests without naming them and lists visible files and protected globs", () => {
		const html = render("t01");
		expect(html).toContain("Hidden test files: 2. Required test ids: 7.");
		expect(block(html, "suites-task-protected")).toContain("tests/**");
		expect(block(html, "suites-task-files")).toContain("ledger/invoice.py");
		expect(block(html, "suites-task-files")).toContain("4.1 KB");
		expect(html).toContain('href="/leaderboard?suite=coding-core@1.0.0"');
	});

	it("says when the file list is cut", () => {
		const d = { ...detail("t01"), files_total: 900, files_truncated: true };
		const html = renderToStaticMarkup(<TaskDetailBody detail={d} suiteVersion="1.0.0" />);
		expect(html).toContain(`Showing the first ${d.files.length} of 900 files.`);
	});
});
describe("TaskDetailBody scenario", () => {
	it("shows the scenario block before the instruction for a scenario task", () => {
		const d = detail("d01");
		const html = renderToStaticMarkup(<TaskDetailBody detail={d} suiteVersion={d.suite_version} suiteOutput="state" />);
		expect(html.indexOf('data-testid="suites-task-scenario"')).toBeGreaterThan(-1);
		expect(html.indexOf('data-testid="suites-task-scenario"')).toBeLessThan(html.indexOf('data-testid="suites-task-instruction"'));
		expect(html).toContain("bench/task-support-desk-d01:9f2c4a1b7e30");
	});

	it("leaves the block out for a plain repository task", () => {
		expect(render("t01")).not.toContain("suites-task-scenario");
	});
});