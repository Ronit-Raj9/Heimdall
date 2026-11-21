import "@/lib/i18n";

import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { WorkerConnection, WorkerConnectionsList } from "@/lib/store/apis/workerConnectionsApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConnectedWorkers, ConnectWorkerForm, draftToCreate, EvaluationWorkerPicker } from "./connectedWorkers";

const q = vi.hoisted(() => ({ list: {} as Record<string, unknown> }));

vi.mock("@/lib/store/apis/workerConnectionsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerConnectionsApi")>()),
	useListWorkerConnectionsQuery: () => q.list,
	useCreateWorkerConnectionMutation: () => mutation(),
	useWorkerConnectionActionMutation: () => mutation(),
	useWorkerInstallCommandMutation: () => mutation(),
	useSetDefaultWorkerMutation: () => mutation(),
}));

const now = new Date().toISOString();

function conn(over: Partial<WorkerConnection>): WorkerConnection {
	return {
		id: "wkc_1",
		name: "fly-1",
		pool: "fly-1",
		platform: "fly",
		platform_title: "Fly Machines",
		settings: {},
		status: "ready",
		status_detail: "Connected, 1 of 2 slots in use.",
		default: false,
		created_by: "usr_1",
		created_at: now,
		updated_at: now,
		...over,
	};
}

const platforms = [
	{ id: "fly", title: "Fly Machines", starts: true },
	{ id: "kubernetes", title: "your Kubernetes cluster", starts: true },
	{ id: "runloop", title: "Runloop", starts: true },
	{ id: "vm", title: "your own VM", starts: false },
];

const list = (connections: WorkerConnection[], def = ""): WorkerConnectionsList => ({ connections, default_worker: def, platforms });

describe("ConnectedWorkers", () => {
	beforeEach(() => {
		q.list = okQuery(list([]));
	});

	it("renders nothing on a server without bring your own worker", () => {
		q.list = errorQuery(503, "hosted only");
		expect(render(<ConnectedWorkers />)).toBe("");
	});

	it("says evaluations use the shared worker when none is connected", () => {
		const html = render(<ConnectedWorkers />);
		expect(html).toContain('data-testid="byo-empty"');
		expect(html).toContain("Shared worker");
		expect(html).toContain('data-testid="byo-connect"');
	});

	it("shows each worker with health, capacity, platform and last seen", () => {
		q.list = okQuery(
			list(
				[
					conn({
						runner: { id: "rnr_1", org_id: "o", name: "fly-1", pool: "fly-1", labels: ["platform=fly"], version: "abc123", host: "m1", state: "running", slots: 2, running: ["s1"], running_slots: 1, completed: 4, token_id: "rtk", last_seen: now, registered_at: now, live: true },
						health: { ok: true, detail: "Fly accepted the token for app w.", capacity: "Fly Machine: 4 CPU (performance), 8 GB memory", checked_at: now },
						default: true,
					}),
					conn({ id: "wkc_2", name: "k8s-prod", pool: "k8s-prod", platform: "kubernetes", platform_title: "your Kubernetes cluster", status: "error", status_detail: "The credential may not create deployments.", health: { ok: false, detail: "denied", checked_at: now } }),
					conn({ id: "wkc_3", name: "lab-box", pool: "lab-box", platform: "vm", platform_title: "your own VM", status: "waiting", status_detail: "Waiting for the install command to run on your VM." }),
				],
				"fly-1",
			),
		);
		const html = render(<ConnectedWorkers />);
		expect(html).toContain('data-testid="byo-card-fly-1"');
		expect(html).toContain("Ready");
		expect(html).toContain("1 of 2 runs at once");
		expect(html).toContain("Version abc123");
		expect(html).toContain("Default");
		expect(html).toContain("Needs attention");
		expect(html).toContain("The credential may not create deployments.");
		expect(html).toContain('data-testid="byo-card-lab-box-install-show"');
		expect(html).not.toContain('data-testid="byo-card-lab-box-start"');
		expect(html).toContain('data-testid="byo-card-k8s-prod-start"');
		expect(html).toContain('<option value="fly-1" selected="">fly-1 (Fly Machines)</option>');
		for (const word of ["runner", "pool", "lease"]) expect(html.toLowerCase()).not.toContain(` ${word} `);
	});
});

describe("ConnectWorkerForm", () => {
	it("shows the Kubernetes manifest link and credential choice", () => {
		const html = render(<ConnectWorkerForm platforms={[platforms[1]]} onClose={() => undefined} />);
		expect(html).toContain("/api/bench/workers/kubernetes-manifest?namespace=heimdall");
		expect(html).toContain('data-testid="byo-form-kubeconfig"');
		expect(html).toContain('data-testid="byo-form-kube-token"');
	});
});

describe("draftToCreate", () => {
	it("builds each platform's request", () => {
		expect(draftToCreate({ platform: "fly", name: " fly-1 ", fields: { app: "w", secret: " tok ", region: "", cpus: "4" }, kubeMode: "kubeconfig" })).toEqual({
			name: "fly-1",
			platform: "fly",
			settings: { app: "w", cpus: "4" },
			secret: "tok",
		});
		expect(draftToCreate({ platform: "vm", name: "lab", fields: { secret: "x" }, kubeMode: "kubeconfig" })).toEqual({ name: "lab", platform: "vm" });
		const k = draftToCreate({ platform: "kubernetes", name: "k", fields: { namespace: "heimdall", server: "https://k", token: "t", ca: "" }, kubeMode: "token" });
		expect(k.settings).toEqual({ namespace: "heimdall" });
		expect(JSON.parse(k.secret as string)).toEqual({ server: "https://k", certificate_authority: "", token: "t" });
	});
});

describe("EvaluationWorkerPicker", () => {
	it("hides without connected workers and offers each one otherwise", () => {
		q.list = okQuery(list([]));
		expect(render(<EvaluationWorkerPicker value="" onChange={() => undefined} />)).toBe("");
		q.list = okQuery(list([conn({ default: true }), conn({ id: "wkc_2", name: "rl-1", pool: "rl-1", platform_title: "Runloop", status: "stopped" })], "fly-1"));
		const html = render(<EvaluationWorkerPicker value="" onChange={() => undefined} />);
		expect(html).toContain("fly-1 (default)");
		expect(html).toContain('<option value="shared">Shared worker</option>');
		expect(html).toContain("rl-1 (Runloop, stopped)");
	});
});
