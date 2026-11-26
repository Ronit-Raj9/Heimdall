// Bring your own worker (handlers/workerconns.go, package byoworker): the workers an organization
// connects on Runloop, Fly Machines, its own Kubernetes cluster or its own VM, and the worker its
// evaluations run on by default. Hosted only; local servers answer 503.
import { withOrgParam } from "./orgParam";
import type { Runner } from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

export type WorkerPlatformId = "runloop" | "fly" | "kubernetes" | "vm" | "modal" | "daytona" | "e2b" | string;

export type WorkerConnectionStatus = "ready" | "starting" | "waiting" | "offline" | "stopped" | "error";

export interface WorkerHealth {
	ok: boolean;
	detail: string;
	capacity?: string;
	checked_at: string;
}

export interface WorkerInstance {
	id: string;
	state: "starting" | "running" | "stopped" | "failed" | "unknown" | string;
	detail?: string;
}

export interface WorkerConnection {
	id: string;
	name: string;
	pool: string;
	platform: WorkerPlatformId;
	platform_title: string;
	settings: Record<string, string>;
	status: WorkerConnectionStatus;
	status_detail: string;
	health?: WorkerHealth;
	instance?: WorkerInstance;
	runner?: Runner;
	default: boolean;
	created_by: string;
	created_at: string;
	updated_at: string;
}

export interface WorkerPlatform {
	id: WorkerPlatformId;
	title: string;
	/** True when Heimdall starts the worker itself; false for a VM the org installs. */
	starts: boolean;
}

export interface WorkerConnectionsList {
	connections: WorkerConnection[];
	/** The pool new evaluations run on; "" is the shared worker. */
	default_worker: string;
	platforms: WorkerPlatform[];
}

export interface WorkerConnectionCreate {
	name: string;
	platform: WorkerPlatformId;
	settings?: Record<string, string>;
	secret?: string;
}

export interface WorkerConnectionCreated {
	connection: WorkerConnection;
	/** A VM's one-line install command; it holds the worker's token and is shown once. */
	install?: string;
}

const id = (value: string) => encodeURIComponent(value);

export type WorkerAction = "check" | "start" | "stop" | "remove";

export const workerConnectionsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listWorkerConnections: builder.query<WorkerConnectionsList, void>({
			query: () => "/workers/connections",
			providesTags: ["WorkerConnections"],
		}),
		createWorkerConnection: builder.mutation<WorkerConnectionCreated, WorkerConnectionCreate>({
			query: (body) => ({ url: "/workers/connections", method: "POST", body }),
			invalidatesTags: ["WorkerConnections", "WorkerTarget", "Runners", "RunnerTokens", "AuditLog"],
		}),
		workerConnectionAction: builder.mutation<WorkerConnection, { id: string; action: WorkerAction }>({
			query: ({ id: connId, action }) => ({ url: `/workers/connections/${id(connId)}/${action}`, method: "POST" }),
			invalidatesTags: ["WorkerConnections", "WorkerTarget", "Runners", "AuditLog"],
		}),
		workerInstallCommand: builder.mutation<{ install: string }, string>({
			query: (connId) => ({ url: `/workers/connections/${id(connId)}/install-command`, method: "POST" }),
			invalidatesTags: ["AuditLog"],
		}),
		setDefaultWorker: builder.mutation<{ worker: string }, string>({
			query: (worker) => ({ url: "/workers/default", method: "PUT", body: { worker } }),
			invalidatesTags: ["WorkerConnections", "WorkerTarget", "Runners", "AuditLog"],
		}),
	}),
});

export const {
	useListWorkerConnectionsQuery,
	useCreateWorkerConnectionMutation,
	useWorkerConnectionActionMutation,
	useWorkerInstallCommandMutation,
	useSetDefaultWorkerMutation,
} = workerConnectionsApi;

/** The URL of the RBAC manifest for a namespace (GET /workers/kubernetes-manifest). */
export function kubernetesManifestUrl(namespace: string): string {
	return withOrgParam(`${BENCH_API_BASE}/workers/kubernetes-manifest?namespace=${encodeURIComponent(namespace)}`);
}