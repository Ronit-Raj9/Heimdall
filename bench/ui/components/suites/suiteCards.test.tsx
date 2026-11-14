import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SuiteCards } from "./suiteCards";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "./suiteFacts";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { suite: string }; children: React.ReactNode }) => (
		<a href={`${to}?suite=${search.suite}`} {...rest}>
			{children}
		</a>
	),
}));

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;
const card = (html: string, id: string) => html.match(new RegExp(`data-testid="suites-card-${id}"[\\s\\S]*?</button>`))?.[0] ?? "";

describe("SuiteCards", () => {
	const html = renderToStaticMarkup(<SuiteCards suites={list.suites} selected="coding-core" onSelect={() => undefined} />);

	it("presses the selected suite only", () => {
		expect(html.match(/aria-pressed="true"/g)).toHaveLength(1);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="suites-card-coding-core"/);
	});

	it("shows lock state, admitted count, drift and verifier format", () => {
		const core = card(html, "coding-core");
		expect(core).toMatch(/data-testid="suites-card-coding-core-lock" data-tone="good">.*Locked/);
		expect(core).toMatch(/data-testid="suites-card-coding-core-admitted" data-tone="good">4 of 4 tasks admitted/);
		expect(core).toContain("10 probes");
		const acme = card(html, "acme-payments");
		expect(acme).toMatch(/data-tone="warn">.*Lock is stale/);
		expect(acme).toContain("1 of 3 tasks admitted");
		expect(acme).toContain("1 change since lock");
		expect(card(html, "harbor-shell")).toContain(">Reward file<");
		expect(card(html, "harbor-shell")).toContain("digest not pinned");
	});

	it("shows a pack that failed to load with its error", () => {
		const broken = card(html, "acme-broken");
		expect(broken).toContain('data-state="error"');
		expect(broken).toContain("Failed to load");
		expect(broken).toContain("verifier.tests: required field missing");
	});
});

describe("suite facts", () => {
	it("links to the suite's leaderboard and shows the threshold of a reward suite", () => {
		const html = renderToStaticMarkup(<SuiteFacts suite={suite("harbor-shell")} />);
		expect(html).toContain('href="/leaderboard?suite=harbor-shell@0.1.0"');
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