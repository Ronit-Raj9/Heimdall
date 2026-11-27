import type ufc6En from "@/locales/en/ufc6.json";

// Types the "ufc6" namespace (locales/en/ufc6.json): phase C texts of the failures and checks pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc6: typeof ufc6En;
	}
}

export {};
