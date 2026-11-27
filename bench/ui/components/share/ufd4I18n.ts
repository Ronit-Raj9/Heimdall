import type ufd4En from "@/locales/en/ufd4.json";

// Types the "ufd4" namespace (locales/en/ufd4.json): share link rules and the shared comparison page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufd4: typeof ufd4En;
	}
}

export {};
