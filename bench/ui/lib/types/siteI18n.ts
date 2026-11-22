// Types the "site" namespace (lane Y08: landing, public catalog, pricing, downloads, legal and
// security pages) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		site: typeof import("@/locales/en/site.json");
	}
}

export {};
