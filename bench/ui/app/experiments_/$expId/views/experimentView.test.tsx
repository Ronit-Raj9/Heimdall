import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import ExperimentView from "./experimentView";

// QC2-28-04: while a new experiment route loads, RTK hands back the previous experiment's status as
// data. The screen must not render that status, or its Start action, under the new id.

const api = vi.hoisted(() => ({ status: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({ ...(await importOriginal<typeof import("nuqs")>()), useQueryState: () => [null, () => undefined] }));
vi.mock("@/lib/store", () => ({ useAppDispatch: () => () => undefined }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetExperimentQuery: () => api.status,
	useGetVerdictQuery: () => ({}),
	useGetGridQuery: () => ({}),
	useGetHealthQuery: () => ({}),
	useStartExperimentMutation: () => [() => undefined, {}],
	useRegradeMutation: () => [() => undefined, {}],
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useCancelExperimentMutation: () => [() => undefined, {}],
	useResumeExperimentMutation: () => [() => undefined, {}],
}));
vi.mock("@/lib/store/apis/dataOpsApi", () => ({ useDeleteExperimentMutation: () => [() => undefined, {}] }));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({ useGetExperimentAdaptiveQuery: () => ({}) }));
vi.mock("@/lib/store/apis/supportApi", () => ({ useGetExperimentSupportQuery: () => ({}) }));
vi.mock("@/lib/store/apis/researchApi", () => ({ useGetExperimentResearchQuery: () => ({}) }));
vi.mock("@/lib/store/apis/routingApi", () => ({ useGetExperimentRoutingQuery: () => ({}) }));
vi.mock("./liveProgress", () => ({ LiveProgress: () => null, default: () => null }));
vi.mock("@/components/shell/routeTabs", () => ({ RouteTabs: () => null }));

const created = (id: string) => ({ exp_id: id, name: id, state: "created", slots: { total: 2, done: 0, running: 0, failed: 0 } });

describe("ExperimentView identity (QC2-28-04)", () => {
	it("shows the loading state, not the previous experiment's Start, while the new id loads", () => {
		api.status = okQuery(created("e_previous"));
		const html = render(<ExperimentView expId="e_next" />);
		expect(html).toContain('data-testid="experiment-loading"');
		expect(html).not.toContain("experiment-start");
	});
	it("renders the header and Start once the response matches the route", () => {
		api.status = okQuery(created("e_next"));
		const html = render(<ExperimentView expId="e_next" />);
		expect(html).toContain('data-testid="experiment-start"');
	});
});

describe("ExperimentView delete (X16)", () => {
	it("offers Delete to an admin, disabled with the reason while the comparison runs", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { tagOf } = await import("@/lib/testing/render");
		api.status = okQuery(created("e_done"));
		const idle = render(<ExperimentView expId="e_done" />, hostedSession("admin"));
		expect(tagOf(idle, "experiment-delete")).not.toContain('disabled=""');
		api.status = okQuery({ ...created("e_run"), state: "running" });
		const running = render(<ExperimentView expId="e_run" />, hostedSession("admin"));
		expect(tagOf(running, "experiment-delete")).toContain('disabled=""');
		expect(running).toContain("Stop the comparison or wait for it to finish before deleting it.");
	});
	it("hides Delete below admin", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		api.status = okQuery(created("e_done"));
		const html = render(<ExperimentView expId="e_done" />, hostedSession("member"));
		expect(html).not.toContain('data-testid="experiment-delete"');
		expect(html).toContain("experiment-delete-admin-only");
	});
});
