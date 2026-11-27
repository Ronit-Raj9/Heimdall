import "@/lib/i18n";

import { RUNNERS, hostedSession } from "@/lib/testing/platformFixtures";
import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { runnerStartCommand } from "@/components/platform/platform.utils";
import { RunnersTab, tokenFileOf } from "./runnersTab";

const api = vi.hoisted(() => ({ runners: {} as unknown }));

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/workerPlatformsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerPlatformsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useListWorkerPlatformsQuery: () => ({ data: { platforms: [], default: "" }, isLoading: false }),
		useConnectWorkerPlatformMutation: () => m({}),
		useTestWorkerPlatformMutation: () => m({}),
		useDisconnectWorkerPlatformMutation: () => m({}),
	};
});
vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useGetWorkerTargetQuery: () => ({
			data: {
				target: "shared",
				choices: [{ target: "shared", kind: "shared", name: "shared", title: "Shared worker", current: true, per_evaluation: true }],
			},
			isLoading: false,
		}),
		useSetWorkerTargetMutation: () => m({}),
	};
});
vi.mock("@/lib/store/apis/workerConnectionsApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/workerConnectionsApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useListWorkerConnectionsQuery: () => ({ data: { connections: [], default_worker: "", platforms: [] }, isLoading: false }),
		useCreateWorkerConnectionMutation: () => m({}),
		useWorkerConnectionActionMutation: () => m({}),
		useWorkerInstallCommandMutation: () => m({}),
	};
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
	const { mutation: m, okQuery: ok } = await import("@/lib/testing/render");
	const { RUNNER_TOKENS: tokens } = await import("@/lib/testing/platformFixtures");
	return {
		...real,
		useListRunnersQuery: () => api.runners,
		useListRunnerTokensQuery: () => ok(tokens),
		useRemoveRunnerMutation: () => m({}),
		useCreateRunnerTokenMutation: () => m({}),
		useRevokeRunnerTokenMutation: () => m({}),
	};
});

const org = hostedSession().org!;

describe("RunnersTab shared worker (N12, N13)", () => {
	it("offers Remove on an own runner but never on the shared worker", () => {
		const shared = {
			...RUNNERS.runners[0],
			id: "rnr_s",
			name: "shared-runner-org_abc",
			pool: "shared",
			slots: 2,
			labels: ["shared", "runtime=runsc"],
		};
		api.runners = okQuery({ mode: "live", runners: [RUNNERS.runners[1], shared] });
		const html = render(<RunnersTab org={org} />, hostedSession("admin"));
		expect(html).toContain(`data-testid="runner-remove-${RUNNERS.runners[1].id}"`);
		expect(html).toContain('data-testid="runner-row-rnr_s-shared-note"');
		expect(html).not.toContain('data-testid="runner-remove-rnr_s"');
	});

	it("says the host cap is shared by every organization when the shared worker has a host-slots label", () => {
		const shared = {
			...RUNNERS.runners[0],
			id: "rnr_s",
			name: "shared-runner-org_abc",
			pool: "shared",
			slots: 2,
			labels: ["shared", "runtime=runsc", "host-slots=2"],
		};
		api.runners = okQuery({ mode: "live", runners: [shared] });
		const html = render(<RunnersTab org={org} />, hostedSession("admin"));
		expect(html).toContain('data-testid="runner-row-rnr_s-shared-note-host-cap"');
		expect(html).toContain("Shared worker runs 2 at a time across all organizations.");
		api.runners = okQuery({ mode: "live", runners: [{ ...shared, labels: ["shared"] }] });
		expect(render(<RunnersTab org={org} />, hostedSession("admin"))).not.toContain("for all organizations together");
	});

	it("saves the token in the file the start and install lines read (N225)", () => {
		const start = "heimdall runner start -server 'https://h.dev' -token-file ~/.heimdall-runner-token -pool gpu";
		const install =
			"curl -fsSLo install.sh 'https://h.dev/api/bench/runners/install.sh' && sudo bash install.sh --server 'https://h.dev' --token-file ~/.heimdall-runner-token --pool gpu";
		expect(tokenFileOf(start)).toBe(tokenFileOf(install));
		expect(tokenFileOf("heimdall runner start")).toBe("~/.heimdall-runner-token");
		expect(tokenFileOf(runnerStartCommand("https://h.dev", "gpu"))).toBe("~/.heimdall-runner-token");
	});
});