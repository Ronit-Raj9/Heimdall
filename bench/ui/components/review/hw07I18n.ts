import type hw07En from "@/locales/en/hw07.json";

// Types the "hw07" namespace (locales/en/hw07.json): the review reason for a run a safety check threw out.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw07: typeof hw07En;
	}
}

export {};