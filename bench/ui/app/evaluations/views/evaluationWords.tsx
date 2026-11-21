import { StatusPill } from "@/components/ui/statusPill";
import { useTranslation } from "react-i18next";
import { modeKey, statusKey, statusTone } from "./evaluation.utils";

/** A mode in plain words ("Compare two models"). */
export function ModeLabel({ mode }: { mode: string }) {
	const { t } = useTranslation("evaluations");
	const key = modeKey(mode);
	return <>{key ? t(key) : t("mode.unknown", { mode })}</>;
}

/** A status as a colored pill with a plain word. */
export function EvaluationStatusPill({ status, testId }: { status: string; testId?: string }) {
	const { t } = useTranslation("evaluations");
	const key = statusKey(status);
	return (
		<StatusPill tone={statusTone(status)} testId={testId} data-status={status}>
			{key ? t(key) : t("status.unknown", { status })}
		</StatusPill>
	);
}