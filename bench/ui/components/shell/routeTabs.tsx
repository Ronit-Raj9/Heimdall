import { cn } from "@/lib/utils";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, type ReactNode } from "react";
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
 * the parameter, so a page's plain URL is its default tab. Switching replaces the history entry.
 */
export function useRouteTab(tabs: readonly RouteTab[], opts: { defaultTab?: string; param?: string } = {}) {
	const param = opts.param ?? "tab";
	const [requested, setRequested] = useQueryState(param, parseAsString.withOptions({ history: "replace" }));
	const active = resolveTab(tabs, requested, opts.defaultTab);
	const fallback = resolveTab(tabs, null, opts.defaultTab);
	const select = (id: string) => void setRequested(id === fallback ? null : id);
	// An alias in the address is replaced by the tab's own id, so the address names the real tab.
	const alias = aliasTarget(tabs, requested);
	useEffect(() => {
		if (alias) void setRequested(alias === fallback ? null : alias);
	}, [alias, fallback, setRequested]);
	return [active, select, requested] as const;
}

export interface RouteTabsProps {
	tabs: readonly RouteTab[];
	/** The tab shown when the URL names none; the first visible tab when left out. */
	defaultTab?: string;
	/** Search parameter name; "tab" unless a page needs two strips. */
	param?: string;
	/** Accessible name of the tab strip, for example "Experiment views". */
	label: string;
	/** Renders the active tab's content. Only the active tab renders, so hidden work stays idle. */
	children: (active: string) => ReactNode;
	className?: string;
	testId?: string;
}

/**
 * Page level tabs bound to the URL: each tab has its own address, back and reload keep it, and
 * tabs that do not apply are hidden. Arrow keys, Home and End move between tabs (Radix Tabs).
 * On a narrow screen the strip scrolls sideways.
 */
export function RouteTabs({ tabs, defaultTab, param, label, children, className, testId }: RouteTabsProps) {
	const { t } = useTranslation("gw20");
	const [active, select, requested] = useRouteTab(tabs, { defaultTab, param });
	if (active === null) return null;
	// A link with a tab id this page does not have shows the default tab and says so; choosing a tab
	// replaces the parameter, so the note goes away.
	const unknown = unknownTab(tabs, requested);
	const shownLabel = tabs.find((tab) => tab.id === active)?.label;
	// An active addressable tab goes at the end of the strip; it leaves when another tab is chosen.
	const visible = [...tabs.filter((tab) => !tab.hidden), ...tabs.filter((tab) => tab.hidden && tab.addressable && tab.id === active)];
	return (
		<TabsPrimitive.Root
			value={active}
			onValueChange={select}
			className={cn("flex min-w-0 flex-col gap-5", className)}
			data-testid={testId ?? "route-tabs"}
			data-active={active}
		>
			<TabsPrimitive.List
				aria-label={label}
				className="border-border -mx-1 flex min-w-0 items-end gap-1 overflow-x-auto border-b px-1 [scrollbar-width:thin]"
			>
				{visible.map((tab) => (
					<TabsPrimitive.Trigger
						key={tab.id}
						value={tab.id}
						data-testid={tab.testId ?? `tab-${tab.id}`}
						className={cn(
							"text-muted-foreground hover:text-foreground relative -mb-px inline-flex h-9 shrink-0 cursor-pointer items-center gap-1.5 border-b-2 border-transparent px-2.5 text-sm font-medium whitespace-nowrap transition-colors",
							"data-[state=active]:border-primary data-[state=active]:text-foreground",
							"focus-visible:ring-ring/60 rounded-t-sm focus-visible:ring-2 focus-visible:outline-none",
						)}
					>
						{tab.label}
						{tab.badge !== undefined && tab.badge !== null && (
							<span className="bg-muted text-muted-foreground rounded-full px-1.5 text-[11px] leading-4 tabular-nums">{tab.badge}</span>
						)}
					</TabsPrimitive.Trigger>
				))}
			</TabsPrimitive.List>
			{unknown && (
				<p role="status" className="text-muted-foreground text-xs" data-testid="route-tabs-unknown">
					{t("routeTabs.unknown", { tab: unknown, label: typeof shownLabel === "string" ? shownLabel : active })}
				</p>
			)}
			<TabsPrimitive.Content value={active} className="focus-visible:ring-ring/40 min-w-0 rounded-sm outline-none focus-visible:ring-2">
				{children(active)}
			</TabsPrimitive.Content>
		</TabsPrimitive.Root>
	);
}