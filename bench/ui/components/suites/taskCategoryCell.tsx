import "@/components/guard/w444I18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	TASK_CATEGORIES,
	useGetSuiteCategoriesQuery,
	useSetTaskCategoryMutation,
	type TaskCategoryLabel,
} from "@/lib/store/apis/taskCategoriesApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/** The "Kind of work" column data of a task table: the labels by task id, and whether this person may change them. */
export interface TaskCategories {
	labels: Record<string, TaskCategoryLabel> | undefined;
	canEdit: boolean;
}

/** Reads the kind of work of a benchmark's tasks while the column is shown. Members change it on the org's own benchmarks only. */
export function useTaskCategories(suite: SuiteSummary, shown: boolean): TaskCategories {
	const session = useSession();
	const q = useGetSuiteCategoriesQuery(suite.id, { skip: !shown });
	return { labels: q.data?.labels, canEdit: !suite.builtin && suite.source !== "library" && canUse(session, "member") };
}

/** The word for a label: the category's plain name, with "(unsure)" when a rule was not sure. */
export function categoryWord(label: TaskCategoryLabel | undefined, names: Record<string, string>, none: string, unsure: string): string {
	if (!label?.category) return none;
	const name = names[label.category] ?? label.category;
	return label.sure ? name : `${name} ${unsure}`;
}

/** A pick in the picker: saves a known kind of work; the "Not labelled" entry saves nothing. */
export function pickCategory(
	save: (arg: { suiteId: string; task: string; category: string }) => unknown,
	suiteId: string,
	task: string,
	value: string,
) {
	if ((TASK_CATEGORIES as readonly string[]).includes(value)) save({ suiteId, task, category: value });
}

/** One task's kind of work: a picker for members on the org's own benchmark, the word otherwise. */
export function TaskCategoryCell({
	suiteId,
	taskId,
	label,
	canEdit,
}: {
	suiteId: string;
	taskId: string;
	label?: TaskCategoryLabel;
	canEdit: boolean;
}) {
	const { t } = useTranslation("w444");
	const names = t("categories.names", { returnObjects: true }) as Record<string, string>;
	const word = categoryWord(label, names, t("categories.none"), t("categories.unsure"));
	if (!canEdit) {
		return <span data-testid={`suites-task-category-${taskId}`}>{word}</span>;
	}
	return <CategoryPicker suiteId={suiteId} taskId={taskId} label={label} names={names} />;
}

function CategoryPicker({
	suiteId,
	taskId,
	label,
	names,
}: {
	suiteId: string;
	taskId: string;
	label?: TaskCategoryLabel;
	names: Record<string, string>;
}) {
	const { t } = useTranslation("w444");
	const [save, saved] = useSetTaskCategoryMutation();
	return (
		<span className="flex flex-col items-start gap-1">
			<select
				className="h-8 rounded-md border px-1.5 text-xs"
				aria-label={t("categories.change", { task: taskId })}
				value={label?.category ?? ""}
				disabled={saved.isLoading}
				onChange={(e) => pickCategory(save, suiteId, taskId, e.target.value)}
				data-testid={`suites-task-category-${taskId}`}
			>
				{!label?.category && <option value="">{t("categories.none")}</option>}
				{TASK_CATEGORIES.map((c) => (
					<option key={c} value={c}>
						{c === label?.category && !label.sure ? `${names[c] ?? c} ${t("categories.unsure")}` : (names[c] ?? c)}
					</option>
				))}
			</select>
			{saved.isError && (
				<span className="text-destructive text-[11px]" role="alert">
					{t("categories.saveFailed")}
				</span>
			)}
		</span>
	);
}