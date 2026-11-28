import { benchApi } from "./benchApi";
import type { Incident, IncidentImpact, IncidentStatus } from "./statusApi";

/** POST /status/incidents (handlers/statusincidents.go OpenIncidentBody). Server admins only. */
export interface OpenIncidentBody {
	title: string;
	message: string;
	impact?: IncidentImpact;
}

/** POST /status/incidents/{id} (UpdateIncidentBody). */
export interface UpdateIncidentBody {
	message: string;
	status?: IncidentStatus;
}

export const statusIncidentsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		openIncident: builder.mutation<Incident, OpenIncidentBody>({
			query: (body) => ({ url: "/status/incidents", method: "POST", body }),
		}),
		updateIncident: builder.mutation<Incident, { id: string; body: UpdateIncidentBody }>({
			query: ({ id, body }) => ({ url: `/status/incidents/${encodeURIComponent(id)}`, method: "POST", body }),
		}),
	}),
});

export const { useOpenIncidentMutation, useUpdateIncidentMutation } = statusIncidentsApi;
