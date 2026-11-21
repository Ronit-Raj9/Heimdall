import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SuiteCards } from "./suiteCards";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "./suiteFacts";

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
const card = (html: string, id: string) => html.match(new RegExp(`data-testid="suites-card-${id}"[\\s\\S]*?</article>`))?.[0] ?? "";

describe("SuiteCards", () => {
	const html = renderToStaticMarkup(<SuiteCards suites={list.suites} />);

	it("links every card to its suite's page by a readable name", () => {
		expect(html).toMatch(/href="\/suites\/coding-core"[^>]*data-testid="suites-card-coding-core-link"[^>]*>Coding core</);
	});

	it("shows one status pill and the task count, and hides engineering detail", () => {
		const core = card(html, "coding-core");
		expect(core).toMatch(/data-testid="suites-card-coding-core-status" data-status="ready">.*Ready/);
		expect(core).toContain("4 tasks");
		expect(core).toContain('data-testid="suites-card-coding-core-run"');
		const acme = card(html, "acme-payments");
		expect(acme).toMatch(/data-status="needsAdmission">.*2 tasks not ready yet/);
		expect(card(html, "harbor-shell")).not.toContain("Reward file");
		expect(card(html, "harbor-shell")).not.toContain("digest not pinned");
		expect(core).not.toContain("probes");
	});

	it("shows a pack that failed to load with its error and no Run button", () => {
		const broken = card(html, "acme-broken");
		expect(broken).toContain('data-state="error"');
		expect(broken).toContain("Could not load");
		expect(broken).toContain("verifier.tests: required field missing");
		expect(broken).not.toContain("-run");
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
		expect(html).toContain("bifrost-bench suite lock acme-payments\nbifrost-bench suite admit acme-payments");
		expect(renderToStaticMarkup(<SuiteDriftNotice suite={suite("coding-core")} />)).toBe("");
	});

	it("shows a broken pack's error as text with the lint command", () => {
		const html = renderToStaticMarkup(<BrokenSuite suite={suite("acme-broken")} />);
		expect(html).toContain('role="alert"');
		expect(html).toContain("bifrost-bench suite lint acme-broken");
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
		expect(html).toContain("never ranked against, or compared with, a verified suite");
		expect(html).toMatch(/data-testid="suites-imported".*?recall-example@1/);
	});

	it("puts a category icon on every card", () => {
		const html = renderToStaticMarkup(<SuiteCards suites={list.suites} />);
		expect(card(html, "coding-core")).toContain('data-track="coding"');
		expect(card(html, "harbor-shell")).toContain('data-track="terminal"');
	});
});
