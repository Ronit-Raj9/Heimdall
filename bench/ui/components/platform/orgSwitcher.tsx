import { useMenuKeyboard } from "@/components/ui/dropdownMenu";
import "@/components/platform/fw23I18n";
import { RoleBadge } from "@/components/platform/badges";
import { CreateOrgDialog } from "@/components/platform/createOrgDialog";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { Building2, Check, ChevronsUpDown, Loader2, Plus } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useOrgSwitch } from "./useOrgSwitch";

/** First letters of an organization name, for its square. */
function orgMark(name: string): string {
	return name.trim().charAt(0).toUpperCase() || "?";
}

export interface OrgSwitcherProps {
	/** Renders the menu open (tests render to static markup and cannot click). */
	defaultOpen?: boolean;
}

/**
 * The organization the session acts in. For a signed-in user the badge is always a menu: the
 * user's organizations (the current one checked; choosing another switches to it) and "Create
 * organization". Switching refetches everything (every bench route is per organization) and drops
 * the page's query string, whose ids belonged to the other organization. An access token belongs
 * to one organization, so it sees the name only.
 */
export function OrgSwitcher({ defaultOpen = false }: OrgSwitcherProps) {
	const { t } = useTranslation("platform");
	const { t: tf } = useTranslation("fw23");
	const session = useSession();
	const state = useOrgSwitch(session.org?.id);
	const [open, setOpen] = useState(defaultOpen);
	const [creating, setCreating] = useState(false);
	const rootRef = useRef<HTMLDivElement>(null);
	const triggerRef = useRef<HTMLButtonElement>(null);
	// Focus moves into the menu on open; arrows, Home and End move between items (M1-39).
	const menuRef = useRef<HTMLDivElement>(null);
	const onMenuKey = useMenuKeyboard(open, menuRef, () => setOpen(false), triggerRef);
	const menuId = useId();

	useEffect(() => {
		if (!open) return;
		const onPointer = (event: PointerEvent) => {
			if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false);
		};
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setOpen(false);
				triggerRef.current?.focus();
			}
		};
		document.addEventListener("pointerdown", onPointer);
		document.addEventListener("keydown", onKey);
		return () => {
			document.removeEventListener("pointerdown", onPointer);
			document.removeEventListener("keydown", onKey);
		};
	}, [open]);

	const org = session.org;
	if (session.authMode !== "hosted") return null;
	// Signed in with memberships but no current org (the current one was just left): requests
	// resolve no org until one is chosen, so the picker is offered instead of nothing.
	if (!org && !(session.signedIn && session.orgs.length > 0)) return null;

	const square = org && (
		<span
			aria-hidden="true"
			className="bg-primary/12 text-primary flex size-6 shrink-0 items-center justify-center rounded-md text-[11px] font-semibold"
		>
			{orgMark(org.name)}
		</span>
	);

	// Not signed in as a user (an access token acts in its one organization): the name, nothing to pick.
	if (org && (!session.signedIn || session.kind === "token")) {
		return (
			<div className="flex min-w-0 items-center gap-2 px-1" data-testid="org-current">
				{square}
				<span className="text-foreground truncate text-sm font-medium">{org.name}</span>
				{session.role && <RoleBadge role={session.role} className="hidden sm:inline-flex" />}
			</div>
		);
	}

	const choose = (orgId: string) => {
		setOpen(false);
		void state.choose(orgId);
	};

	const itemCls =
		"hover:bg-accent focus-visible:bg-accent flex min-h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-1 text-left text-sm focus-visible:outline-none";

	return (
		<div ref={rootRef} className="relative flex min-w-0 items-center gap-2">
			<button
				ref={triggerRef}
				type="button"
				onClick={() => setOpen((value) => !value)}
				disabled={state.isLoading}
				aria-haspopup="menu"
				aria-expanded={open}
				aria-controls={open ? menuId : undefined}
				aria-label={t("header.switchOrg")}
				className="hover:bg-accent focus-visible:ring-(--focus) flex h-8 max-w-64 min-w-0 cursor-pointer items-center gap-2 rounded-md px-1.5 focus-visible:ring-2 focus-visible:outline-none"
				data-testid="org-switcher"
			>
				{square}
				<span className="truncate text-sm font-medium">{org ? org.name : t("header.pickOrg")}</span>
				{state.isLoading ? (
					<Loader2 className="size-3.5 shrink-0 animate-spin" aria-label={t("header.switching")} />
				) : (
					<ChevronsUpDown className="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
				)}
			</button>
			{session.role && <RoleBadge role={session.role} className={cn("hidden lg:inline-flex")} />}
			{open && (
				<div
					ref={menuRef}
					onKeyDown={onMenuKey}
					id={menuId}
					role="menu"
					aria-label={tf("orgs.menu")}
					className={cn(
						"bg-popover text-popover-foreground absolute top-10 left-0 z-50 w-72 origin-top-left rounded-lg border p-1 shadow-(--shadow-pop)",
						"animate-in fade-in-0 zoom-in-95 duration-150",
					)}
					data-testid="org-menu"
				>
					<div className="text-muted-foreground flex items-center gap-1.5 px-2 pt-1.5 pb-1 text-[11px] font-medium tracking-[0.06em] uppercase">
						<Building2 className="size-3" aria-hidden="true" />
						{t("header.org")}
					</div>
					{session.orgs.map((option) => {
						const current = option.id === org?.id;
						return (
							<button
								key={option.id}
								type="button"
								role="menuitemradio"
								aria-checked={current}
								onClick={() => choose(option.id)}
								className={itemCls}
								data-testid={`org-option-${option.slug}`}
							>
								<Check className={cn("size-3.5 shrink-0", current ? "text-primary" : "invisible")} aria-hidden="true" />
								<span className="truncate">{option.name}</span>
								<span className="text-muted-foreground font-mono text-[11px]">{option.slug}</span>
								{option.role && <RoleBadge role={option.role} className="ml-auto" />}
							</button>
						);
					})}
					<div className="bg-border my-1 h-px" />
					<button
						type="button"
						role="menuitem"
						onClick={() => {
							setOpen(false);
							setCreating(true);
						}}
						className={itemCls}
						data-testid="org-create"
					>
						<Plus className="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
						{tf("orgs.create")}
					</button>
				</div>
			)}
			{state.error && (
				<span role="alert" className="text-chart-error-ink truncate text-xs" data-testid="org-switch-error">
					{t("header.switchError", { error: getBenchErrorMessage(state.error) })}
				</span>
			)}
			{creating && <CreateOrgDialog onClose={() => setCreating(false)} />}
		</div>
	);
}