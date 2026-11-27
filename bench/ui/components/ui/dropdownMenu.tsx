import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import type { LucideIcon } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from "react";

/**
 * The one dropdown menu of the app. A button opens a short list of actions or links; focus moves
 * to the first item, ArrowUp and ArrowDown move, Home and End jump, Escape closes and returns focus
 * to the button, a click outside or a route change closes it. Built without a new dependency.
 */

/** The index focus moves to for a menu key, or null when the key is not a menu key. Pure. */
export function menuKeyTarget(key: string, current: number, count: number): number | null {
	if (count <= 0) return null;
	switch (key) {
		case "ArrowDown":
			return current < 0 ? 0 : (current + 1) % count;
		case "ArrowUp":
			return current < 0 ? count - 1 : (current - 1 + count) % count;
		case "Home":
			return 0;
		case "End":
			return count - 1;
		default:
			return null;
	}
}

function menuItems(menu: HTMLElement | null): HTMLElement[] {
	if (!menu) return [];
	return Array.from(menu.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemradio"]:not([aria-disabled="true"])'));
}

/**
 * Keyboard and focus handling for a hand-built menu (header menus keep their own markup). Focuses
 * the first item when it opens; returns the onKeyDown for the menu element.
 */
export function useMenuKeyboard(open: boolean, menuRef: RefObject<HTMLElement | null>, onClose: () => void, triggerRef?: RefObject<HTMLElement | null>) {
	useEffect(() => {
		if (!open) return;
		// After paint, so the items exist.
		const id = requestAnimationFrame(() => {
			const items = menuItems(menuRef.current);
			const checked = items.find((el) => el.getAttribute("aria-checked") === "true");
			(checked ?? items[0])?.focus();
		});
		return () => cancelAnimationFrame(id);
	}, [open, menuRef]);

	return useCallback(
		(e: ReactKeyboardEvent<HTMLElement>) => {
			if (e.key === "Escape") {
				e.preventDefault();
				onClose();
				triggerRef?.current?.focus();
				return;
			}
			if (e.key === "Tab") {
				onClose();
				return;
			}
			const items = menuItems(menuRef.current);
			const current = items.indexOf(document.activeElement as HTMLElement);
			const target = menuKeyTarget(e.key, current, items.length);
			if (target === null) return;
			e.preventDefault();
			items[target]?.focus();
		},
		[menuRef, onClose, triggerRef],
	);
}

export interface DropdownMenuItem {
	label: ReactNode;
	/** A stable key; defaults to the label when it is a string. */
	id?: string;
	icon?: LucideIcon;
	/** A route to open, or an action to run. */
	to?: string;
	params?: Record<string, string>;
	search?: Record<string, unknown>;
	onSelect?: () => void;
	/** Red text: only for the item that leads to a delete confirm. */
	destructive?: boolean;
	disabled?: boolean;
	/** Shown under the label when disabled, so the reason is visible. */
	disabledReason?: string;
	/** Draw a separator line above this item. */
	separatorBefore?: boolean;
	testId?: string;
}

export interface DropdownMenuProps {
	/** Accessible name of the menu and of an icon-only trigger. */
	label: string;
	/** Renders the trigger button; receive the props to spread on it (ref, aria, onClick). */
	trigger: (props: DropdownTriggerProps) => ReactNode;
	items: readonly DropdownMenuItem[];
	align?: "start" | "end";
	className?: string;
	testId?: string;
}

export interface DropdownTriggerProps {
	ref: RefObject<HTMLButtonElement | null>;
	type: "button";
	"aria-haspopup": "menu";
	"aria-expanded": boolean;
	"aria-controls": string | undefined;
	onClick: () => void;
}

export const dropdownContentClass = "bg-card text-card-foreground absolute z-50 mt-1 flex min-w-48 flex-col rounded-lg border p-1 shadow-(--shadow-pop)";
export const dropdownItemClass =
	"hover:bg-muted focus-visible:bg-muted flex min-h-(--control-h) w-full items-center gap-2 rounded-md px-2.5 py-1 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-(--focus) disabled:cursor-not-allowed disabled:opacity-60";

export function DropdownMenu({ label, trigger, items, align = "end", className, testId }: DropdownMenuProps) {
	const [open, setOpen] = useState(false);
	const root = useRef<HTMLDivElement>(null);
	const button = useRef<HTMLButtonElement>(null);
	const menu = useRef<HTMLUListElement>(null);
	const menuId = useId();
	const close = useCallback(() => setOpen(false), []);
	const onKeyDown = useMenuKeyboard(open, menu, close, button);
	const pathname = useSafePathname();

	// Back or any navigation closes the menu.
	useEffect(() => setOpen(false), [pathname]);

	useEffect(() => {
		if (!open) return;
		const onDown = (e: MouseEvent) => {
			if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
		};
		document.addEventListener("mousedown", onDown);
		return () => document.removeEventListener("mousedown", onDown);
	}, [open]);

	return (
		<div ref={root} className={cn("relative", className)} data-testid={testId}>
			{trigger({
				ref: button,
				type: "button",
				"aria-haspopup": "menu",
				"aria-expanded": open,
				"aria-controls": open ? menuId : undefined,
				onClick: () => setOpen((v) => !v),
			})}
			{open && (
				<ul
					ref={menu}
					id={menuId}
					role="menu"
					aria-label={label}
					onKeyDown={onKeyDown}
					className={cn(dropdownContentClass, align === "end" ? "right-0" : "left-0")}
					data-testid={testId ? `${testId}-list` : undefined}
				>
					{items.map((item, index) => {
						const Icon = item.icon;
						const key = item.id ?? (typeof item.label === "string" ? item.label : String(index));
						const body = (
							<>
								{Icon && <Icon className={cn("size-4 shrink-0", item.destructive ? "" : "text-muted-foreground")} aria-hidden="true" />}
								<span className="flex min-w-0 flex-col">
									<span>{item.label}</span>
									{item.disabled && item.disabledReason && <span className="text-muted-foreground text-xs">{item.disabledReason}</span>}
								</span>
							</>
						);
						const cls = cn(dropdownItemClass, item.destructive && "text-danger-ink");
						return (
							<li key={key} role="none" className={cn(item.separatorBefore && index > 0 && "mt-1 border-t pt-1")}>
								{item.to && !item.disabled ? (
									<Link
										role="menuitem"
										tabIndex={-1}
										to={item.to as never}
										params={item.params as never}
										search={item.search as never}
										onClick={() => setOpen(false)}
										className={cls}
										data-testid={item.testId}
									>
										{body}
									</Link>
								) : (
									<button
										type="button"
										role="menuitem"
										tabIndex={-1}
										disabled={item.disabled}
										aria-disabled={item.disabled || undefined}
										onClick={() => {
											setOpen(false);
											item.onSelect?.();
										}}
										className={cls}
										data-testid={item.testId}
									>
										{body}
									</button>
								)}
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}

function useSafePathname(): string | null {
	try {
		return useRouterState({ select: (state) => state.location.pathname });
	} catch {
		// Rendered without a router (an isolated component test).
		return null;
	}
}
