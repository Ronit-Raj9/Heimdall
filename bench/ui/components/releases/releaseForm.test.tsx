import "@/lib/i18n";

import type { Action, ExperimentStatus, ReleasePolicyDecision } from "@/lib/types/bench";
import { SessionContext, type SessionInfo } from "@/lib/session/sessionContext";
import { demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// The form lists finished evaluations (M7-74); the rendering here has no store, so the list is stubbed.
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useListEvaluationsQuery: () => ({ data: [] }),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
import { ExperimentOption, ReleaseForm } from "./releaseForm";
import type { ReleaseFormValues } from "./releases.utils";

const experiment: ExperimentStatus = {
	exp_id: "e_synth_notes",
	name: "notes v2",
	state: "done",
	manifest_hash: "sha256:m",
	frozen_at: "2026-11-10T09:00:00Z",
	slots: { total: 4, done: 4, running: 0, failed: 0 },
	spent_usd: { state: "unknown", v: null, reason: "synthetic" },
};

function render(action: Action, policy: ReleasePolicyDecision | undefined, values: Partial<ReleaseFormValues>): string {
	return renderToStaticMarkup(
		<ReleaseForm
			experiments={[experiment]}
			expId="e_synth_notes"
			onExpChange={() => {}}
			verdict={{ analysisId: "a_synth001", decision: action === "ship" ? "improved" : "inconclusive", action }}
			policy={policy}
			initialValues={values}
			onSubmit={() => {}}
		/>,
	);
}

/** The release button's opening tag. */
function submitButton(html: string): string {
	const match = html.match(/<button[^>]*data-testid="release-submit"[^>]*>/);
	expect(match).not.toBeNull();
	return match![0];
}

describe("ReleaseForm", () => {
	const cases: {
		name: string;
		action: Action;
		policy?: ReleasePolicyDecision;
		values: Partial<ReleaseFormValues>;
		enabled: boolean;
		blocked: boolean;
	}[] = [
		{
			name: "ship and allow with an approver",
			action: "ship",
			policy: "allow",
			values: { approver: "maya" },
			enabled: true,
			blocked: false,
		},
		{ name: "ship with no policy", action: "ship", values: { approver: "maya" }, enabled: true, blocked: false },
		{ name: "ship without an approver", action: "ship", policy: "allow", values: {}, enabled: false, blocked: false },
		{ name: "hold", action: "hold", values: { approver: "maya" }, enabled: false, blocked: true },
		{ name: "ship but policy blocks", action: "ship", policy: "block", values: { approver: "maya" }, enabled: false, blocked: true },
		{
			name: "override ticked without a reason",
			action: "hold",
			values: { approver: "maya", override: true },
			enabled: false,
			blocked: true,
		},
		{
			name: "override with a reason",
			action: "ship",
			policy: "block",
			values: { approver: "maya", override: true, overrideReason: "hotfix" },
			enabled: true,
			blocked: true,
		},
	];
	for (const c of cases) {
		it(c.name, () => {
			const html = render(c.action, c.policy, c.values);
			expect(/ disabled=""/.test(submitButton(html))).toBe(!c.enabled);
			expect(html.includes('data-testid="release-override-checkbox"')).toBe(c.blocked);
			expect(html.includes('data-testid="release-blocked"')).toBe(c.blocked);
		});
	}

	it("shows the verdict action and policy decision it would cite", () => {
		const html = render("ship", "warn", { approver: "maya" });
		expect(html).toContain("a_synth001");
		expect(html).toMatch(/data-testid="release-verdict-action"[^>]*>ship</);
		expect(html).toMatch(/data-testid="release-verdict-policy"[^>]*>warn</);
	});

	it("names each blocker", () => {
		const html = render("no_decision", "block", { approver: "maya" });
		expect(html).toContain("Result is no_decision. Release needs ship.");
		expect(html).toContain("The release rules block this release.");
		expect(html).not.toContain('data-testid="release-override-reason"');
	});

	it("is disabled until a verdict loads", () => {
		const html = renderToStaticMarkup(
			<ReleaseForm
				experiments={[experiment]}
				expId="e_synth_notes"
				onExpChange={() => {}}
				verdictLoading
				initialValues={{ approver: "maya" }}
				onSubmit={() => {}}
			/>,
		);
		expect(submitButton(html)).toMatch(/ disabled=""/);
		expect(html).toContain('data-testid="release-verdict-loading"');
	});
});
describe("ReleaseForm in hosted mode", () => {
	const inSession = (session: SessionInfo) =>
		renderToStaticMarkup(
			<SessionContext.Provider value={session}>
				<ReleaseForm
					experiments={[experiment]}
					expId="e_synth_notes"
					onExpChange={() => {}}
					verdict={{ analysisId: "a_synth001", decision: "improved", action: "ship" }}
					policy={undefined}
					onSubmit={() => {}}
				/>
			</SessionContext.Provider>,
		);
	it("has no write-token box for a member; local mode keeps it", () => {
		expect(inSession(hostedSession("member"))).not.toContain("release-token-input");
		expect(render("ship", undefined, {})).toContain("release-token-input");
	});
	it("tells a viewer the role needed and shows a demo visitor no form", () => {
		expect(inSession(hostedSession("viewer"))).toContain('data-testid="release-form-role"');
		expect(inSession(demoSession())).toBe("");
	});
});
describe("ExperimentOption", () => {
	it("reads the human name with the id as grey secondary text", () => {
		const html = renderToStaticMarkup(
			<ExperimentOption experiment={{ ...experiment, name: "quick-20261121-101500-ab12", suite: "tb2@1.0.0", arms: ["A", "B"] }} />,
		);
		expect(html).toContain("Quick run on tb2");
		expect(html).toContain("text-muted-foreground");
		expect(html).toContain(">e_synth_notes<");
		expect(html).not.toContain("quick-20261121");
		const check = renderToStaticMarkup(<ExperimentOption experiment={{ ...experiment, name: "admit-acme-safety" }} />);
		expect(check).toContain("Readiness check for acme-safety");
	});
});
describe("ReleaseForm says why the button is off (N170)", () => {
	const form = (props: Partial<Parameters<typeof ReleaseForm>[0]>) =>
		renderToStaticMarkup(
			<ReleaseForm experiments={[experiment]} expId="e_synth_notes" onExpChange={() => {}} onSubmit={() => {}} {...props} />,
		);
	it("while the result loads", () => {
		const html = form({ verdictLoading: true });
		expect(html).toContain('data-testid="release-disabled-reason"');
		expect(html).toContain("Loading the comparison&#x27;s result...");
	});
	it("when nobody approves yet", () => {
		const html = form({ verdict: { analysisId: "a_1", decision: "improved", action: "ship" } });
		expect(html).toContain("Write who approves this release.");
	});
	it("when the result says not to release", () => {
		const html = form({ verdict: { analysisId: "a_1", decision: "inconclusive", action: "hold" }, initialValues: { approver: "ana" } });
		expect(html).toContain("Tick Release anyway and give a reason.");
	});
	it("not at all when the button is on", () => {
		const html = form({ verdict: { analysisId: "a_1", decision: "improved", action: "ship" }, initialValues: { approver: "ana" } });
		expect(html).not.toContain("release-disabled-reason");
	});
});