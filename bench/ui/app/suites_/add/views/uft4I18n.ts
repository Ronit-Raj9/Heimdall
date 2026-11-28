import type uft4En from "@/locales/en/uft4.json";

// Types the "uft4" namespace (locales/en/uft4.json): round 7 lane T4, a task from a repository commit.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uft4: typeof uft4En;
	}
}

export {};
