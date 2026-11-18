import { cn } from "@/lib/utils";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { parseAsString, useQueryState } from "nuqs";
import type { ReactNode } from "react";

export interface RouteTab {
	/** The value of the search parameter, for example "matrix". */
	id: string;
	label: ReactNode;
	/** A tab that does not apply to this object: left out of the strip, never shown empty. */
	hidden?: boolean;
	/** A count or mark after the label. */
	badge?: ReactNode;
	testId?: string;
}

/**
 * The tab to show: the requested one when it is a visible tab, else the default when that is
 * visible, else the first visible tab. Null when every tab is hidden.
 */
export function resolveTab(tabs: readonly RouteTab[], requested: string | null | undefined, defaultTab?: string): string | null {
	const visible = tabs.filter((tab) => !tab.hidden);
	if (requested && visible.some((tab) => tab.id === requested)) return requested;
	if (defaultTab && visible.some((tab) => tab.id === defaultTab)) return defaultTab;
	return visible[0]?.id ?? null;
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
	return [active, select] as const;
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
	const [active, select] = useRouteTab(tabs, { defaultTab, param });
	if (active === null) return null;
	const visible = tabs.filter((tab) => !tab.hidden);
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
			<TabsPrimitive.Content value={active} className="focus-visible:ring-ring/40 min-w-0 rounded-sm outline-none focus-visible:ring-2">
				{children(active)}
			</TabsPrimitive.Content>
		</TabsPrimitive.Root>
	);
}