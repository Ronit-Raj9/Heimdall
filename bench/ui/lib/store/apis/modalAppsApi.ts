import { benchApi } from "./benchApi";

/** One serving app on the org's Modal account that no running deployment owns (openmodel.AppInfo). */
export interface ModalApp {
	name: string;
	app_id: string;
	state: string;
	tasks: string;
}

/** GET /open-models/modal-apps (handlers/openmodels.go OpenModelApps): org admins only. */
export const modalAppsRequest = { url: "/open-models/modal-apps" } as const;

/** Round 5 lane L8: leftover Modal apps on Settings, Operations. */
export const modalAppsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getModalApps: builder.query<{ apps: ModalApp[] }, void>({ query: () => modalAppsRequest }),
	}),
});

export const { useLazyGetModalAppsQuery } = modalAppsApi;
