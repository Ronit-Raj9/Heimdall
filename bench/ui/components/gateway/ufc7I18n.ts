import type ufc7En from "@/locales/en/ufc7.json";

// Types the "ufc7" namespace (locales/en/ufc7.json): gateway, Settings and model key texts (fix wave lane C7).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc7: typeof ufc7En;
	}
}

export {};
