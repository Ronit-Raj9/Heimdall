import { cn } from "@/lib/utils";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { parseAsString, useQueryState } from "nuqs";
import { useScrollEdges } from "@/lib/hooks/useScrollEdges";
import { useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "./gw20I18n";

export interface RouteTab {
	/** The value of the search parameter, for example "matrix". */
	id: string;
	label: ReactNode;
	/** A tab that does not apply to this object: left out of the strip, never shown empty. */
	hidden?: boolean;
	/**
	 * A hidden tab that still opens when the URL names it (a menu links to it). It is never the
	 * default; while it is active the strip shows it at its end so the user sees where they are.
	 */
	addressable?: boolean;
	/** Other ids that open this tab (an old or a natural name, for example "files" for "edit"); the address is then rewritten to id. */
	aliases?: readonly string[];
	/** A count or mark after the label. */
	badge?: ReactNode;
	testId?: string;
}

/**
 * The tab to show: the requested one when it is a visible or an addressable tab, else the default
 * when that is visible, else the first visible tab. Null when every tab is hidden.
 */
export function resolveTab(tabs: readonly RouteTab[], requested: string | null | undefined, defaultTab?: string): string | null {
	const visible = tabs.filter((tab) => !tab.hidden);
	const want = aliasTarget(tabs, requested) ?? requested;
	if (want && tabs.some((tab) => tab.id === want && (!tab.hidden || tab.addressable))) return want;
	if (defaultTab && visible.some((tab) => tab.id === defaultTab)) return defaultTab;
	return visible[0]?.id ?? null;
}

/**
 * The tab id an alias in the address stands for (fix wave 3, N271: "?tab=files" opens Edit files), or
 * null when requested is no alias. A real tab id always wins over another tab's alias.
 */
export function aliasTarget(tabs: readonly RouteTab[], requested: string | null | undefined): string | null {
	if (!requested || tabs.some((tab) => tab.id === requested)) return null;
	return tabs.find((tab) => tab.aliases?.includes(requested))?.id ?? null;
}

/**
 * The requested tab id when no tab of the page has it (visible, hidden or addressable), else null. A
 * hidden tab that exists is not unknown: the page has its own note for it. Exported for tests.
 */
export function unknownTab(tabs: readonly RouteTab[], requested: string | null | undefined): string | null {
	if (!requested || tabs.some((tab) => tab.id === requested) || aliasTarget(tabs, requested)) return null;
	return requested;
}

/**
 * The active tab bound to a search parameter (default "tab"). Choosing the default tab removes
 * the parameter, so a page's plain URL is its default tab. Choosing a tab adds a history entry, so
 * Back steps through the tabs before it leaves the page (M1-10); pass history "replace" to opt out.
 */
export function useRouteTab(tabs: readonly RouteTab[], opts: { defaultTab?: string; param?: string; history?: "push" | "replace" } = {}) {
	const param = opts.param ?? "tab";
	const history = opts.history ?? "push";
	const [requested, setRequested] = useQueryState(param, parseAsString);
	const active = resolveTab(tabs, requested, opts.defaultTab);
	const fallback = resolveTab(tabs, null, opts.defaultTab);
	const select = (id: string) => void setRequested(id === fallback ? null : id, { history });
	// An alias in the address is replaced by the tab's own id, so the address names the real tab.
	const alias = aliasTarget(tabs, requested);
	useEffect(() => {
		if (alias) void setRequested(alias === fallback ? null : alias, { history: "replace" });
	}, [alias, fallback, setRequested]);
	return [active, select, requested] as const;
}

export interface RouteTabsProps {
	tabs: readonly RouteTab[];
	/** The tab shown when the URL names none; the first visible tab when left out. */
	defaultTab?: string;
	/** Search parameter name; "tab" unless a page needs two strips. */
	param?: string;
	/** "push" (default): Back steps through the tabs. "replace": a tab change keeps one history entry. */
	history?: "push" | "replace";
	/** Accessible name of the tab strip, for example "Experiment views". */
	label: string;
	/** Renders the active tab's content. Only the active tab renders, so hidden work stays idle. */
	children: (active: string) => ReactNode;
	className?: string;
	testId?: string;
	/** Shown at the end of the strip, after the scroller and outside its fade: a More menu (M4-14). */
	trailing?: ReactNode;
}

/**
 * The scrollLeft that puts a tab in the middle of the strip, clamped to the scroll range. Pure.
 * stripLeft and tabLeft are viewport x positions; scrollLeft is the strip's current scroll.
 */
export function centeredScrollLeft(scrollLeft: number, stripLeft: number, stripWidth: number, tabLeft: number, tabWidth: number, scrollWidth: number): number {
	const target = scrollLeft + (tabLeft - stripLeft) - (stripWidth - tabWidth) / 2;
	return Math.max(0, Math.min(Math.max(0, scrollWidth - stripWidth), Math.round(target)));
}

/**
 * Page level tabs bound to the URL: each tab has its own address, back and reload keep it, and
 * tabs that do not apply are hidden. Arrow keys, Home and End move between tabs (Radix Tabs).
 * On a narrow screen the strip scrolls sideways, fades at the edge that hides tabs, and keeps the
 * active tab in view.
 */
export function RouteTabs({ tabs, defaultTab, param, history, label, children, className, testId, trailing }: RouteTabsProps) {
	const { t } = useTranslation("gw20");
	const [active, select, requested] = useRouteTab(tabs, { defaultTab, param, history });
	const list = useRef<HTMLDivElement>(null);
	useScrollEdges(list);
	// The active tab is scrolled into view on load and after every change (X-95).
	useEffect(() => {
		const strip = list.current;
		const trigger = strip?.querySelector<HTMLElement>('[data-state="active"]');
		if (!strip || !trigger) return;
		// Centred, so the edge fade never covers the active tab on a phone (Q-105).
		const s = strip.getBoundingClientRect();
		const r = trigger.getBoundingClientRect();
		strip.scrollLeft = centeredScrollLeft(strip.scrollLeft, s.left, s.width, r.left, r.width, strip.scrollWidth);
	}, [active]);
	if (active === null) return null;
	// A link with a tab id this page does not have shows the default tab and says so; choosing a tab
	// replaces the parameter, so the note goes away.
	const unknown = unknownTab(tabs, requested);
	const shownLabel = tabs.find((tab) => tab.id === active)?.label;
	// An active addressable tab goes at the end of the strip; it leaves when another tab is chosen.
	const visible = [...tabs.filter((tab) => !tab.hidden), ...tabs.filter((tab) => tab.hidden && tab.addressable && tab.id === active)];
	const tabList = (
		<TabsPrimitive.List
			ref={list}
			aria-label={label}
			className={cn(
				"bench-no-scrollbar flex min-w-0 items-end gap-1 overflow-x-auto overflow-y-hidden overscroll-x-contain",
				// With a trailing slot the strip ends at the slot's edge and keeps its own end padding, so the last tab is never under it.
				trailing ? "-mb-px -ml-1 flex-1 pr-3 pl-1" : "border-border -mx-1 border-b px-1",
			)}
		>
			{visible.map((tab) => (
				<TabsPrimitive.Trigger
					key={tab.id}
					value={tab.id}
					data-testid={tab.testId ?? `tab-${tab.id}`}
					className={cn(
						"text-muted-foreground hover:text-foreground relative -mb-px inline-flex h-9 shrink-0 cursor-pointer items-center gap-1.5 border-b-2 border-transparent px-2.5 text-sm font-medium whitespace-nowrap transition-colors",
						"data-[state=active]:border-primary data-[state=active]:text-foreground",
						"rounded-t-sm focus-visible:ring-2 focus-visible:ring-(--focus) focus-visible:outline-none",
					)}
				>
					{tab.label}
					{tab.badge !== undefined && tab.badge !== null && (
						<span className="bg-muted text-muted-foreground rounded-full px-1.5 text-[11px] leading-4 tabular-nums">{tab.badge}</span>
					)}
				</TabsPrimitive.Trigger>
			))}
		</TabsPrimitive.List>
	);
	return (
		<TabsPrimitive.Root
			value={active}
			onValueChange={select}
			className={cn("flex min-w-0 flex-col gap-5", className)}
			data-testid={testId ?? "route-tabs"}
			data-active={active}
		>
			{trailing ? (
				<div className="border-border flex min-w-0 items-end gap-2 border-b">
					{tabList}
					<div className="flex shrink-0 items-center pb-1" data-testid="route-tabs-trailing">
						{trailing}
					</div>
				</div>
			) : (
				tabList
			)}
			{unknown && (
				<p role="status" className="text-muted-foreground text-xs" data-testid="route-tabs-unknown">
					{t("routeTabs.unknown", { tab: unknown, label: typeof shownLabel === "string" ? shownLabel : active })}
				</p>
			)}
			<TabsPrimitive.Content value={active} className="min-w-0 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-(--focus)">
				{children(active)}
			</TabsPrimitive.Content>
		</TabsPrimitive.Root>
	);
}