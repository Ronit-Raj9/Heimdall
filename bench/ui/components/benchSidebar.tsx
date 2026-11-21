import { HeimdallMark } from "@/components/heimdallMark";
import { OrgSwitcher } from "@/components/platform/orgSwitcher";
import { BENCH_NAV_GROUPS, DEFAULT_CLOSED_GROUPS, activeNavGroup, isNavActive, navHintKey, type BenchNavGroupId, type BenchNavItem } from "@/lib/constants/navigation";
import { useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronDown, PanelLeftClose, PanelLeftOpen, X } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

const COLLAPSE_KEY = "heimdall.nav-collapsed";
const GROUPS_KEY = "heimdall.nav-groups";

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

function readClosedGroups(): BenchNavGroupId[] {
	try {
		const raw = window.localStorage.getItem(GROUPS_KEY);
		// Nothing stored yet: the Advanced group starts closed.
		if (raw === null) return [...DEFAULT_CLOSED_GROUPS];
		const parsed: unknown = JSON.parse(raw);
		return Array.isArray(parsed) ? parsed.filter((id): id is BenchNavGroupId => BENCH_NAV_GROUPS.some((group) => group.id === id)) : [];
	} catch {
		return [...DEFAULT_CLOSED_GROUPS];
	}
}

function writeClosedGroups(ids: readonly BenchNavGroupId[]) {
	try {
		window.localStorage.setItem(GROUPS_KEY, JSON.stringify(ids));
	} catch {
		// Storage blocked: the groups reopen on the next page load.
	}
}

/** The groups and entries this session may see: Settings only for a signed-in hosted user. */
export function visibleNavGroups(session: SessionInfo) {
	const hosted = session.authMode === "hosted" && session.signedIn;
	return BENCH_NAV_GROUPS.map((group) => ({ ...group, items: group.items.filter((item) => (!item.hostedOnly || hosted) && (!item.serverAdminOnly || session.serverAdmin === true)) })).filter(
		(group) => group.items.length > 0,
	);
}

/**
 * How the nav shows. auto: an icon rail from md to xl, labels from xl. rail or full: pinned by the
 * user's toggle. The mobile drawer always shows the full form.
 */
type NavForm = "auto" | "rail" | "full";

export interface BenchSidebarProps {
	/** Below md the nav is a drawer the header's menu button opens. */
	drawerOpen?: boolean;
	onDrawerClose?: () => void;
}

/**
 * Left navigation in five collapsible groups. From md it is a sidebar (an icon rail up to xl,
 * labels from xl, either pinned by the toggle); below md it is a drawer over the page.
 */
export function BenchSidebar({ drawerOpen = false, onDrawerClose }: BenchSidebarProps) {
	const { t } = useTranslation("bench");
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	// null: follow the width (rail below xl). true or false: the user's choice.
	const [collapsed, setCollapsed] = useState<boolean | null>(null);
	const [closedGroups, setClosedGroups] = useState<BenchNavGroupId[]>(() => [...DEFAULT_CLOSED_GROUPS]);
	useEffect(() => {
		setCollapsed(readCollapsed());
		setClosedGroups(readClosedGroups());
	}, []);
	// Arriving on a page of a closed group opens that group, so the current entry is always visible.
	const currentGroup = activeNavGroup(pathname);
	useEffect(() => {
		if (!currentGroup) return;
		setClosedGroups((prev) => (prev.includes(currentGroup) ? prev.filter((id) => id !== currentGroup) : prev));
	}, [currentGroup]);
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

	const form: NavForm = collapsed === null ? "auto" : collapsed ? "rail" : "full";
	const showsRail = form === "rail" || (form === "auto" && !wide);
	const toggle = () => {
		const next = !showsRail;
		setCollapsed(next);
		writeCollapsed(next);
	};
	const toggleGroup = (id: BenchNavGroupId) => {
		setClosedGroups((prev) => {
			const next = prev.includes(id) ? prev.filter((group) => group !== id) : [...prev, id];
			writeClosedGroups(next);
			return next;
		});
	};

	const labelClass = cn(form === "rail" && "md:sr-only", form === "auto" && "md:sr-only xl:not-sr-only");

	return (
		<>
			<aside
				className={cn(
					"bg-sidebar hidden shrink-0 flex-col gap-2 px-2.5 py-3 md:flex md:min-h-0",
					"transition-[width] duration-200 ease-(--ease-out)",
					form === "rail" && "md:w-[3.75rem]",
					form === "auto" && "md:w-[3.75rem] xl:w-60",
					form === "full" && "md:w-60",
				)}
				data-testid="bench-sidebar"
				data-state={form === "auto" ? "auto" : form === "rail" ? "collapsed" : "expanded"}
			>
				<Brand form={form} />
				<SidebarOrg form={form} />
				<NavGroups form={form} pathname={pathname} closedGroups={closedGroups} onToggleGroup={toggleGroup} idPrefix="nav" />
				<button
					type="button"
					onClick={toggle}
					className={cn(
						"text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground flex h-8 items-center gap-2.5 rounded-md px-2.5 text-xs transition-colors",
						"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
						form === "rail" && "justify-center px-0",
						form === "auto" && "justify-center px-0 xl:justify-start xl:px-2.5",
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
			{drawerOpen && (
				<NavDrawer
					pathname={pathname}
					closedGroups={closedGroups}
					onToggleGroup={toggleGroup}
					onClose={onDrawerClose ?? (() => undefined)}
				/>
			)}
		</>
	);
}

function Brand({ form }: { form: NavForm }) {
	const { t } = useTranslation("bench");
	const labelClass = cn(form === "rail" && "md:sr-only", form === "auto" && "md:sr-only xl:not-sr-only");
	return (
		<Link
			to="/"
			className={cn(
				"focus-visible:ring-ring/60 flex h-10 items-center gap-2.5 rounded-lg px-1.5 focus-visible:ring-2 focus-visible:outline-none",
				form === "rail" && "md:justify-center md:px-0",
				form === "auto" && "md:justify-center md:px-0 xl:justify-start xl:px-1.5",
			)}
			data-testid="nav-brand"
		>
			<span className="bg-foreground text-background flex size-8 shrink-0 items-center justify-center rounded-lg">
				<HeimdallMark className="size-5" />
			</span>
			<span className={cn("text-foreground truncate text-base leading-5 font-semibold tracking-[-0.015em]", labelClass)} title={t("app.tagline")}>
				{t("app.name")}
			</span>
		</Link>
	);
}

/**
 * The organization at the top of the sidebar (hosted mode only; OrgSwitcher renders nothing in
 * local mode). The icon rail has no room for it, so it shows where labels show.
 */
function SidebarOrg({ form }: { form: NavForm }) {
	return (
		<div
			className={cn(
				"border-sidebar-border bg-card/60 min-w-0 rounded-lg border px-1 py-0.5 empty:hidden",
				form === "rail" && "md:hidden",
				form === "auto" && "md:hidden xl:block",
			)}
			data-testid="sidebar-org"
		>
			<OrgSwitcher />
		</div>
	);
}

interface NavGroupsProps {
	form: NavForm;
	pathname: string;
	closedGroups: readonly BenchNavGroupId[];
	onToggleGroup: (id: BenchNavGroupId) => void;
	/** Keeps element ids unique when the sidebar and the drawer are both in the page. */
	idPrefix: string;
	onNavigate?: () => void;
}

/** The grouped entries. Group headers toggle their group; in the icon rail a hairline stands in for them. */
export function NavGroups({ form, pathname, closedGroups, onToggleGroup, idPrefix, onNavigate }: NavGroupsProps) {
	const { t } = useTranslation("bench");
	const session = useSession();
	const groups = visibleNavGroups(session);
	const current = activeNavGroup(pathname);
	const labelClass = cn(form === "rail" && "md:sr-only", form === "auto" && "md:sr-only xl:not-sr-only");
	// Where group headers show: everywhere but the rail.
	const headerClass = cn(form === "rail" && "md:hidden", form === "auto" && "md:hidden xl:flex");
	const hairlineClass = cn("bg-border mx-3 mb-1 hidden h-px", form === "rail" && "md:block", form === "auto" && "md:block xl:hidden");
	// A closed group hides its entries only where its header shows, so the rail always lists every entry.
	const closedItemsClass = form === "full" ? "hidden" : form === "auto" ? "xl:hidden" : "";

	return (
		<nav aria-label={t("nav.label")} className="-mx-1 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-1 pt-1 [scrollbar-width:thin]" data-testid={`${idPrefix}-groups`}>
			{groups.map((group) => {
				const label = t(group.labelKey);
				const closed = closedGroups.includes(group.id);
				const listId = `${idPrefix}-group-${group.id}`;
				return (
					<div key={group.id} className="flex flex-col gap-0.5" data-testid={`nav-group-${group.id}`} data-closed={closed || undefined}>
						<button
							type="button"
							onClick={() => onToggleGroup(group.id)}
							aria-expanded={!closed}
							aria-controls={listId}
							title={t("shell.groupToggle", { group: label })}
							className={cn(
								"text-muted-foreground/80 hover:text-foreground flex h-6 items-center gap-1 rounded-sm px-2.5 text-[11px] font-semibold tracking-[0.05em] uppercase transition-colors",
								"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
								headerClass,
							)}
							data-testid={`nav-group-toggle-${group.id}`}
						>
							<span className="flex-1 text-left">{label}</span>
							{closed && current === group.id && <span aria-hidden="true" className="bg-primary size-1.5 rounded-full" />}
							<ChevronDown
								className={cn("size-3.5 shrink-0 transition-transform duration-150", closed && "-rotate-90")}
								aria-hidden="true"
							/>
						</button>
						<span aria-hidden="true" className={hairlineClass} />
						<ul id={listId} aria-label={label} className={cn("flex flex-col gap-0.5", closed && closedItemsClass)}>
							{group.items.map((item) => (
								<li key={item.to}>
									<NavLink item={item} active={isNavActive(item, pathname)} form={form} labelClass={labelClass} onNavigate={onNavigate} />
								</li>
							))}
						</ul>
					</div>
				);
			})}
		</nav>
	);
}

function NavLink({
	item,
	active,
	form,
	labelClass,
	onNavigate,
}: {
	item: BenchNavItem;
	active: boolean;
	form: NavForm;
	labelClass: string;
	onNavigate?: () => void;
}) {
	const { t } = useTranslation("bench");
	const label = t(item.labelKey);
	// One plain sentence on what the page is for, on hover (and the label too, for the icon rail).
	const hint = t(navHintKey(item) as never, { defaultValue: "" }) as string;
	const Icon = item.icon;
	return (
		<Link
			to={item.to as never}
			data-testid={item.testId}
			data-active={active || undefined}
			title={hint ? `${label}: ${hint}` : label}
			aria-current={active ? "page" : undefined}
			onClick={onNavigate}
			className={cn(
				"group text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground relative flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-sm font-medium whitespace-nowrap transition-colors duration-150",
				"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
				form === "rail" && "md:justify-center md:px-0",
				form === "auto" && "md:justify-center md:px-0 xl:justify-start xl:px-2.5",
				active && "bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary dark:bg-primary/15 dark:hover:bg-primary/20",
			)}
		>
			<Icon className={cn("size-4 shrink-0", active ? "opacity-100" : "opacity-80 group-hover:opacity-100")} aria-hidden="true" />
			<span className={labelClass}>{label}</span>
		</Link>
	);
}

const FOCUSABLE = 'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * The navigation as a modal drawer below md. Focus moves to the close button and stays inside
 * until it closes (Escape, the backdrop, the close button or following a link); then it returns
 * to whatever opened it.
 */
function NavDrawer({
	pathname,
	closedGroups,
	onToggleGroup,
	onClose,
}: {
	pathname: string;
	closedGroups: readonly BenchNavGroupId[];
	onToggleGroup: (id: BenchNavGroupId) => void;
	onClose: () => void;
}) {
	const { t } = useTranslation("bench");
	const panel = useRef<HTMLDivElement>(null);
	const closeButton = useRef<HTMLButtonElement>(null);
	// The latest close handler, so the open effect runs once per opening.
	const close = useRef(onClose);
	close.current = onClose;

	useEffect(() => {
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		closeButton.current?.focus();
		const overflow = document.body.style.overflow;
		document.body.style.overflow = "hidden";
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape") close.current();
		};
		window.addEventListener("keydown", onKey);
		// Past md the drawer has no place: close it so the sidebar takes over.
		const query = window.matchMedia?.("(min-width: 48rem)");
		const onWide = () => query?.matches && close.current();
		query?.addEventListener("change", onWide);
		return () => {
			window.removeEventListener("keydown", onKey);
			query?.removeEventListener("change", onWide);
			document.body.style.overflow = overflow;
			opener?.focus();
		};
	}, []);

	// Keep Tab inside the drawer.
	const trap = (event: ReactKeyboardEvent<HTMLDivElement>) => {
		if (event.key !== "Tab" || !panel.current) return;
		const items = Array.from(panel.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.offsetParent !== null);
		if (items.length === 0) return;
		const first = items[0];
		const last = items[items.length - 1];
		if (event.shiftKey && document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	};

	return (
		<div className="fixed inset-0 z-50 md:hidden" data-testid="nav-drawer">
			<div
				className="animate-in fade-in absolute inset-0 bg-black/40 backdrop-blur-[2px] duration-200"
				aria-hidden="true"
				onClick={onClose}
				data-testid="nav-drawer-backdrop"
			/>
			<div
				ref={panel}
				role="dialog"
				aria-modal="true"
				aria-label={t("nav.label")}
				onKeyDown={trap}
				className="bg-sidebar animate-in slide-in-from-left absolute inset-y-0 left-0 flex w-[17rem] max-w-[85vw] flex-col gap-2 border-r px-2.5 py-3 shadow-2xl duration-200 ease-(--ease-out)"
			>
				<div className="flex items-center gap-1">
					<div className="min-w-0 flex-1" onClick={onClose}>
						<Brand form="full" />
					</div>
					<button
						ref={closeButton}
						type="button"
						onClick={onClose}
						aria-label={t("shell.closeMenu")}
						className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground focus-visible:ring-ring/60 flex size-8 shrink-0 items-center justify-center rounded-md focus-visible:ring-2 focus-visible:outline-none"
						data-testid="nav-drawer-close"
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</div>
				<SidebarOrg form="full" />
				<NavGroups
					form="full"
					pathname={pathname}
					closedGroups={closedGroups}
					onToggleGroup={onToggleGroup}
					idPrefix="drawer"
					onNavigate={onClose}
				/>
			</div>
		</div>
	);
}