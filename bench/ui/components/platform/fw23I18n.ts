import type fw23En from "@/locales/en/fw23.json";

// Types the "fw23" namespace (locales/en/fw23.json): the organization menu, the help menu, page
// titles and the form checks of the app shell.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw23: typeof fw23En;
	}
}

export {};