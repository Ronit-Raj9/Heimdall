import { cn } from "@/lib/utils";
import { Search } from "lucide-react";
import { useId, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/**
 * The compact option list (Spec 7): one 32 to 40 px row per choice instead of a card, a search box
 * once there are more than eight, optional groups with "Select all", and a sticky "3 of 50
 * selected" count in multi mode. Every row is its own label, so the whole row is the target and
 * the native radio or checkbox keeps keyboard and screen reader behaviour.
 */
export interface OptionItem {
	id: string;
	label: string;
	/** Short mono detail on the same line (a model id, a task count). */
	meta?: string;
	/** Chips shown inline after the label. */
	badges?: ReactNode;
	/** Shown as a second line only while the row is selected (and as the row title). */
	description?: string;
	disabled?: boolean;
	/** Visible under the label when disabled. */
	disabledReason?: string;
	/** The id of the group in `groups` this item belongs to. */
	group?: string;
}

export interface OptionListProps {
	mode: "single" | "multi";
	items: readonly OptionItem[];
	value: string | readonly string[];
	onChange(value: string | string[]): void;
	/** Placeholder of the search box, shown when items.length > 8. */
	searchPlaceholder?: string;
	groups?: readonly { id: string; label: string }[];
	/** Multi only: a "Select all" toggle per group (or for the whole list without groups). */
	selectAll?: boolean;
	/** Accessible group name (fieldset legend). */
	label: string;
	testId: string;
	className?: string;
}

/** Items whose label, meta or description contain the query (case-insensitive). Pure. */
export function filterOptions(items: readonly OptionItem[], query: string): OptionItem[] {
	const q = query.trim().toLowerCase();
	if (!q) return [...items];
	return items.filter((item) => [item.label, item.meta, item.description].some((text) => text?.toLowerCase().includes(q)));
}

/** Items split by group in the order of `groups`; items without a known group come last. Pure. */
export function groupOptions(items: readonly OptionItem[], groups: readonly { id: string; label: string }[] | undefined) {
	if (!groups || groups.length === 0) return [{ id: "", label: "", items: [...items] }];
	const known = new Set(groups.map((group) => group.id));
	const out = groups.map((group) => ({ ...group, items: items.filter((item) => item.group === group.id) }));
	const rest = items.filter((item) => !item.group || !known.has(item.group));
	if (rest.length > 0) out.push({ id: "", label: "", items: rest });
	return out.filter((group) => group.items.length > 0);
}

/** The next multi value after toggling "Select all" for some items: all on when any is off, else all off. Pure. */
export function toggleAll(value: readonly string[], items: readonly OptionItem[]): string[] {
	const ids = items.filter((item) => !item.disabled).map((item) => item.id);
	const allOn = ids.every((id) => value.includes(id));
	if (allOn) return value.filter((id) => !ids.includes(id));
	return [...value, ...ids.filter((id) => !value.includes(id))];
}

export function OptionList({ mode, items, value, onChange, searchPlaceholder, groups, selectAll, label, testId, className }: OptionListProps) {
	const { t } = useTranslation("ufs1");
	const [query, setQuery] = useState("");
	const name = useId();
	const selected = useMemo(() => (Array.isArray(value) ? (value as readonly string[]) : value ? [value as string] : []), [value]);
	const shown = filterOptions(items, query);
	const sections = groupOptions(shown, groups);
	const searchable = items.length > 8;

	const pick = (id: string, on: boolean) => {
		if (mode === "single") onChange(id);
		else onChange(on ? [...selected, id] : selected.filter((v) => v !== id));
	};

	return (
		<fieldset className={cn("flex min-w-0 flex-col gap-2", className)} data-testid={testId}>
			<legend className="sr-only">{label}</legend>
			{searchable && (
				<label className="relative block max-w-md">
					<span className="sr-only">{searchPlaceholder ?? t("optionList.search")}</span>
					<Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" aria-hidden="true" />
					<input
						type="search"
						value={query}
						onChange={(e) => setQuery(e.target.value)}
						placeholder={searchPlaceholder ?? t("optionList.search")}
						className="border-input bg-card placeholder:text-neutral-500 focus-visible:border-primary h-(--control-h) w-full rounded-md border pr-3 pl-8 text-sm"
						data-testid={`${testId}-search`}
					/>
				</label>
			)}
			{mode === "multi" && (
				<p className="bg-card text-muted-foreground sticky top-0 z-10 py-1 text-xs" aria-live="polite" data-testid={`${testId}-count`}>
					{t("optionList.selected", { count: selected.length, total: items.length })}
				</p>
			)}
			{shown.length === 0 && <p className="text-muted-foreground text-sm">{t("optionList.noMatch")}</p>}
			{sections.map((section) => (
				<div key={section.id || "rest"} className="flex flex-col">
					{(section.label || (mode === "multi" && selectAll)) && (
						<div className="flex items-center gap-2 px-1 pb-1">
							{section.label && <h3 className="text-muted-foreground text-xs font-semibold">{section.label}</h3>}
							{mode === "multi" && selectAll && (
								<button
									type="button"
									className="text-primary ml-auto min-h-6 rounded-sm px-1 text-xs hover:underline"
									onClick={() => onChange(toggleAll(selected, section.items))}
									data-testid={`${testId}-all-${section.id || "rest"}`}
								>
									{section.items.filter((item) => !item.disabled).every((item) => selected.includes(item.id)) ? t("optionList.clearAll") : t("optionList.selectAll")}
								</button>
							)}
						</div>
					)}
					<ul className="bg-card divide-y rounded-lg border">
						{section.items.map((item) => {
							const on = selected.includes(item.id);
							return (
								<li key={item.id}>
									<label
										className={cn(
											"flex min-h-(--row-h) cursor-pointer items-start gap-3 px-3 py-2 text-sm",
											on && "bg-primary-soft",
											item.disabled ? "cursor-not-allowed opacity-70" : "hover:bg-muted",
										)}
										title={item.description}
										data-testid={`${testId}-option-${item.id}`}
										data-selected={on || undefined}
									>
										<input
											type={mode === "single" ? "radio" : "checkbox"}
											name={mode === "single" ? name : undefined}
											className="accent-primary mt-0.5 size-4 shrink-0"
											checked={on}
											disabled={item.disabled}
											onChange={(e) => pick(item.id, e.target.checked)}
										/>
										<span className="flex min-w-0 flex-1 flex-col gap-0.5">
											<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
												<span className="text-foreground font-medium">{item.label}</span>
												{item.meta && <span className="text-muted-foreground truncate font-mono text-xs">{item.meta}</span>}
												{item.badges}
											</span>
											{item.disabled && item.disabledReason && <span className="text-muted-foreground text-xs">{item.disabledReason}</span>}
											{on && item.description && <span className="text-muted-foreground text-xs">{item.description}</span>}
										</span>
									</label>
								</li>
							);
						})}
					</ul>
				</div>
			))}
		</fieldset>
	);
}
