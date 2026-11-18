import { HeimdallMark } from "@/components/heimdallMark";
import { BENCH_NAV, type BenchNavItem } from "@/lib/constants/navigation";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { GitCompare, PanelLeftClose, PanelLeftOpen, Settings, type LucideIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

type GroupId = "results" | "runs" | "setup" | "more";

// Which section each screen sits in. A screen added to BENCH_NAV without an entry here still
// shows, under "More", so a new route never disappears from the navigation.
const GROUP_OF: Record<string, GroupId> = {
	"/leaderboard": "results",
	"/verdict": "results",
	"/trends": "results",
	"/matrix": "results",
	"/agents/diff": "results",
	"/releases": "results",
	"/runs": "runs",
	"/receipts": "runs",
	"/triage": "runs",
	"/clusters": "runs",
	"/review": "runs",
	"/suites": "setup",
	"/agents": "setup",
	"/imports": "setup",
	"/workers": "setup",
	"/experiments/new": "setup",
	"/settings": "setup", // U1 platform: hosted settings
};

// Order inside each section; screens not listed keep their BENCH_NAV order after these.
const ORDER = [
	"/leaderboard",
	"/verdict",
	"/trends",
	"/matrix",
	"/agents/diff",
	"/releases",
	"/runs",
	"/receipts",
	"/triage",
	"/clusters",
	"/review",
	"/suites",
	"/agents",
	"/imports",
	"/workers",
	"/experiments/new",
	"/settings",
];

const GROUPS: readonly GroupId[] = ["results", "runs", "setup", "more"];

interface NavEntry {
	to: BenchNavItem["to"] | "/agents/diff" | "/settings";
	label: string;
	icon: LucideIcon;
	testId: string;
}

const COLLAPSE_KEY = "heimdall.nav-collapsed";

function readCollapsed(): boolean | null {
	try {
		const raw = window.localStorage.getItem(COLLAPSE_KEY);
		return raw === null ? null : raw === "1";
	} catch {
		return null;
	}
}

function writeCollapsed(value: boolean) {
	try {
		window.localStorage.setItem(COLLAPSE_KEY, value ? "1" : "0");
	} catch {
		// Storage blocked (private mode): the choice lasts for this page view only.
	}
}

/**
 * Left navigation in labelled sections. Below md it is a scrolling row above the content; from md
 * to xl it is an icon rail; from xl it shows labels. The toggle pins either form.
 */
export function BenchSidebar() {
	const { t } = useTranslation("bench");
	const { t: tp } = useTranslation("platform");
	const session = useSession();
	// null: follow the width (rail below xl). true or false: the user's choice.
	const [collapsed, setCollapsed] = useState<boolean | null>(null);
	useEffect(() => setCollapsed(readCollapsed()), []);
	// Whether the width alone would show labels (xl and up), for the toggle's label and icon.
	const [wide, setWide] = useState(true);
	useEffect(() => {
		if (typeof window === "undefined" || !window.matchMedia) return;
		const query = window.matchMedia("(min-width: 80rem)");
		const update = () => setWide(query.matches);
		update();
		query.addEventListener("change", update);
		return () => query.removeEventListener("change", update);
	}, []);

	// A demo visitor or a viewer cannot start experiments, so the form is not offered.
	const entries: NavEntry[] = BENCH_NAV.filter((item) => item.to !== "/experiments/new" || canUse(session, "member")).map(
		(item: BenchNavItem) => ({
			to: item.to,
			label: t(item.labelKey),
			icon: item.icon,
			testId: item.testId,
		}),
	);
	// Hosted mode: members, tokens, runners and the audit log of the organization.
	if (session.authMode === "hosted" && session.signedIn) {
		entries.push({ to: "/settings", label: tp("settings.title"), icon: Settings, testId: "nav-link-settings" });
	}
	// Agent diff has its own route but no BENCH_NAV entry; keep it one click away.
	if (!entries.some((entry) => entry.to === "/agents/diff")) {
		entries.push({ to: "/agents/diff", label: t("agentDiff.title"), icon: GitCompare, testId: "nav-link-agent-diff" });
	}
	const rank = (to: string) => {
		const index = ORDER.indexOf(to);
		return index === -1 ? ORDER.length : index;
	};
	const grouped = GROUPS.map((group) => ({
		group,
		items: entries.filter((entry) => (GROUP_OF[entry.to] ?? "more") === group).sort((a, b) => rank(a.to) - rank(b.to)),
	})).filter((section) => section.items.length > 0);

	// "auto" lets CSS pick the form by width; the user's toggle overrides it.
	const state = collapsed === null ? "auto" : collapsed ? "collapsed" : "expanded";
	const rail = state === "collapsed";
	const auto = state === "auto";
	// Label visibility: always in the mobile row; on md+ hidden in the rail.
	const labelClass = cn(rail && "md:sr-only", auto && "md:sr-only xl:not-sr-only");
	const sectionLabelClass = cn(rail && "md:hidden", auto && "md:hidden xl:block");

	// From "auto", the visible form decides what the toggle says and does.
	const showsRail = rail || (auto && !wide);
	const toggle = () => {
		const next = !showsRail;
		setCollapsed(next);
		writeCollapsed(next);
	};

	return (
		<aside
			className={cn(
				"bg-sidebar flex shrink-0 flex-col gap-3 border-b px-3 py-2.5 md:min-h-0 md:border-r md:border-b-0 md:px-2.5 md:py-3",
				"transition-[width] duration-200 ease-(--ease-out)",
				rail && "md:w-[3.75rem]",
				auto && "md:w-[3.75rem] xl:w-60",
				state === "expanded" && "md:w-60",
			)}
			data-testid="bench-sidebar"
			data-state={state}
		>
			<div
				className={cn(
					"flex items-center gap-2.5 px-1.5 md:pt-1 md:pb-2",
					rail && "md:justify-center md:px-0",
					auto && "md:justify-center md:px-0 xl:justify-start xl:px-1.5",
				)}
			>
				<span className="bg-foreground text-background flex size-8 shrink-0 items-center justify-center rounded-lg">
					<HeimdallMark className="size-5" />
				</span>
				<div className={cn("flex min-w-0 flex-col", labelClass)}>
					<span className="text-foreground text-[15px] leading-5 font-semibold tracking-[-0.01em]">{t("app.name")}</span>
					<span className="text-muted-foreground line-clamp-2 text-[11px] leading-snug">{t("app.tagline")}</span>
				</div>
			</div>
			<nav
				aria-label={t("nav.label")}
				className="flex min-h-0 flex-1 gap-1 overflow-x-auto md:flex-col md:gap-4 md:overflow-x-visible md:overflow-y-auto"
			>
				{grouped.map(({ group, items }) => (
					<div key={group} className="flex gap-1 md:flex-col md:gap-0.5" role="group" aria-labelledby={`nav-group-${group}`}>
						<span
							id={`nav-group-${group}`}
							className={cn(
								"text-muted-foreground hidden px-2.5 pb-1 text-[11px] font-medium tracking-[0.06em] uppercase md:block",
								sectionLabelClass,
							)}
						>
							{t(`app.groups.${group}`)}
						</span>
						{/* In the rail, a hairline stands in for the section label. */}
						<span aria-hidden="true" className={cn("bg-border mx-3 mb-1 hidden h-px", rail && "md:block", auto && "md:block xl:hidden")} />
						{items.map(({ to, label, icon: Icon, testId }) => (
							<Link
								key={to}
								to={to}
								data-testid={testId}
								title={label}
								activeOptions={{ exact: to === "/agents" }}
								className={cn(
									"group text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground relative flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] font-medium whitespace-nowrap transition-colors duration-100",
									"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
									rail && "md:justify-center md:px-0",
									auto && "md:justify-center md:px-0 xl:justify-start xl:px-2.5",
								)}
								activeProps={{
									className:
										"bg-sidebar-accent text-sidebar-accent-foreground before:bg-primary before:absolute before:top-1.5 before:bottom-1.5 before:-left-2.5 before:w-[3px] before:rounded-r-full max-md:before:hidden",
									"aria-current": "page",
								}}
							>
								<Icon className="size-4 shrink-0 opacity-90" aria-hidden="true" />
								<span className={labelClass}>{label}</span>
							</Link>
						))}
					</div>
				))}
			</nav>
			<button
				type="button"
				onClick={toggle}
				className={cn(
					"text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground hidden h-8 items-center gap-2.5 rounded-md px-2.5 text-xs transition-colors md:flex",
					"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
					rail && "md:justify-center md:px-0",
					auto && "md:justify-center md:px-0 xl:justify-start xl:px-2.5",
				)}
				aria-label={showsRail ? t("app.expandNav") : t("app.collapseNav")}
				title={showsRail ? t("app.expandNav") : t("app.collapseNav")}
				data-testid="nav-collapse-toggle"
			>
				{showsRail ? (
					<PanelLeftOpen className="size-4 shrink-0" aria-hidden="true" />
				) : (
					<PanelLeftClose className="size-4 shrink-0" aria-hidden="true" />
				)}
				<span className={labelClass}>{showsRail ? t("app.expandNav") : t("app.collapseNav")}</span>
			</button>
		</aside>
	);
}