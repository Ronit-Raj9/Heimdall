import "@/lib/i18n";

import { hostedSession, signedOutSession } from "@/lib/testing/platformFixtures";
import type { SessionInfo } from "@/lib/session/sessionContext";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	session: undefined as unknown as SessionInfo,
	suitesArg: [] as unknown[],
	lightSuites: [] as unknown[],
}));

vi.mock("@/lib/session/sessionContext", async (orig) => ({
	...(await orig<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => state.session,
}));
vi.mock("@/lib/store", () => ({
	useGetSuitesQuery: (arg: unknown) => {
		state.suitesArg.push(arg);
		return { data: { suites: [] }, error: undefined, isLoading: false, refetch: () => undefined };
	},
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { suites: state.lightSuites }, error: undefined, isError: false, isLoading: false, refetch: () => undefined }),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/shell", () => ({ PageHeader: ({ title }: { title: string }) => <h1>{title}</h1> }));

const { default: QuickstartView, isSignedOutVisitor, QUICKSTART_LOGIN_HREF } = await import("./quickstartView");

describe("QuickstartView (G2.quickstart)", () => {
	beforeEach(() => {
		state.suitesArg = [];
		state.lightSuites = [];
	});

	it("shows a signed-out visitor the steps and a sign-in button, and never asks for suites", () => {
		state.session = signedOutSession();
		expect(isSignedOutVisitor(state.session)).toBe(true);
		const html = renderToStaticMarkup(<QuickstartView />);
		expect(html).toContain('data-testid="quickstart-steps"');
		expect(html).toContain('data-testid="quickstart-signed-out"');
		expect(html).toContain("Sign in to start");
		expect(html).toContain(`href="${QUICKSTART_LOGIN_HREF.replace(/&/g, "&amp;")}"`);
		expect(html).not.toContain('data-testid="quickstart-no-mined"');
		expect(state.suitesArg.every((arg) => typeof arg === "symbol")).toBe(true);
	});

	it("is unchanged for a signed-in user: the mined tasks, from the full suites call when the light list lacks the facts", () => {
		state.session = hostedSession("viewer");
		state.lightSuites = [{ id: "bfcl", tasks: [] }];
		expect(isSignedOutVisitor(state.session)).toBe(false);
		const html = renderToStaticMarkup(<QuickstartView />);
		expect(html).not.toContain('data-testid="quickstart-signed-out"');
		expect(html).toContain('data-testid="quickstart-no-mined"');
		expect(state.suitesArg).toEqual([undefined]);
	});

	it("offers the website path: Evaluate and a model key link (M2-17)", () => {
		state.session = hostedSession("member");
		const html = renderToStaticMarkup(<QuickstartView />);
		expect(html).toContain('data-testid="quickstart-web-evaluate"');
		expect(html).toContain('data-testid="quickstart-web-keys"');
		expect(html).toContain('data-testid="quickstart-step-install"');
	});

	it("reads the mined facts from the light list and skips the full list (M2-20)", () => {
		state.session = hostedSession("viewer");
		state.lightSuites = [{ id: "bfcl", task_count: 3, mined_tasks: 2, mined_admitted_tasks: 1, unreviewed_tasks: [{ id: "c01", title: "Fix it" }] }];
		const html = renderToStaticMarkup(<QuickstartView />);
		expect(html).toContain('data-testid="quickstart-unreviewed-c01"');
		expect(state.suitesArg.every((arg) => typeof arg === "symbol")).toBe(true);
	});
});
