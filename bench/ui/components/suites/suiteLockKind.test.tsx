import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { SuiteFacts } from "./suiteFacts";
import { FingerprintDriftNotice, LockKindBadge } from "./suiteLockKind";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { suite: string }; children: React.ReactNode }) => (
		<a href={`${to}?suite=${search.suite}`} {...rest}>
			{children}
		</a>
	),
}));

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;

describe("LockKindBadge", () => {
	it("shows a fingerprinted lock in the good tone", () => {
		const html = renderToStaticMarkup(<LockKindBadge kind="fingerprinted" testId="kind" />);
		expect(html).toContain('data-tone="good"');
		expect(html).toContain("Fingerprinted lock");
	});

	it("shows a legacy lock with the hint to add fingerprints", () => {
		const html = renderToStaticMarkup(<LockKindBadge kind="legacy" testId="kind" />);
		expect(html).toContain('data-tone="warn"');
		expect(html).toContain("Legacy lock");
		expect(html).toContain("Evaluator settings are not bound; run suite lock to add fingerprints.");
	});

	it("shows nothing for an unlocked suite", () => {
		expect(renderToStaticMarkup(<LockKindBadge kind={undefined} />)).toBe("");
	});

	it("sits beside the lock state in the suite facts", () => {
		expect(renderToStaticMarkup(<SuiteFacts suite={suite("coding-core")} />)).toMatch(
			/data-testid="suites-facts-lock-kind" data-tone="good">.*Fingerprinted lock/,
		);
		expect(renderToStaticMarkup(<SuiteFacts suite={{ ...suite("coding-core"), lock_kind: "legacy" }} />)).toMatch(
			/data-testid="suites-facts-lock-kind" data-tone="warn">.*Legacy lock/,
		);
		expect(renderToStaticMarkup(<SuiteFacts suite={suite("harbor-shell")} />)).not.toContain("suites-facts-lock-kind");
	});
});

describe("FingerprintDriftNotice", () => {
	it("lists each drift with its scope, id, part and fields", () => {
		const html = renderToStaticMarkup(<FingerprintDriftNotice suite={suite("acme-payments")} />);
		expect(html).toContain('role="alert"');
		expect(html).toContain("Lock fingerprint drift");
		expect(html).toMatch(/data-testid="suites-fingerprint-drift-suite-acme-payments-settings">suite acme-payments: settings \(verifier\)</);
		expect(html).toMatch(/data-testid="suites-fingerprint-drift-task-p02-files">task p02: files</);
	});

	it("shows nothing when nothing drifted or the lock is legacy", () => {
		expect(renderToStaticMarkup(<FingerprintDriftNotice suite={suite("coding-core")} />)).toBe("");
		expect(renderToStaticMarkup(<FingerprintDriftNotice suite={{ ...suite("coding-core"), lock_kind: "legacy" }} />)).toBe("");
	});
});