// Types the shared namespaces of lane S2: paid actions, toasts, errors and the S2 extras.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		paid: typeof import("@/locales/en/paid.json");
		toast: typeof import("@/locales/en/toast.json");
	}
}
export {};
