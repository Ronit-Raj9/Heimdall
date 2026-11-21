// Where runs go (handlers/workertarget.go): one per-org setting over the two ways to bring your own
// compute, a fresh cloud sandbox per run (Modal, Daytona, E2B) or one of the org's own workers
// (Runloop, Fly Machines, Kubernetes, a VM). Hosted only; local servers answer 503.
import { benchApi } from "./benchApi";

export type WorkerTargetKind = "shared" | "platform" | "worker";

export interface WorkerTargetChoice {
	/** "shared", "platform:<name>" or "worker:<pool>". */
	target: string;
	kind: WorkerTargetKind;
	name: string;
	title: string;
	status?: string;
	current: boolean;
	/** False when one evaluation cannot pick it on its own; note says why. */
	per_evaluation: boolean;
	note?: string;
}

export interface WorkerTargetView {
	target: string;
	choices: WorkerTargetChoice[];
}

export const workerTargetApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getWorkerTarget: builder.query<WorkerTargetView, void>({
			query: () => "/workers/target",
			providesTags: ["WorkerTarget"],
		}),
		setWorkerTarget: builder.mutation<WorkerTargetView, string>({
			query: (target) => ({ url: "/workers/target", method: "PUT", body: { target } }),
			invalidatesTags: ["WorkerTarget", "WorkerConnections", "Runners", "AuditLog"],
		}),
	}),
});

export const { useGetWorkerTargetQuery, useSetWorkerTargetMutation } = workerTargetApi;