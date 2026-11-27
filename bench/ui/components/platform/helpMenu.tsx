import { useMenuKeyboard } from "@/components/ui/dropdownMenu";
import "@/components/platform/fw23I18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Activity, BookA, BookOpen, CircleHelp, Rocket, type LucideIcon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

interface HelpLink {
	key: "guides" | "glossary" | "quickstart" | "status";
	to: string;
	search?: Record<string, string>;
	icon: LucideIcon;
}

/** Where help lives: the guide list, the glossary, the first-run walkthrough and the server status. */
const HELP_LINKS: readonly HelpLink[] = [
	{ key: "guides", to: "/docs", icon: BookOpen },
	{ key: "glossary", to: "/docs", search: { doc: "glossary" }, icon: BookA },
	{ key: "quickstart", to: "/quickstart", icon: Rocket },
	{ key: "status", to: "/status", icon: Activity },
];

export interface HelpMenuProps {
	/** Renders the menu open (tests render to static markup and cannot click). */
	defaultOpen?: boolean;
}

/** The "?" in the top bar: a menu of help pages. Escape or a click outside closes it. */
export function HelpMenu({ defaultOpen = false }: HelpMenuProps) {
	const { t } = useTranslation("fw23");
	const [open, setOpen] = useState(defaultOpen);
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

	return (
		<div ref={rootRef} className="relative">
			<button
				ref={triggerRef}
				type="button"
				onClick={() => setOpen((value) => !value)}
				aria-haspopup="menu"
				aria-expanded={open}
				aria-controls={open ? menuId : undefined}
				aria-label={t("help.label")}
				title={t("help.label")}
				className="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-(--focus) flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-md transition-colors focus-visible:ring-2 focus-visible:outline-none"
				data-testid="header-help"
			>
				<CircleHelp className="size-4" aria-hidden="true" />
			</button>
			{open && (
				<div
					ref={menuRef}
					onKeyDown={onMenuKey}
					id={menuId}
					role="menu"
					aria-label={t("help.label")}
					className={cn(
						"bg-popover text-popover-foreground absolute top-10 right-0 z-50 w-56 origin-top-right rounded-lg border p-1 shadow-(--shadow-pop)",
						"animate-in fade-in-0 zoom-in-95 duration-150",
					)}
					data-testid="help-menu"
				>
					{HELP_LINKS.map(({ key, to, search, icon: Icon }) => (
						<Link
							key={key}
							to={to as never}
							search={search as never}
							role="menuitem"
							onClick={() => setOpen(false)}
							className="hover:bg-accent focus-visible:bg-accent flex h-8 items-center gap-2 rounded-md px-2.5 text-sm"
							data-testid={`help-menu-${key}`}
						>
							<Icon className="text-muted-foreground size-4" aria-hidden="true" />
							{t(`help.${key}`)}
						</Link>
					))}
				</div>
			)}
		</div>
	);
}