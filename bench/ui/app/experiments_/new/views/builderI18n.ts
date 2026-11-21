import type builderEn from "@/locales/en/builder.json";

// Types the "builder" namespace (locales/en/builder.json), owned by the experiment builder.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		builder: typeof builderEn;
	}
}

export {};
