import type { DoctorResponse, HealthResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** GET /doctor: the preflight checks of a self-hosted server (handlers/health.go). The hosted server refuses it. */
export const doctorRequest = { url: "/doctor" } as const;

/** Round 5 lane L8: the preflight panel on Settings, Operations. */
export const doctorApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getDoctor: builder.query<DoctorResponse, void>({ query: () => doctorRequest }),
		getServerHealth: builder.query<HealthResponse, void>({ query: () => ({ url: "/health" }) }),
	}),
});

export const { useLazyGetDoctorQuery, useGetServerHealthQuery } = doctorApi;