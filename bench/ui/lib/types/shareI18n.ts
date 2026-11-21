// Types the "share" namespace (lane X14: share links, exports, the shared page) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		share: typeof import("@/locales/en/share.json");
	}
}

export {};