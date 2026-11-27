import type ufw3En from "@/locales/en/ufw3.json";

// Types the "ufw3" namespace (locales/en/ufw3.json): round 2 pass W, lane W3 (benchmarks, comparisons, checks, releases).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufw3: typeof ufw3En;
	}
}

export {};
