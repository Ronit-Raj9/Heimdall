import {
	Activity,
	Bot,
	Boxes,
	CirclePlay,
	ClipboardCheck,
	FlaskConical,
	House,
	Inbox,
	ListOrdered,
	PackageCheck,
	Radio,
	Server,
	Settings,
	ShieldCheck,
	type LucideIcon,
} from "lucide-react";

export type BenchNavGroupId = "benchmark" | "build" | "production" | "govern" | "operate";

export interface BenchNavItem {
	/** The entry's route. Object routes below it (an experiment, a run, a suite) keep it highlighted. */
	to:
		| "/"
		| "/experiments"
		| "/runs"
		| "/leaderboard"
		| "/agents"
		| "/suites"
		| "/canaries"
		| "/live"
		| "/inbox"
		| "/review"
		| "/releases"
		| "/monitors"
		| "/workers"
		| "/settings";
	labelKey:
		| "shell.nav.home"
		| "shell.nav.experiments"
		| "shell.nav.runs"
		| "shell.nav.leaderboard"
		| "shell.nav.agents"
		| "shell.nav.suites"
		| "shell.nav.canaries"
		| "shell.nav.live"
		| "shell.nav.inbox"
		| "shell.nav.review"
		| "shell.nav.releases"
		| "shell.nav.monitors"
		| "shell.nav.workers"
		| "shell.nav.settings";
	icon: LucideIcon;
	testId: string;
	/**
	 * Other paths that belong to this entry: pages that became one of its tabs and still answer
	 * until their redirect lands, or pages reached from it (the quickstart from Home).
	 */
	also?: readonly string[];
	/** Hosted mode with a signed-in user only (the organization's settings). */
	hostedOnly?: boolean;
}

export interface BenchNavGroup {
	id: BenchNavGroupId;
	labelKey: "shell.groups.benchmark" | "shell.groups.build" | "shell.groups.production" | "shell.groups.govern" | "shell.groups.operate";
	items: readonly BenchNavItem[];
}

/**
 * Left navigation, grouped around the objects a user thinks in (UI regroup contract). A feature
 * page is a tab of its object or an entry of its group, never a nav entry of its own. "New
 * experiment" is the header's primary action, not an entry.
 */
export const BENCH_NAV_GROUPS: readonly BenchNavGroup[] = [
	{
		id: "benchmark",
		labelKey: "shell.groups.benchmark",
		items: [
			{ to: "/", labelKey: "shell.nav.home", icon: House, testId: "nav-link-home", also: ["/quickstart"] },
			{
				to: "/experiments",
				labelKey: "shell.nav.experiments",
				icon: FlaskConical,
				testId: "nav-link-experiments",
				also: ["/verdict", "/matrix", "/migration", "/receipts"],
			},
			{ to: "/runs", labelKey: "shell.nav.runs", icon: CirclePlay, testId: "nav-link-runs", also: ["/compare"] },
			{ to: "/leaderboard", labelKey: "shell.nav.leaderboard", icon: ListOrdered, testId: "nav-link-leaderboard", also: ["/trends"] },
		],
	},
	{
		id: "build",
		labelKey: "shell.groups.build",
		items: [
			{ to: "/agents", labelKey: "shell.nav.agents", icon: Bot, testId: "nav-link-agents", also: ["/agents_"] },
			{ to: "/suites", labelKey: "shell.nav.suites", icon: Boxes, testId: "nav-link-suites", also: ["/imports", "/calibration"] },
		],
	},
	{
		id: "production",
		labelKey: "shell.groups.production",
		items: [
			{ to: "/canaries", labelKey: "shell.nav.canaries", icon: Activity, testId: "nav-link-canaries" },
			{ to: "/live", labelKey: "shell.nav.live", icon: Radio, testId: "nav-link-live" },
			{ to: "/inbox", labelKey: "shell.nav.inbox", icon: Inbox, testId: "nav-link-inbox", also: ["/clusters", "/triage"] },
		],
	},
	{
		id: "govern",
		labelKey: "shell.groups.govern",
		items: [
			{ to: "/review", labelKey: "shell.nav.review", icon: ClipboardCheck, testId: "nav-link-review" },
			{ to: "/releases", labelKey: "shell.nav.releases", icon: PackageCheck, testId: "nav-link-releases" },
			{ to: "/monitors", labelKey: "shell.nav.monitors", icon: ShieldCheck, testId: "nav-link-monitors" },
		],
	},
	{
		id: "operate",
		labelKey: "shell.groups.operate",
		items: [
			{ to: "/workers", labelKey: "shell.nav.workers", icon: Server, testId: "nav-link-workers" },
			{ to: "/settings", labelKey: "shell.nav.settings", icon: Settings, testId: "nav-link-settings", hostedOnly: true },
		],
	},
];

/** Every entry in order, for lookups. */
export const BENCH_NAV: readonly BenchNavItem[] = BENCH_NAV_GROUPS.flatMap((group) => group.items);

function underPath(pathname: string, base: string): boolean {
	return pathname === base || pathname.startsWith(`${base}/`);
}

/**
 * Whether an entry is the current one for a path: its route or any route below it (an object
 * page, a tab route), or one of its `also` paths. Home matches "/" and its `also` paths only.
 */
export function isNavActive(item: BenchNavItem, pathname: string): boolean {
	const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
	if (item.to === "/") return path === "/" || path === "" || (item.also ?? []).some((base) => underPath(path, base));
	return underPath(path, item.to) || (item.also ?? []).some((base) => underPath(path, base));
}

/** The entry to highlight for a path, or null when none owns it (the jobs page, an auth page). */
export function activeNavItem(pathname: string): BenchNavItem | null {
	return BENCH_NAV.find((item) => isNavActive(item, pathname)) ?? null;
}

/** The group that holds the entry for a path, or null. */
export function activeNavGroup(pathname: string): BenchNavGroupId | null {
	const item = activeNavItem(pathname);
	if (!item) return null;
	return BENCH_NAV_GROUPS.find((group) => group.items.includes(item))?.id ?? null;
}