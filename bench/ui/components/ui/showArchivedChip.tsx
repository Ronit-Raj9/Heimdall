import "./ufd5I18n";
import { cn } from "@/lib/utils";
import { filterChipClass, filterChipOff, filterChipOn } from "./toolbar";
import { Archive } from "lucide-react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The "Show archived" switch, kept in the URL as ?archived=1 so a shared link keeps it (decision 6). */
export function useShowArchived(): [boolean, (on: boolean) => void] {
	const search = useSearch({ strict: false }) as Record<string, unknown> | undefined;
	const navigate = useNavigate();
	const raw = search?.archived;
	const set = (on: boolean) =>
		void navigate({
			to: ".",
			search: ((prev: Record<string, unknown>) => ({ ...prev, archived: on ? "1" : undefined })) as never,
			replace: true,
		} as never);
	return [raw === "1" || raw === 1, set];
}

/**
 * Splits a list for the switch: archived rows are hidden unless it is on. Works whether or not the
 * server already filtered (a server without archive sends no archived_at, so nothing is hidden). Pure.
 */
export function withoutArchived<T>(items: readonly T[], isArchived: (item: T) => boolean, show: boolean): { shown: T[]; archived: number } {
	const archived = items.filter(isArchived).length;
	return { shown: show ? [...items] : items.filter((item) => !isArchived(item)), archived };
}

/**
 * The chip's label. The count is the server's when it gives one, else the rows seen; with no count
 * and the switch off it says just "Show archived". Pure.
 */
export function showArchivedLabelKey(on: boolean, count: number | undefined): { key: "archive.hide" | "archive.showCount" | "archive.show"; count?: number } {
	if (on) return { key: "archive.hide" };
	if (typeof count === "number" && count > 0) return { key: "archive.showCount", count };
	return { key: "archive.show" };
}

/**
 * The list switch beside the filters, like ShowTestsChip. Shown when the server reported archived
 * rows, or when the count is unknown (the list asked without archived rows), or when it is on.
 */
export function ShowArchivedChip({
	count,
	on,
	onChange,
	className,
	testId,
}: {
	count: number | undefined;
	on: boolean;
	onChange: (on: boolean) => void;
	className?: string;
	testId?: string;
}) {
	const { t } = useTranslation("ufd5");
	if (!on && count === 0) return null;
	const label = showArchivedLabelKey(on, count);
	return (
		<button
			type="button"
			aria-pressed={on}
			onClick={() => onChange(!on)}
			className={cn(filterChipClass, on ? filterChipOn : filterChipOff, className)}
			data-testid={testId ?? "show-archived-chip"}
		>
			<Archive className="size-3.5" aria-hidden="true" />
			{t(label.key, { count: label.count })}
		</button>
	);
}

/** The "Archived" pill on a muted archived row. */
export function ArchivedPill({ at, testId }: { at?: string | null; testId?: string }) {
	const { t, i18n } = useTranslation("ufd5");
	const date = at ? new Date(at) : null;
	const title = date && !Number.isNaN(date.getTime()) ? t("archive.pillTitle", { date: date.toLocaleDateString(i18n.language) }) : undefined;
	return (
		<span
			className="bg-muted text-muted-foreground inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
			title={title}
			data-testid={testId ?? "archived-pill"}
		>
			<Archive className="size-3" aria-hidden="true" />
			{t("archive.pill")}
		</span>
	);
}

/** The muted look of an archived row. */
export const ARCHIVED_ROW_CLASS = "opacity-70";
