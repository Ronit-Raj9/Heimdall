import type ufd3En from "@/locales/en/ufd3.json";

// Types the "ufd3" namespace (locales/en/ufd3.json): the wizard's free check and the catalog's command line text.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufd3: typeof ufd3En;
	}
}

export {};
