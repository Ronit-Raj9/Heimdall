import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SuiteCards } from "./suiteCards";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "./suiteFacts";

vi.mock("@/components/suites/suiteRowDelete", () => ({ SuiteRowDelete: () => null })); // row Delete has its own test (suiteRowDelete.iwid.test.tsx)
vi.mock("@tanstack/react-router", () => ({
	Link: ({
		to,
		params,
		search,
		children,
		...rest
	}: {
		to: string;
		params?: Record<string, string>;
		search?: Record<string, string>;
		children: React.ReactNode;
	}) => {
		let href = to;
		for (const [key, value] of Object.entries(params ?? {})) href = href.replace(`$${key}`, value);
		const qs = new URLSearchParams(search ?? {}).toString();
		return (
			<a href={qs ? `${href}?${qs}` : href} {...rest}>
				{children}
			</a>
		);
	},
}));

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;
const card = (html: string, id: string) => html.match(new RegExp(`data-testid="suites-row-${id}"[\\s\\S]*?</li>`))?.[0] ?? "";

describe("SuiteCards", () => {
	const html = renderToStaticMarkup(<SuiteCards suites={list.suites} />);

	it("links every row to its benchmark's page by a human name, with the id only in the hover title", () => {
		expect(html).toMatch(
			/href="\/suites\/coding-core"[^>]*title="coding-core@[^"]*"[^>]*data-testid="suites-row-coding-core-link"[^>]*>Coding basics</,
		);
		expect(card(html, "coding-core")).toContain("Fix small Python projects so their hidden tests pass.");
		expect(card(html, "coding-core")).not.toContain("font-mono");
		// C1.name: a benchmark the team added with no curated name reads as its id, as typed.
		expect(card(html, "acme-payments")).toMatch(/>acme-payments</);
	});

	it("shows readiness as a dot and words, the task count and a quiet Run, and hides engineering detail", () => {
		const core = card(html, "coding-core");
		expect(core).toMatch(/data-testid="suites-row-coding-core-status" data-status="ready">.*Ready/);
		expect(core).toContain("4 tasks");
		expect(core).toContain('data-testid="suites-row-coding-core-run"');
		expect(core).toContain("md:inline-flex"); // M6-44: Run shows without hover from md up
		const acme = card(html, "acme-payments");
		expect(acme).toMatch(/data-status="needsAdmission">.*1 of 3 tasks ready/);
		expect(acme).not.toContain("task init");
		expect(card(html, "harbor-shell")).not.toContain("Reward file");
		expect(card(html, "harbor-shell")).not.toContain("test.sh");
		expect(core).not.toContain("probes");
	});

	it("shows a pack that failed to load with its error and no Run button", () => {
		const broken = card(html, "acme-broken");
		expect(broken).toContain('data-state="error"');
		expect(broken).toContain("Could not load");
		expect(broken).toContain("verifier.tests: required field missing");
		expect(broken).not.toContain("-run");
	});

	it("puts no colored icon tile on any row", () => {
		expect(html).not.toContain("data-track=");
	});
});

describe("suite facts", () => {
	it("links to the suite's leaderboard and shows the threshold of a reward suite", () => {
		const html = renderToStaticMarkup(<SuiteFacts suite={suite("harbor-shell")} />);
		expect(html).toContain('href="/leaderboard?suite=harbor-shell%400.1.0"');
		expect(html).toMatch(/data-testid="suites-fact-threshold".*?>1</);
		expect(renderToStaticMarkup(<SuiteFacts suite={suite("coding-core")} />)).not.toContain("suites-fact-threshold");
	});

	it("lists drift with the re-lock commands, and nothing when the lock matches", () => {
		const html = renderToStaticMarkup(<SuiteDriftNotice suite={suite("acme-payments")} />);
		expect(html).toContain('data-testid="suites-drift-task-p02"');
		expect(html).toContain("task p02: repo_tree_sha256, status");
		expect(html).toContain("heimdall suite lock acme-payments\nheimdall suite admit acme-payments");
		expect(renderToStaticMarkup(<SuiteDriftNotice suite={suite("coding-core")} />)).toBe("");
	});

	it("shows a broken pack's error as text with the lint command", () => {
		const html = renderToStaticMarkup(<BrokenSuite suite={suite("acme-broken")} />);
		expect(html).toContain('role="alert"');
		expect(html).toContain("heimdall suite lint acme-broken");
	});
});
describe("scenario facts", () => {
	it("shows track, grading, output and services for every suite", () => {
		const html = renderToStaticMarkup(<SuiteFacts suite={suite("support-desk")} />);
		expect(html).toMatch(/data-testid="suites-fact-track".*?>Tool use</);
		expect(html).toMatch(/data-testid="suites-fact-grading".*?data-tone="good".*?>Verified</);
		expect(html).toMatch(/data-testid="suites-fact-output".*?>State</);
		expect(html).toMatch(/data-testid="suites-fact-services".*?>crm</);
		expect(html).not.toContain("suites-judged-note");
		expect(html).not.toContain("suites-imported");
		const core = renderToStaticMarkup(<SuiteFacts suite={suite("coding-core")} />);
		expect(core).toMatch(/data-testid="suites-fact-output".*?>Diff</);
		expect(core).toMatch(/data-testid="suites-fact-services".*?>none</);
	});

	it("explains a judged suite in plain words and shows where an imported suite came from", () => {
		const html = renderToStaticMarkup(<SuiteFacts suite={suite("memory-recall")} />);
		expect(html).toMatch(/data-testid="suites-fact-grading".*?data-tone="warn".*?>Judged</);
		expect(html).toMatch(/data-testid="suites-judged-note"/);
		expect(html).toContain("a judge model decides whether each answer is correct");
		expect(html).toContain("never ranked against, or compared with, a verified benchmark");
		expect(html).toMatch(/data-testid="suites-imported".*?recall-example@1/);
	});
});