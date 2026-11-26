import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { RunnersTab } from "./runnersTab";

const api = vi.hoisted(() => ({
	created: {
		token: "hrt_secret",
		info: { id: "rtk_1", name: "box", pool: "gpu", prefix: "hrt_", created_at: "2026-11-01T09:00:00Z" },
		command: "start line",
		install: "install line",
	} as unknown,
}));

// The one-time secret block shows only after a token is created; the token section's null state is
// replaced with a created token so the static render shows it.
vi.mock("react", async (importOriginal) => {
	const real = await importOriginal<typeof import("react")>();
	const useState = ((init: unknown) =>
		init === null && /RunnerTokensSection/.test(new Error().stack ?? "")
			? [api.created, () => undefined]
			: real.useState(init)) as typeof real.useState;
	return { ...real, default: { ...real, useState }, useState };
});
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
	const { RUNNER_TOKENS: tokens, RUNNERS: runners } = await import("@/lib/testing/platformFixtures");
	return {
		...real,
		useListRunnersQuery: () => ok(runners),
		useListRunnerTokensQuery: () => ok(tokens),
		useRemoveRunnerMutation: () => m({}),
		useCreateRunnerTokenMutation: () => m({}),
		useRevokeRunnerTokenMutation: () => m({}),
	};
});

describe("runner install guide (N137)", () => {
	it("install guide opens the in-app guide", () => {
		const html = render(<RunnersTab org={hostedSession().org!} />, hostedSession("admin"));
		expect(html).toContain('data-testid="runner-install-doc"');
		expect(html).toMatch(
			/href="\/docs\?doc=runners"[^>]*data-testid="runner-install-doc"|data-testid="runner-install-doc"[^>]*href="\/docs\?doc=runners"/,
		);
		expect(html).toContain('hash="installing-a-runner"');
		expect(html).not.toContain("github.com/maximhq");
	});
});