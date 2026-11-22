import type fw26En from "@/locales/en/fw26.json";

// Types the "fw26" namespace (locales/en/fw26.json): wizard step 4 notes and the step 4 Next hint.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw26: typeof fw26En;
	}
}

export {};