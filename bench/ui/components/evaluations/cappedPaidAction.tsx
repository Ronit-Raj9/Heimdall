import "@/components/reportcard/uff5I18n";
import { Input } from "@/components/ui/input";
import { PaidButton, type PaidButtonProps } from "@/components/ui/paidButton";
import { formatUsd } from "@/lib/money";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { UNKNOWN_ESTIMATE_CAP_USD, suggestedBudget } from "./paidCaps";

export interface CappedPaidActionProps extends Pick<
	PaidButtonProps,
	"verb" | "variant" | "size" | "isLoading" | "disabled" | "confirmText" | "kind"
> {
	/** The estimate in USD; null when not known (the cap then starts at $1.00 and the confirm says so). */
	estimateUsd: number | null;
	estimateRuns?: number;
	onConfirm(capUsd: number): void | Promise<unknown>;
	testId: string;
}

/**
 * A paid evaluation action with its own spending cap (round 4, F5): a cap field that starts at the suggested
 * budget for the estimate, and the shared PaidButton that names the cap and always confirms with the amount.
 */
export function CappedPaidAction({
	estimateUsd,
	estimateRuns,
	onConfirm,
	testId,
	verb,
	confirmText,
	kind = "retry",
	...rest
}: CappedPaidActionProps) {
	const { t } = useTranslation("uff5");
	const id = useId();
	const [cap, setCap] = useState<number>(() => suggestedBudget(estimateUsd) ?? UNKNOWN_ESTIMATE_CAP_USD);
	const valid = Number.isFinite(cap) && cap > 0;
	const note = estimateUsd === null ? t("cap.unknown") : t("cap.estimate", { usd: formatUsd(estimateUsd) });
	return (
		<span className="inline-flex flex-wrap items-center gap-2" data-testid={`${testId}-group`}>
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("cap.label")}
			</label>
			<Input
				id={id}
				type="number"
				size="number"
				min={0.01}
				step={0.05}
				value={Number.isFinite(cap) ? cap : ""}
				onChange={(e) => setCap(Number(e.target.value))}
				aria-describedby={`${id}-note`}
				data-testid={`${testId}-cap`}
			/>
			<PaidButton
				{...rest}
				verb={verb}
				capUsd={valid ? cap : null}
				estimateUsd={estimateUsd}
				estimateRuns={estimateRuns}
				confirm="always"
				kind={kind}
				estimateAt="title"
				confirmText={confirmText}
				disabled={rest.disabled || !valid}
				onConfirm={() => onConfirm(cap)}
				testId={testId}
			/>
			<span id={`${id}-note`} className="text-muted-foreground text-xs" data-testid={`${testId}-note`}>
				{valid ? note : t("cap.needCap")}
			</span>
		</span>
	);
}