import { optionsState, type OptionsQuery } from "@/lib/hooks/useOptionsState";
import { isTestObject } from "@/lib/testObjects";
import { cn } from "@/lib/utils";
import { ChevronDown, X } from "lucide-react";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { menuPosition, type MenuPosition } from "./dropdownMenu";
import { useTranslation } from "react-i18next";
import "./ufc0I18n";

/**
 * The searchable picker for models and benchmarks (X-99): a text box that filters a grouped list
 * as you type. ArrowDown and ArrowUp move, Enter picks, Escape closes (a second Escape clears the
 * text), Home and End jump. Test objects ("zz-test-..." and stand-ins) are hidden unless the query
 * asks for them or the current value is one. Built without a new dependency.
 */
export interface ComboboxItem {
	value: string;
	label: string;
	/** A group heading, for example the provider. Items keep their order inside a group. */
	group?: string;
	disabled?: boolean;
	/** Short muted text on the same row (an id, a price, a reason when disabled). */
	hint?: string;
	/** Marks a test object by hand; otherwise isTestObject(value) or isTestObject(label) decides. */
	test?: boolean;
}

export interface ComboboxProps {
	/** Visible label; also the accessible name. */
	label: string;
	/** Hide the visible label (the page shows its own); it stays the accessible name. */
	hideLabel?: boolean;
	items: readonly ComboboxItem[];
	value: string | null;
	onChange(value: string | null): void;
	placeholder?: string;
	/** Shown when the search matches nothing. */
	emptyText?: string;
	/** The query that loads the items: loading and error states show in the list (useOptionsState). */
	query?: OptionsQuery;
	/** Retry for a failed query, usually `query.refetch`. */
	onRetry?: () => void;
	/** Keep test objects in the list. Default false. */
	showTests?: boolean;
	/** Show a small clear button when a value is set. */
	clearable?: boolean;
	disabled?: boolean;
	className?: string;
	testId: string;
}

/** Whether an item is a test object. Pure. */
export function isTestItem(item: ComboboxItem): boolean {
	return item.test ?? (isTestObject(item.value) || isTestObject(item.label));
}

/**
 * The items to list for a query: label, value, hint or group contain every word (case-insensitive).
 * Test items stay hidden unless showTests, the query mentions "test", or the item is the value.
 * Returns the shown items and how many test items were hidden. Pure.
 */
export function filterCombo(items: readonly ComboboxItem[], query: string, opts: { showTests?: boolean; value?: string | null } = {}) {
	const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
	const wantsTests = opts.showTests || words.some((w) => w.includes("test"));
	let hidden = 0;
	const shown: ComboboxItem[] = [];
	for (const item of items) {
		if (!wantsTests && item.value !== opts.value && isTestItem(item)) {
			hidden++;
			continue;
		}
		const text = `${item.label} ${item.value} ${item.hint ?? ""} ${item.group ?? ""}`.toLowerCase();
		if (words.every((w) => text.includes(w))) shown.push(item);
	}
	return { shown, hidden };
}

/** Items split by group in first-seen order; ungrouped items first. Pure. */
export function groupCombo(items: readonly ComboboxItem[]): { group: string; items: ComboboxItem[] }[] {
	const out: { group: string; items: ComboboxItem[] }[] = [];
	for (const item of items) {
		const g = item.group ?? "";
		let section = out.find((s) => s.group === g);
		if (!section) {
			section = { group: g, items: [] };
			if (g === "") out.unshift(section);
			else out.push(section);
		}
		section.items.push(item);
	}
	return out;
}

/** The next active index for a key over the enabled items, or null when the key does not move. Pure. */
export function comboKeyTarget(key: string, current: number, items: readonly ComboboxItem[]): number | null {
	const enabled = items.map((item, i) => (item.disabled ? -1 : i)).filter((i) => i >= 0);
	if (enabled.length === 0) return null;
	const pos = enabled.indexOf(current);
	switch (key) {
		case "ArrowDown":
			return enabled[pos < 0 ? 0 : (pos + 1) % enabled.length];
		case "ArrowUp":
			return enabled[pos < 0 ? enabled.length - 1 : (pos - 1 + enabled.length) % enabled.length];
		case "Home":
			return enabled[0];
		case "End":
			return enabled[enabled.length - 1];
		default:
			return null;
	}
}

export function Combobox({
	label,
	hideLabel,
	items,
	value,
	onChange,
	placeholder,
	emptyText,
	query,
	onRetry,
	showTests,
	clearable,
	disabled,
	className,
	testId,
}: ComboboxProps) {
	const { t } = useTranslation("ufc0");
	const id = useId();
	const listId = `${id}-list`;
	const root = useRef<HTMLDivElement>(null);
	const input = useRef<HTMLInputElement>(null);
	const [open, setOpen] = useState(false);
	const [text, setText] = useState("");
	const [active, setActive] = useState(-1);
	const selected = items.find((item) => item.value === value) ?? null;
	const state = query ? optionsState(query, items.length) : items.length === 0 ? "empty" : "ready";
	const { shown, hidden } = useMemo(() => filterCombo(items, text, { showTests, value }), [items, text, showTests, value]);
	const sections = useMemo(() => groupCombo(shown), [shown]);
	// Flat order as rendered, so the active index matches what the eye sees.
	const flat = useMemo(() => sections.flatMap((s) => s.items), [sections]);

	useEffect(() => {
		if (!open) return;
		const onDown = (e: MouseEvent) => {
			const t = e.target as Node;
			if (root.current?.contains(t) || pop.current?.contains(t)) return;
			close();
		};
		document.addEventListener("mousedown", onDown);
		return () => document.removeEventListener("mousedown", onDown);
	});

	// The list renders in document.body (round 3) so no table, card or dialog box clips or covers it;
	// it sits under the box at the box's width and flips up near the bottom of the window.
	const box = useRef<HTMLDivElement>(null);
	const pop = useRef<HTMLDivElement>(null);
	const [pos, setPos] = useState<(MenuPosition & { width: number }) | null>(null);
	const place = useCallback(() => {
		const b = box.current;
		const p = pop.current;
		if (!b || !p) return;
		const r = b.getBoundingClientRect();
		const width = Math.min(Math.max(r.width, 224), window.innerWidth - 16);
		const next = { ...menuPosition(r, { width, height: p.getBoundingClientRect().height }, window.innerWidth, window.innerHeight, "start"), width };
		setPos((prev) =>
			prev && prev.top === next.top && prev.left === next.left && prev.width === next.width && prev.placement === next.placement ? prev : next,
		);
	}, []);
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
	}, [open, text, place, flat.length]);

	const close = () => {
		setOpen(false);
		setText("");
		setActive(-1);
	};
	const pick = (item: ComboboxItem) => {
		if (item.disabled) return;
		onChange(item.value);
		close();
	};
	const openList = () => {
		if (disabled) return;
		setOpen(true);
		const at = flat.findIndex((item) => item.value === value);
		setActive(at >= 0 ? at : (comboKeyTarget("Home", -1, flat) ?? -1));
	};

	const onKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
		if (e.key === "Escape") {
			if (open) {
				e.preventDefault();
				e.stopPropagation();
				close();
			} else if (text) setText("");
			return;
		}
		if (e.key === "Enter") {
			if (open && flat[active]) {
				e.preventDefault();
				pick(flat[active]);
			}
			return;
		}
		if (e.key === "Tab") {
			if (open) close();
			return;
		}
		if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
			e.preventDefault();
			openList();
			return;
		}
		if (!open) return;
		if ((e.key === "Home" || e.key === "End") && text) return;
		const next = comboKeyTarget(e.key, active, flat);
		if (next === null) return;
		e.preventDefault();
		setActive(next);
		document.getElementById(`${id}-opt-${next}`)?.scrollIntoView?.({ block: "nearest" });
	};

	const activeId = open && flat[active] ? `${id}-opt-${active}` : undefined;
	return (
		<div ref={root} className={cn("relative flex min-w-0 flex-col gap-1.5", className)} data-testid={testId}>
			<label htmlFor={`${id}-input`} className={cn("text-sm font-medium", hideLabel && "sr-only")}>
				{label}
			</label>
			{/* The bordered box holds the text and its buttons side by side, so nothing is drawn over the text. */}
			<div
				ref={box}
				className={cn(
					"border-input bg-card focus-within:border-primary flex h-(--control-h) w-full min-w-0 items-center rounded-md border shadow-xs focus-within:ring-2 focus-within:ring-(--focus)/40",
					disabled && "cursor-not-allowed opacity-50",
				)}
			>
				<input
					ref={input}
					id={`${id}-input`}
					type="text"
					role="combobox"
					aria-expanded={open}
					aria-controls={listId}
					aria-autocomplete="list"
					aria-activedescendant={activeId}
					autoComplete="off"
					disabled={disabled}
					value={open ? text : (selected?.label ?? "")}
					placeholder={selected ? selected.label : (placeholder ?? t("combobox.placeholder"))}
					onChange={(e) => {
						setText(e.target.value);
						if (!open) setOpen(true);
						setActive(0);
					}}
					onFocus={openList}
					onClick={() => !open && openList()}
					onKeyDown={onKeyDown}
					className="-my-px h-[calc(100%+2px)] w-full min-w-0 flex-1 rounded-md bg-transparent pl-3 text-base outline-none placeholder:text-neutral-500 disabled:cursor-not-allowed md:text-sm"
					data-testid={`${testId}-input`}
				/>
				<span className="flex shrink-0 items-center gap-0.5 pr-1">
					{clearable && value && !disabled && (
						<button
							type="button"
							aria-label={t("combobox.clear")}
							onClick={() => {
								onChange(null);
								input.current?.focus();
							}}
							className="text-muted-foreground hover:text-foreground inline-flex size-6 items-center justify-center rounded-sm"
							data-testid={`${testId}-clear`}
						>
							<X className="size-3.5" aria-hidden="true" />
						</button>
					)}
					<button
						type="button"
						tabIndex={-1}
						aria-label={t("combobox.open")}
						disabled={disabled}
						onClick={() => (open ? close() : (input.current?.focus(), openList()))}
						className="text-muted-foreground inline-flex size-6 items-center justify-center rounded-sm"
					>
						<ChevronDown className="size-4" aria-hidden="true" />
					</button>
				</span>
			</div>
			{open &&
				createPortal(
				<div
					ref={pop}
					className="bg-card text-card-foreground fixed z-50 rounded-lg border p-1 shadow-(--shadow-pop)"
					style={pos ? { top: pos.top, left: pos.left, width: pos.width } : { top: 0, left: 0, visibility: "hidden" }}
					data-placement={pos?.placement}
				>
					<ul id={listId} role="listbox" aria-label={label} className="max-h-72 overflow-y-auto" data-testid={`${testId}-list`}>
						{state === "loading" && <li className="text-muted-foreground px-2.5 py-2 text-sm">{t("combobox.loading")}</li>}
						{state === "error" && (
							<li className="flex items-center gap-2 px-2.5 py-2 text-sm" role="alert">
								<span className="text-danger-ink">{t("combobox.error")}</span>
								{onRetry && (
									<button
										type="button"
										onClick={onRetry}
										className="text-primary min-h-6 rounded-sm px-1 hover:underline"
										data-testid={`${testId}-retry`}
									>
										{t("combobox.retry")}
									</button>
								)}
							</li>
						)}
						{state === "empty" && <li className="text-muted-foreground px-2.5 py-2 text-sm">{t("combobox.none")}</li>}
						{state === "ready" && flat.length === 0 && (
							<li className="text-muted-foreground px-2.5 py-2 text-sm">{emptyText ?? t("combobox.empty")}</li>
						)}
						{state === "ready" &&
							sections.map((section) => (
								<li key={section.group || "rest"} role="presentation">
									{section.group && <div className="text-muted-foreground px-2.5 pt-2 pb-1 text-xs font-semibold">{section.group}</div>}
									<ul role="group" aria-label={section.group || undefined}>
										{section.items.map((item) => {
											const index = flat.indexOf(item);
											return (
												<li
													key={item.value}
													id={`${id}-opt-${index}`}
													role="option"
													aria-selected={item.value === value}
													aria-disabled={item.disabled || undefined}
													onMouseDown={(e) => e.preventDefault()}
													onClick={() => pick(item)}
													onMouseMove={() => !item.disabled && setActive(index)}
													className={cn(
														"flex min-h-(--control-h) cursor-pointer items-center gap-2 rounded-md px-2.5 py-1 text-sm",
														index === active && "bg-muted",
														item.value === value && "font-medium",
														item.disabled && "cursor-not-allowed opacity-60",
													)}
													data-testid={`${testId}-option-${item.value}`}
												>
													<span className="min-w-0 flex-1 truncate">{item.label}</span>
													{item.hint && <span className="text-muted-foreground shrink-0 font-mono text-xs">{item.hint}</span>}
												</li>
											);
										})}
									</ul>
								</li>
							))}
					</ul>
					{state === "ready" && hidden > 0 && (
						<p className="text-muted-foreground border-t px-2.5 pt-1.5 pb-1 text-xs" data-testid={`${testId}-hidden`}>
							{t("combobox.hiddenTests", { count: hidden })}
						</p>
					)}
				</div>,
				document.body,
				)}
		</div>
	);
}