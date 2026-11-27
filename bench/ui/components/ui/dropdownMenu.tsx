import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import type { LucideIcon } from "lucide-react";
import { createPortal } from "react-dom";
import {
	useCallback,
	useEffect,
	useId,
	useLayoutEffect,
	useRef,
	useState,
	type CSSProperties,
	type KeyboardEvent as ReactKeyboardEvent,
	type ReactNode,
	type RefObject,
} from "react";

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
	return Array.from(
		menu.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemradio"]:not([aria-disabled="true"])'),
	);
}

/**
 * Keyboard and focus handling for a hand-built menu (header menus keep their own markup). Focuses
 * the first item when it opens; returns the onKeyDown for the menu element.
 */
export function useMenuKeyboard(
	open: boolean,
	menuRef: RefObject<HTMLElement | null>,
	onClose: () => void,
	triggerRef?: RefObject<HTMLElement | null>,
) {
	useEffect(() => {
		if (!open) return;
		// After paint, so the items exist.
		const id = requestAnimationFrame(() => {
			const items = menuItems(menuRef.current);
			const checked = items.find((el) => el.getAttribute("aria-checked") === "true");
			const first = checked ?? items[0] ?? menuRef.current?.querySelector<HTMLElement>("button:not([disabled]), a[href], input, select");
			first?.focus();
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
	/**
	 * Free content instead of items, for a row menu whose action keeps its own state (a delete with
	 * its error line). Shown in the same layer and place; Escape and an outside click close it.
	 */
	content?: ReactNode;
	align?: "start" | "end";
	className?: string;
	/** Classes for the list itself, for example "w-72" for items with a second line (X-42). */
	contentClassName?: string;
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

export const dropdownContentClass =
	"bg-card text-card-foreground absolute z-50 mt-1 flex min-w-48 flex-col rounded-lg border p-1 shadow-(--shadow-pop)";
export const dropdownItemClass =
	"hover:bg-muted focus-visible:bg-muted flex min-h-(--control-h) w-full items-center gap-2 rounded-md px-2.5 py-1 text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-(--focus) disabled:cursor-not-allowed disabled:opacity-60";

export function DropdownMenu({ label, trigger, items, content, align = "end", className, contentClassName, testId }: DropdownMenuProps) {
	const [open, setOpen] = useState(false);
	const root = useRef<HTMLDivElement>(null);
	const button = useRef<HTMLButtonElement>(null);
	const menu = useRef<HTMLElement>(null);
	const menuId = useId();
	const close = useCallback(() => setOpen(false), []);
	const onKeyDown = useMenuKeyboard(open, menu, close, button);
	const pathname = useSafePathname();

	const [pos, setPos] = useState<MenuPosition | null>(null);
	const place = useCallback(() => {
		const b = button.current;
		const m = menu.current;
		if (!b || !m) return;
		setPos(menuPosition(b.getBoundingClientRect(), m.getBoundingClientRect(), window.innerWidth, window.innerHeight, align));
	}, [align]);
	useLayoutEffect(() => {
		if (!open) {
			setPos(null);
			return;
		}
		place();
		window.addEventListener("resize", place);
		window.addEventListener("scroll", place, true);
		return () => {
			window.removeEventListener("resize", place);
			window.removeEventListener("scroll", place, true);
		};
	}, [open, place]);

	// Back or any navigation closes the menu.
	useEffect(() => setOpen(false), [pathname]);

	useEffect(() => {
		if (!open) return;
		const onDown = (e: MouseEvent) => {
			const t = e.target as Node;
			if (root.current?.contains(t) || menu.current?.contains(t)) return;
			// A confirm dialog opened from the menu is part of it: a click there keeps the menu.
			if (content !== undefined && (t as Element).closest?.('[role="dialog"], [role="alertdialog"]')) return;
			setOpen(false);
		};
		document.addEventListener("mousedown", onDown);
		return () => document.removeEventListener("mousedown", onDown);
	}, [open, content]);

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
			{open &&
				content !== undefined &&
				portal(
					<div
						ref={menu as RefObject<HTMLDivElement>}
						id={menuId}
						role="dialog"
						aria-label={label}
						onKeyDown={onKeyDown}
						className={cn(dropdownContentClass, "fixed mt-0", contentClassName)}
						style={pos ? { top: pos.top, left: pos.left } : { top: 0, left: 0, visibility: "hidden" }}
						data-placement={pos?.placement}
						data-testid={testId ? `${testId}-list` : undefined}
					>
						{content}
					</div>,
				)}
			{open &&
				content === undefined &&
				portal(
				<ul
					ref={menu as RefObject<HTMLUListElement>}
					id={menuId}
					role="menu"
					aria-label={label}
					onKeyDown={onKeyDown}
					className={cn(dropdownContentClass, "fixed mt-0 max-h-[min(24rem,calc(100dvh-1rem))] overflow-y-auto", contentClassName)}
					style={pos ? { top: pos.top, left: pos.left } : { top: 0, left: 0, visibility: "hidden" }}
					data-placement={pos?.placement}
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
				</ul>,
				)}
		</div>
	);
}

/** Renders into document.body so no row, table box or stacking context can cover or clip the menu. */
function portal(node: ReactNode): ReactNode {
	if (typeof document === "undefined") return node;
	return createPortal(node, document.body);
}

export interface MenuPosition {
	top: number;
	left: number;
	placement: "below" | "above";
}

/**
 * Where a menu goes: below its button (4 px gap), right edges aligned for align "end", left edges
 * for "start"; flipped above when it does not fit below and fits better above; always kept 8 px
 * inside the window. Pure.
 */
export function menuPosition(
	trigger: { top: number; bottom: number; left: number; right: number },
	menu: { width: number; height: number },
	vw: number,
	vh: number,
	align: "start" | "end" = "end",
): MenuPosition {
	const gap = 4;
	const edge = 8;
	let left = align === "end" ? trigger.right - menu.width : trigger.left;
	left = Math.max(edge, Math.min(left, vw - edge - menu.width));
	const below = vh - trigger.bottom - gap - edge;
	const above = trigger.top - gap - edge;
	const placement = menu.height > below && above > below ? "above" : "below";
	let top = placement === "below" ? trigger.bottom + gap : trigger.top - gap - menu.height;
	top = Math.max(edge, Math.min(top, vh - edge - menu.height));
	return { top: Math.round(top), left: Math.round(left), placement };
}

function useSafePathname(): string | null {
	try {
		return useRouterState({ select: (state) => state.location.pathname });
	} catch {
		// Rendered without a router (an isolated component test).
		return null;
	}
}
/**
 * The shared menu layer for a menu that keeps its own markup (header and sidebar menus): the panel
 * goes into document.body with fixed position from menuPosition, so it is always on top and inside
 * the window. Before mount (static markup in tests) it renders in place.
 */
export function MenuLayer({ children }: { children: ReactNode }) {
	const [mounted, setMounted] = useState(false);
	useEffect(() => setMounted(true), []);
	return mounted ? portal(children) : children;
}

/** Fixed position for a MenuLayer panel next to its trigger; hidden until measured. */
export function useMenuPlacement(
	open: boolean,
	triggerRef: RefObject<HTMLElement | null>,
	menuRef: RefObject<HTMLElement | null>,
	align: "start" | "end" = "end",
): CSSProperties {
	const [pos, setPos] = useState<MenuPosition | null>(null);
	const place = useCallback(() => {
		const b = triggerRef.current;
		const m = menuRef.current;
		if (!b || !m) return;
		setPos(menuPosition(b.getBoundingClientRect(), m.getBoundingClientRect(), window.innerWidth, window.innerHeight, align));
	}, [align, triggerRef, menuRef]);
	useLayoutEffect(() => {
		if (!open) {
			setPos(null);
			return;
		}
		// Again after the layer mounted, so the panel can be measured.
		const id = requestAnimationFrame(place);
		place();
		window.addEventListener("resize", place);
		window.addEventListener("scroll", place, true);
		return () => {
			cancelAnimationFrame(id);
			window.removeEventListener("resize", place);
			window.removeEventListener("scroll", place, true);
		};
	}, [open, place]);
	return pos ? { position: "fixed", top: pos.top, left: pos.left } : { position: "fixed", top: 0, left: 0, visibility: "hidden" };
}
