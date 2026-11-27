import type ufc3En from "@/locales/en/ufc3.json";

// Types the "ufc3" namespace (locales/en/ufc3.json): phase C texts of the comparison pages.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc3: typeof ufc3En;
	}
}

export {};
