import type { LeaderboardGradeSourceMode } from "@/lib/types/bench";
import { fieldStackClass } from "@/components/ui/field";
import { cn } from "@/lib/utils";
import { useId } from "react";
import { useTranslation } from "react-i18next";

/** The board's grade sources, in switch order. */
export const GRADE_SOURCE_MODES: readonly LeaderboardGradeSourceMode[] = ["original", "latest", "reviewed"];

export interface GradeSourcePickerProps {
	value: LeaderboardGradeSourceMode;
	onChange: (value: LeaderboardGradeSourceMode) => void;
	disabled?: boolean;
	testId?: string;
}

/**
 * Picks what the board reads outcomes from: as sealed, each experiment's newest regrade, or the
 * sealed grades with human reviews applied. Reviews and regrades never stack.
 */
export function GradeSourcePicker({ value, onChange, disabled, testId = "grade-source-picker" }: GradeSourcePickerProps) {
	const { t } = useTranslation("bench");
	const id = useId();
	return (
		<div className={fieldStackClass}>
			<span id={id} className="text-muted-foreground text-xs">
				{t("review.gradeSource.label")}
			</span>
			<div
				role="group"
				aria-labelledby={id}
				className="bg-muted inline-flex h-(--control-h) w-fit items-center gap-0.5 rounded-md border p-0.5"
				data-testid={testId}
			>
				{GRADE_SOURCE_MODES.map((mode) => {
					const active = mode === value;
					return (
						<button
							key={mode}
							type="button"
							aria-pressed={active}
							title={t(`review.gradeSource.${mode}Hint`)}
							disabled={disabled}
							onClick={() => {
								if (!active) onChange(mode);
							}}
							data-testid={`${testId}-${mode}`}
							className={cn(
								"focus-visible:ring-ring/50 h-full cursor-pointer rounded-[5px] px-3 text-xs font-medium transition-colors duration-100 focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
								active ? "bg-card text-foreground shadow-sm dark:bg-accent" : "text-muted-foreground hover:text-foreground",
							)}
						>
							{t(`review.gradeSource.${mode}`)}
						</button>
					);
				})}
			</div>
		</div>
	);
}