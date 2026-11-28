import "@/components/settings/ufl8I18n";

import { NativeSelect } from "@/components/ui/select";
import { InlineError } from "@/components/runs/screenStates";
import { useSetJobPriorityMutation } from "@/lib/store/apis/jobPriorityApi";
import { useTranslation } from "react-i18next";

/** -10 to 10 in the steps an admin needs: lowest, lower, normal, higher, highest. */
export const PRIORITY_STEPS = [10, 5, 0, -5, -10] as const;

/** Admin only, queued jobs only: move the job up or down the claim order (POST /jobs/{id}/priority). */
export function JobPriority({ jobId, priority }: { jobId: string; priority?: number }) {
	const { t } = useTranslation("ufl8");
	const [set, state] = useSetJobPriorityMutation();
	const value = priority ?? 0;
	return (
		<span className="flex flex-wrap items-center gap-2 text-xs" data-testid="job-priority">
			<label className="flex items-center gap-2">
				<span className="text-muted-foreground">{t("jobPriority.label")}</span>
				<NativeSelect
					value={String(value)}
					disabled={state.isLoading}
					onChange={(e) => void set({ id: jobId, priority: Number(e.target.value) })}
					data-testid="job-priority-select"
				>
					{(PRIORITY_STEPS.includes(value as (typeof PRIORITY_STEPS)[number]) ? PRIORITY_STEPS : [...PRIORITY_STEPS, value]).map((p) => (
						<option key={p} value={String(p)}>
							{t(`jobPriority.step.${p}`, { defaultValue: String(p) })}
						</option>
					))}
				</NativeSelect>
			</label>
			<InlineError error={state.error} testId="job-priority-error" />
		</span>
	);
}
