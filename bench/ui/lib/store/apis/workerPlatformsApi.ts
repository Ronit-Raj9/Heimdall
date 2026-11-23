// Bring your own worker (handlers/workerplatforms.go): the org's own cloud sandbox platforms.
// Injected into benchApi so it shares the base query (CSRF, 401 handling). Tokens go in, never out.
import { benchApi } from "./benchApi";

export type PlatformStatus = "untested" | "ok" | "invalid" | "error";

export interface WorkerPlatform {
	name: string;
	title: string;
	token_hint: string;
	endpoint_label?: string;
	endpoint_required?: boolean;
	docs_url?: string;
	connected: boolean;
	default: boolean;
	endpoint?: string;
	token_last4?: string;
	status?: PlatformStatus | string;
	last_tested_at?: string;
	connected_at?: string;
	connected_by?: string;
	/** The image registry the platform pulls Heimdall's images from (fix wave 3, N216); its token is never returned. */
	registry?: PlatformRegistry;
}

export interface PlatformRegistry {
	registry: string;
	username: string;
	token_last4?: string;
	tested_at?: string;
}

export interface SavePlatformRegistryRequest {
	platform: string;
	registry: string;
	username: string;
	token: string;
}

export interface WorkerPlatformsList {
	platforms: WorkerPlatform[];
	/** The org's default worker: a platform name, or "" for the shared worker. */
	default: string;
}

export interface ConnectPlatformRequest {
	platform: string;
	token: string;
	endpoint?: string;
}

const id = (value: string) => encodeURIComponent(value);

export const workerPlatformsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listWorkerPlatforms: builder.query<WorkerPlatformsList, void>({
			query: () => "/worker-platforms",
			providesTags: ["Runners"],
		}),
		connectWorkerPlatform: builder.mutation<WorkerPlatform, ConnectPlatformRequest>({
			query: ({ platform, token, endpoint }) => ({
				url: `/worker-platforms/${id(platform)}/connect`,
				method: "POST",
				body: endpoint ? { token, endpoint } : { token },
			}),
			invalidatesTags: ["Runners", "WorkerTarget", "AuditLog"],
		}),
		testWorkerPlatform: builder.mutation<WorkerPlatform, string>({
			query: (platform) => ({
				url: `/worker-platforms/${id(platform)}/test`,
				method: "POST",
			}),
			invalidatesTags: ["Runners"],
		}),
		setWorkerPlatformDefault: builder.mutation<WorkerPlatformsList, string>({
			query: (platform) => ({
				url: "/worker-platforms/default",
				method: "POST",
				body: { platform },
			}),
			invalidatesTags: ["Runners", "WorkerConnections", "WorkerTarget", "AuditLog"],
		}),
		disconnectWorkerPlatform: builder.mutation<WorkerPlatformsList, string>({
			query: (platform) => ({
				url: `/worker-platforms/${id(platform)}/disconnect`,
				method: "POST",
			}),
			invalidatesTags: ["Runners", "WorkerTarget", "AuditLog"],
		}),
		savePlatformRegistry: builder.mutation<PlatformRegistry, SavePlatformRegistryRequest>({
			query: ({ platform, registry, username, token }) => ({
				url: `/worker-platforms/${id(platform)}/registry`,
				method: "POST",
				body: { registry, username, token },
			}),
			invalidatesTags: ["Runners", "WorkerTarget", "AuditLog"],
		}),
		removePlatformRegistry: builder.mutation<void, string>({
			query: (platform) => ({
				url: `/worker-platforms/${id(platform)}/registry/remove`,
				method: "POST",
			}),
			invalidatesTags: ["Runners", "WorkerTarget", "AuditLog"],
		}),
	}),
});

export const {
	useListWorkerPlatformsQuery,
	useConnectWorkerPlatformMutation,
	useTestWorkerPlatformMutation,
	useSetWorkerPlatformDefaultMutation,
	useDisconnectWorkerPlatformMutation,
	useSavePlatformRegistryMutation,
	useRemovePlatformRegistryMutation,
} = workerPlatformsApi;

/** The test answer of a connect or test call: the server's reason when the platform refused. */
export function platformStatusTone(status: string | undefined): "success" | "warning" | "outline" {
	if (status === "ok") return "success";
	if (status === "invalid" || status === "error") return "warning";
	return "outline";
}