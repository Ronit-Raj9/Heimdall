import {
	Activity,
	Award,
	Bot,
	Boxes,
	Bug,
	CalendarClock,
	CirclePlay,
	FlaskConical,
	House,
	ListOrdered,
	PackageCheck,
	Plug,
	Radio,
	SearchX,
	Settings,
	Tags,
	Zap,
	TrendingUp,
	type LucideIcon,
} from "lucide-react";
import i18n from "@/lib/i18n";
import { pageTitleOverride } from "@/lib/routing/pageTitle";

export type BenchNavGroupId = "home" | "agents" | "test" | "tasks" | "watch" | "release" | "settings";

export interface BenchNavItem {
	/** The entry's route. Object routes below it (an experiment, a run, a suite) keep it highlighted. */
	to:
		| "/"
		| "/agents"
		| "/leaderboard"
		| "/improvements"
		| "/evaluations"
		| "/experiments"
		| "/failure-tests"
		| "/mcp-test"
		| "/runs"
		| "/suites"
		| "/annotation"
		| "/why-agents-fail"
		| "/scheduled-checks"
		| "/live"
		| "/releases"
		| "/settings";
	labelKey:
		| "shell.nav.home"
		| "shell.nav.agents"
		| "shell.nav.leaderboard"
		| "shell.nav.improvements"
		| "shell.nav.evaluations"
		| "shell.nav.experiments"
		| "shell.nav.failureTests"
		| "shell.nav.mcpTest"
		| "shell.nav.runs"
		| "shell.nav.suites"
		| "shell.nav.labelling"
		| "shell.nav.whyAgentsFail"
		| "shell.nav.scheduledChecks"
		| "shell.nav.live"
		| "shell.nav.releases"
		| "shell.nav.settings";
	icon: LucideIcon;
	testId: string;
	/**
	 * Other paths that belong to this entry: old pages that are now one of its tabs (they redirect
	 * here) and pages reached from it (a job's page under Settings, the quickstart from Home).
	 */
	also?: readonly string[];
}

export interface BenchNavGroup {
	id: BenchNavGroupId;
	/** The group heading; none for the top group (Home), which sits above the headed groups. */
	labelKey?:
		| "shell.groups.agents"
		| "shell.groups.test"
		| "shell.groups.tasks"
		| "shell.groups.watch"
		| "shell.groups.release"
		| "shell.groups.settings";
	items: readonly BenchNavItem[];
	/** Closed until the user opens it (or visits one of its pages). */
	defaultClosed?: boolean;
}

/**
 * Left navigation, agent first: the agent is the first noun, each test a user runs on it has a
 * visible entry, and models, keys and machines are inputs that live in Settings. One entry per
 * object; features are tabs of their page. "Evaluate an agent" is the header's primary action,
 * not an entry.
 */
export const BENCH_NAV_GROUPS: readonly BenchNavGroup[] = [
	{
		id: "home",
		items: [{ to: "/", labelKey: "shell.nav.home", icon: House, testId: "nav-link-home", also: ["/quickstart", "/welcome"] }],
	},
	{
		id: "agents",
		labelKey: "shell.groups.agents",
		items: [
			{ to: "/agents", labelKey: "shell.nav.agents", icon: Bot, testId: "nav-link-agents", also: ["/agents_", "/connect"] },
			{ to: "/leaderboard", labelKey: "shell.nav.leaderboard", icon: ListOrdered, testId: "nav-link-leaderboard", also: ["/trends"] },
			{ to: "/improvements", labelKey: "shell.nav.improvements", icon: TrendingUp, testId: "nav-link-improvements" },
		],
	},
	{
		id: "test",
		labelKey: "shell.groups.test",
		items: [
			{ to: "/evaluations", labelKey: "shell.nav.evaluations", icon: Award, testId: "nav-link-evaluations", also: ["/evaluate"] },
			{
				to: "/experiments",
				labelKey: "shell.nav.experiments",
				icon: FlaskConical,
				testId: "nav-link-experiments",
				also: ["/verdict", "/matrix", "/migration", "/receipts"],
			},
			{ to: "/failure-tests", labelKey: "shell.nav.failureTests", icon: Zap, testId: "nav-link-failure-tests" },
			{ to: "/mcp-test", labelKey: "shell.nav.mcpTest", icon: Plug, testId: "nav-link-mcp-test" },
			{ to: "/runs", labelKey: "shell.nav.runs", icon: CirclePlay, testId: "nav-link-runs", also: ["/compare"] },
		],
	},
	{
		id: "tasks",
		labelKey: "shell.groups.tasks",
		items: [
			{
				to: "/suites",
				labelKey: "shell.nav.suites",
				icon: Boxes,
				testId: "nav-link-suites",
				also: ["/catalog", "/imports", "/calibration"],
			},
			{ to: "/annotation", labelKey: "shell.nav.labelling", icon: Tags, testId: "nav-link-labelling" },
		],
	},
	{
		id: "watch",
		labelKey: "shell.groups.watch",
		items: [
			{
				to: "/why-agents-fail",
				labelKey: "shell.nav.whyAgentsFail",
				icon: SearchX,
				testId: "nav-link-why-agents-fail",
				also: ["/inbox", "/clusters", "/triage", "/maxim"],
			},
			{
				to: "/scheduled-checks",
				labelKey: "shell.nav.scheduledChecks",
				icon: CalendarClock,
				testId: "nav-link-scheduled-checks",
				also: ["/canaries", "/schedules"],
			},
			{ to: "/live", labelKey: "shell.nav.live", icon: Radio, testId: "nav-link-live" },
		],
	},
	{
		id: "release",
		labelKey: "shell.groups.release",
		items: [
			{ to: "/releases", labelKey: "shell.nav.releases", icon: PackageCheck, testId: "nav-link-releases", also: ["/review", "/policies"] },
		],
	},
	{
		id: "settings",
		labelKey: "shell.groups.settings",
		items: [
			{
				to: "/settings",
				labelKey: "shell.nav.settings",
				icon: Settings,
				testId: "nav-link-settings",
				also: ["/models", "/workers", "/usage", "/files", "/jobs", "/integrations", "/operate", "/admin", "/upload", "/monitors"],
			},
		],
	},
];

/** Groups that start closed for a visitor who has not toggled any group yet. */
export const DEFAULT_CLOSED_GROUPS: readonly BenchNavGroupId[] = BENCH_NAV_GROUPS.filter((group) => group.defaultClosed).map(
	(group) => group.id,
);

/** The i18n key of an entry's one-line plain description (bench shell.navHint.*), shown on hover. */
export function navHintKey(item: BenchNavItem): `shell.navHint.${string}` {
	return `shell.navHint.${item.labelKey.slice("shell.nav.".length)}`;
}

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

/** The entry to highlight for a path, or null when none owns it (an auth page, the docs). */
export function activeNavItem(pathname: string): BenchNavItem | null {
	return BENCH_NAV.find((item) => isNavActive(item, pathname)) ?? null;
}

/** The group that holds the entry for a path, or null. */
export function activeNavGroup(pathname: string): BenchNavGroupId | null {
	const item = activeNavItem(pathname);
	if (!item) return null;
	return BENCH_NAV_GROUPS.find((group) => group.items.includes(item))?.id ?? null;
}

/**
 * Pages that have their own browser tab title although the sidebar highlights another entry for
 * them (through `also`) or none: path to its fw23 locale key.
 */
const PAGE_TITLES: Readonly<Record<string, "titles.maxim" | "titles.docs" | "titles.jobs" | "titles.monitors">> = {
	"/maxim": "titles.maxim",
	"/docs": "titles.docs",
	"/jobs": "titles.jobs",
	"/monitors": "titles.monitors",
};

/** Path prefixes with their own title, checked in order after the exact paths: an object page below a list. */
const PAGE_TITLE_PREFIXES: readonly (readonly [string, "titles.job"])[] = [["/jobs/", "titles.job"]];

/** Pages of wave 4 with their own title under another entry: path to its w4m0 locale key. */
const W4_PAGE_TITLES: Readonly<Record<string, "titles.newAgent" | "titles.fromTraces" | "titles.connect">> = {
	"/agents/new": "titles.newAgent",
	"/agents/from-traces": "titles.fromTraces",
	"/connect": "titles.connect",
};

/** The page's own title when the sidebar entry it sits under is another page (or none): "Jobs", "Job", "New agent", "Connect an agent". Null for a page that is its nav entry. */
export function ownPageTitle(pathname: string): string | null {
	const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
	const fw23 = PAGE_TITLES[path] ?? PAGE_TITLE_PREFIXES.find(([prefix]) => path.startsWith(prefix))?.[1];
	if (fw23) return i18n.t(fw23, { ns: "fw23" });
	const w4 = W4_PAGE_TITLES[path];
	if (w4) return i18n.t(w4, { ns: "w4m0" });
	return null;
}

/**
 * The browser tab title for a path: the title the page set for itself (usePageTitle), then the
 * page's own title (exact path, then prefix), else its nav entry's label, then the app name.
 */
export function pageTitle(pathname: string): string {
	const override = pageTitleOverride(pathname);
	if (override) return override;
	const app = i18n.t("app.name", { ns: "bench" });
	const own = ownPageTitle(pathname);
	if (own) return `${own} · ${app}`;
	const item = activeNavItem(pathname);
	return item ? `${i18n.t(item.labelKey, { ns: "bench" })} · ${app}` : app;
}