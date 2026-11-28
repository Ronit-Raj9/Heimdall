import type ufh2En from "@/locales/en/ufh2.json";

// Types the "ufh2" namespace (locales/en/ufh2.json): lane H2, what a feature test needs and the image wait.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufh2: typeof ufh2En;
	}
}

export {};
