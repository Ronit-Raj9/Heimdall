import { RoleBadge } from "@/components/platform/badges";
import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session/sessionContext";
import { useLogoutMutation } from "@/lib/store/apis/platformApi";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { KeyRound, LogOut, Settings } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { initials } from "./platform.utils";

/**
 * Hosted mode: who is signed in, a way to settings, and sign out. Local mode and demo visitors see
 * nothing here (the demo banner carries the sign-in link).
 */
export function UserMenu() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const [open, setOpen] = useState(false);
	const [logout, logoutState] = useLogoutMutation();
	const rootRef = useRef<HTMLDivElement>(null);
	const triggerRef = useRef<HTMLButtonElement>(null);
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
				className="bg-foreground text-background hover:ring-ring/40 flex size-8 cursor-pointer items-center justify-center rounded-full text-[11px] font-semibold tracking-wide transition-shadow hover:ring-2"
				data-testid="user-menu-trigger"
			>
				{session.kind === "token" ? (
					<KeyRound className="size-3.5" aria-hidden="true" />
				) : (
					initials(session.user?.name, session.user?.email)
				)}
			</button>
			{open && (
				<div
					id={menuId}
					role="menu"
					aria-label={t("header.account")}
					className={cn(
						"bg-popover text-popover-foreground absolute top-10 right-0 z-50 w-64 origin-top-right rounded-lg border p-1 shadow-(--shadow-pop)",
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
			)}
		</div>
	);
}