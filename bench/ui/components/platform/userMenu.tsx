import { MenuLayer, useMenuKeyboard, useMenuPlacement } from "@/components/ui/dropdownMenu";
import { RoleBadge } from "@/components/platform/badges";
import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session/sessionContext";
import { useLogoutMutation } from "@/lib/store/apis/platformApi";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import "@/components/platform/fw23I18n";
import { CreateOrgDialog } from "@/components/platform/createOrgDialog";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { ArrowLeftRight, KeyRound, LogOut, Plus, Settings } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { initials } from "./platform.utils";
import { useOrgSwitch } from "./useOrgSwitch";

/**
 * Hosted mode: who is signed in, a way to settings, and sign out. Local mode and demo visitors see
 * nothing here (the demo banner carries the sign-in link).
 */
export interface UserMenuProps {
	/** Renders the menu open (tests render to static markup and cannot click). */
	defaultOpen?: boolean;
}

export function UserMenu({ defaultOpen = false }: UserMenuProps = {}) {
	const { t } = useTranslation("platform");
	const { t: tf } = useTranslation("fw23");
	const session = useSession();
	const [open, setOpen] = useState(defaultOpen);
	const [creating, setCreating] = useState(false);
	const orgSwitch = useOrgSwitch(session.org?.id);
	const [logout, logoutState] = useLogoutMutation();
	const rootRef = useRef<HTMLDivElement>(null);
	const triggerRef = useRef<HTMLButtonElement>(null);
	// Focus moves into the menu on open; arrows, Home and End move between items (M1-39).
	const menuRef = useRef<HTMLDivElement>(null);
	const onMenuKey = useMenuKeyboard(open, menuRef, () => setOpen(false), triggerRef);
	const menuId = useId();
	const menuStyle = useMenuPlacement(open, triggerRef, menuRef, "end");
	const errorRef = useRef<HTMLParagraphElement>(null);
	const errorStyle = useMenuPlacement(Boolean(orgSwitch.error), triggerRef, errorRef, "end");

	useEffect(() => {
		if (!open) return;
		const onPointer = (event: PointerEvent) => {
			// The menu lives in the body layer, so a press inside it is not outside.
			const target = event.target as Node;
			if (rootRef.current && !rootRef.current.contains(target) && !menuRef.current?.contains(target)) setOpen(false);
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

	// Local mode has no account; a demo visitor signs in from the demo banner.
	if (session.authMode !== "hosted" || !session.signedIn) return null;

	const signOut = async () => {
		try {
			await logout().unwrap();
		} catch {
			// Signed out or not, the login page is where to go; a stale cookie fails there.
		}
		window.location.assign("/login");
	};

	const label = session.user?.name || session.user?.email || t("header.viaToken");
	// A user (not an access token, which belongs to one organization) can switch from here too:
	// the sidebar badge is easy to miss on the icon rail (N143).
	const canSwitch = session.kind !== "token";
	const others = canSwitch ? session.orgs.filter((option) => option.id !== session.org?.id) : [];
	const itemCls =
		"hover:bg-accent focus-visible:bg-accent flex min-h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-1 text-left text-sm";
	return (
		<div ref={rootRef} className="relative">
			<button
				ref={triggerRef}
				type="button"
				onClick={() => setOpen((value) => !value)}
				aria-haspopup="menu"
				aria-expanded={open}
				aria-controls={open ? menuId : undefined}
				aria-label={t("header.account")}
				title={session.user?.email ?? label}
				className="bg-foreground text-background hover:ring-(--focus)/40 flex size-8 cursor-pointer items-center justify-center rounded-full text-[11px] font-semibold tracking-wide transition-shadow hover:ring-2"
				data-testid="user-menu-trigger"
			>
				{session.kind === "token" ? (
					<KeyRound className="size-3.5" aria-hidden="true" />
				) : (
					initials(session.user?.name, session.user?.email)
				)}
			</button>
			{open && (
				<MenuLayer>
				<div
					ref={menuRef}
					style={menuStyle}
					onKeyDown={onMenuKey}
					id={menuId}
					role="menu"
					aria-label={t("header.account")}
					className={cn(
						"bg-popover text-popover-foreground z-50 w-64 origin-top-right rounded-lg border p-1 shadow-(--shadow-pop)",
						"animate-in fade-in-0 zoom-in-95 duration-150",
					)}
					data-testid="user-menu"
				>
					<div className="flex flex-col gap-1 px-2.5 py-2">
						<span className="text-muted-foreground text-[11px]">{t("header.signedInAs")}</span>
						<span className="text-foreground truncate text-sm font-medium">{label}</span>
						{session.user?.email && session.user.email !== label && (
							<span className="text-muted-foreground truncate font-mono text-xs">{session.user.email}</span>
						)}
						{session.role && session.org && (
							<span className="text-muted-foreground mt-1 flex items-center gap-1.5 text-xs">
								<RoleBadge role={session.role} />
								<span className="truncate">{session.org.name}</span>
							</span>
						)}
					</div>
					{others.length > 0 && (
						<>
							<div className="bg-border my-1 h-px" />
							<div
								className="text-muted-foreground flex items-center gap-1.5 px-2.5 pt-1 pb-0.5 text-[11px] font-medium"
								data-testid="user-menu-switch"
							>
								<ArrowLeftRight className="size-3" aria-hidden="true" />
								{tf("account.switchOrg")}
							</div>
							{others.map((option) => (
								<button
									key={option.id}
									type="button"
									role="menuitem"
									onClick={() => {
										setOpen(false);
										void orgSwitch.choose(option.id);
									}}
									disabled={orgSwitch.isLoading}
									className={itemCls}
									data-testid={`user-menu-org-${option.slug}`}
								>
									<span className="truncate">{option.name}</span>
									{option.role && <RoleBadge role={option.role} className="ml-auto" />}
								</button>
							))}
						</>
					)}
					{canSwitch && others.length === 0 && session.orgs.length <= 1 && (
						<>
							<div className="bg-border my-1 h-px" />
							<button
								type="button"
								role="menuitem"
								onClick={() => {
									setOpen(false);
									setCreating(true);
								}}
								className={itemCls}
								data-testid="user-menu-create-org"
							>
								<Plus className="text-muted-foreground size-4" aria-hidden="true" />
								{tf("orgs.create")}
							</button>
						</>
					)}
					<div className="bg-border my-1 h-px" />
					<Link
						to="/settings"
						role="menuitem"
						onClick={() => setOpen(false)}
						className="hover:bg-accent focus-visible:bg-accent flex h-8 items-center gap-2 rounded-md px-2.5 text-sm"
						data-testid="user-menu-settings"
					>
						<Settings className="text-muted-foreground size-4" aria-hidden="true" />
						{t("header.settings")}
					</Link>
					<button
						type="button"
						role="menuitem"
						onClick={() => void signOut()}
						disabled={logoutState.isLoading}
						className="hover:bg-accent focus-visible:bg-accent flex h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 text-left text-sm"
						data-testid="user-menu-sign-out"
					>
						<LogOut className="text-muted-foreground size-4" aria-hidden="true" />
						{t("header.signOut")}
					</button>
				</div>
				</MenuLayer>
			)}
			{orgSwitch.error && (
				<MenuLayer>
				<p
					ref={errorRef}
					style={errorStyle}
					role="alert"
					className="text-chart-error-ink bg-popover z-50 w-64 rounded-md border px-2.5 py-1.5 text-xs"
					data-testid="user-menu-switch-error"
				>
					{tf("account.switchError", { error: getBenchErrorMessage(orgSwitch.error) })}
				</p>
				</MenuLayer>
			)}
			{creating && <CreateOrgDialog onClose={() => setCreating(false)} />}
		</div>
	);
}